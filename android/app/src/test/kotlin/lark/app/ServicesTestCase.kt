package lark.app

import android.content.Context
import android.content.SharedPreferences
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.TestResult
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import lark.bridge.NativeEvent
import lark.cache.CacheProviding
import lark.player.PlayerEngine
import lark.testing.FakeBackend
import lark.testing.FakeCache
import lark.testing.FakeClock
import lark.testing.FakeNetwork
import lark.testing.FakeScheduler
import lark.testing.FakeWebData
import lark.testing.MemorySecretStore
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Rule
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.net.InetAddress
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList

/**
 * The app services on fakes (iOS `makeServices` in AppServicesTests, CacheWiringTests, StayOfflineTests): the real
 * `LarkApi`, `AuthStore` and cache against a local [server] (iOS `StubURLProtocol`), a fake backend, network, scheduler,
 * clock and web view data. Each test runs in [servicesTest]: the services' coroutines run on the test's dispatcher, and
 * [waitUntil] lets real HTTP finish.
 */
@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
abstract class ServicesTestCase {
    @get:Rule val folder = TemporaryFolder()

    val server = MockWebServer()
    /** What the server answers (iOS `StubURLProtocol.handler`); a 404 by default. Runs on the server's thread. */
    @Volatile var handler: (RecordedRequest) -> MockResponse = { MockResponse().setResponseCode(404) }
    val requests = CopyOnWriteArrayList<RecordedRequest>()
    val network = FakeNetwork()
    val backend = FakeBackend()
    val scheduler = FakeScheduler()
    val clock = FakeClock()
    val web = FakeWebData()
    val sent = mutableListOf<NativeEvent>()
    lateinit var dir: File
    lateinit var serverUrl: String
    lateinit var scope: TestScope
    val context: Context get() = ApplicationProvider.getApplicationContext()

    @Before fun startServer() {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse { requests += request; return handler(request) }
        }
        server.start(InetAddress.getByName("localhost"), 0)
        serverUrl = "http://localhost:${server.port}/"     // plain http is allowed for localhost
        dir = folder.newFolder("svc")
    }

    @After fun stopServer() { server.shutdown() }

    fun newPrefs(): SharedPreferences = context.getSharedPreferences("lark.tests.${UUID.randomUUID()}", Context.MODE_PRIVATE)

    fun ok(body: String, type: String = "application/json") = MockResponse().setHeader("Content-Type", type).setBody(body)
    fun status(code: Int) = MockResponse().setResponseCode(code)
    fun requestsTo(path: String) = requests.filter { it.requestUrl?.encodedPath == path }

    /** A signed-in user [user] with token T1 on [serverUrl] (or signed out with `user = null`), as after a relaunch. */
    fun makeServices(
        user: Long? = 5,
        prefs: SharedPreferences = newPrefs(),
        cache: CacheProviding? = FakeCache(),
        dataDirectory: File = File(dir, "data"),
        cacheRoot: File = File(dir, "caches"),
        server: String? = serverUrl,
        network: FakeNetwork = this.network,
        backend: FakeBackend = this.backend,
        refresh: RefreshScheduler = RefreshScheduler.none,
        onEngine: (PlayerEngine?) -> Unit = {},
    ): AppServices {
        val secrets = MemorySecretStore()
        if (user != null) { secrets.set("token", "T1"); secrets.set("userId", user.toString()) }
        if (server != null && !prefs.contains(AppServices.SERVER_URL_KEY)) prefs.edit().putString(AppServices.SERVER_URL_KEY, server).commit()
        return AppServices(
            prefs = prefs, secrets = secrets, webData = web, http = OkHttpClient(), dataDirectory = dataDirectory,
            backend = backend, network = network, cacheRoot = cacheRoot, scope = scope, scheduler = scheduler, clock = clock,
            cache = cache, refresh = refresh, onEngine = onEngine,
        )
    }

    fun servicesTest(body: suspend TestScope.() -> Unit): TestResult = runTest {
        scope = this
        body()
    }

    /** Runs what is due on the test dispatcher and waits (real time) for HTTP until [condition] holds. */
    suspend fun TestScope.waitUntil(seconds: Double = 10.0, state: () -> String = { "" }, condition: () -> Boolean) {
        val end = System.nanoTime() + (seconds * 1e9).toLong()
        while (true) {
            advanceUntilIdle()
            if (condition()) return
            if (System.nanoTime() > end) fail("timed out waiting ${state()}")
            withContext(Dispatchers.Default) { delay(10) }
        }
    }

    /** Lets everything in flight settle for a while (real time), then checks nothing more happened. */
    suspend fun TestScope.settle(ms: Long = 150) {
        advanceUntilIdle()
        withContext(Dispatchers.Default) { delay(ms) }
        advanceUntilIdle()
    }

    fun queueFile(user: Long) = File(File(dir, "data"), "queues-$user.json")
    fun eventsFile(user: Long) = File(File(dir, "data"), "events-$user.json")
}
