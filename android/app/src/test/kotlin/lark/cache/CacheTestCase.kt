package lark.cache

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.test.TestResult
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import lark.testing.FakeClock
import lark.testing.FakeLarkApi
import lark.testing.FakeNetwork
import lark.testing.FakeScheduler
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.rules.TemporaryFolder
import java.io.File
import java.time.Instant

/** iOS `CacheTestCase`: a cache on `lark/<server>/5` in a temporary folder, with fakes for everything outside it. */
@OptIn(ExperimentalCoroutinesApi::class)
abstract class CacheTestCase {
    @get:Rule val folder = TemporaryFolder()

    lateinit var root: File
    lateinit var scratch: File
    val api = FakeLarkApi()
    val network = FakeNetwork()
    val clock = FakeClock()
    val scheduler = FakeScheduler()
    lateinit var cache: CacheStore
    lateinit var testScope: TestScope
    /** The cache's coroutines: on the test's scheduler, so [settle] runs them (a `backgroundScope` is left out of it). */
    lateinit var cacheScope: CoroutineScope
    val t0: Instant = Instant.ofEpochSecond(1_800_000_000)

    /** Free space the cache sees (plenty, unless a test says otherwise). */
    var free: Long? = 100L shl 30

    fun at(seconds: Long): Instant = t0.plusSeconds(seconds)

    /** Runs the body with the cache's coroutines on the test's dispatcher (iOS `async` tests with `waitUntil`). */
    fun cacheTest(body: suspend TestScope.() -> Unit): TestResult = runTest {
        testScope = this
        val job = Job()
        cacheScope = CoroutineScope(job + StandardTestDispatcher(testScheduler))
        root = File(folder.newFolder("base"), "lark/5")
        scratch = folder.newFolder("scratch")
        clock.current = t0
        beforeCache()
        cache = makeCache()
        try { body() } finally { job.cancel() }
    }

    open fun beforeCache() {}

    /** A cache on [root], as after a process launch (the previous one flushed its index, as on backgrounding). */
    fun makeCache(indexDelay: Double = CacheStore.INDEX_WRITE_DELAY_S, move: (File, File) -> Unit = ::moveAtomic): CacheStore {
        if (this::cache.isInitialized) cache.flush()
        return CacheStore(root, api, network, clock, cacheScope, scheduler, { free }, indexDelaySeconds = indexDelay, move = move)
    }

    fun put(id: Int, bytes: Int, fav: Boolean, at: Instant) {
        clock.current = at
        val f = File(scratch, "$id.m4a")
        f.writeBytes(ByteArray(bytes) { 1 })
        val t = trackModel(id)
        cache.store(id.toLong(), f, t.track, t.json, fav)
    }

    /** Lets every coroutine run until it waits for something (a held download) or ends. */
    suspend fun settle() { testScope.advanceUntilIdle() }

    /** Waits until every download the fake API started has finished and been stored. */
    suspend fun settle(downloads: Int) {
        settle()
        assertTrue("downloads finished: ${api.finishedDownloads}", api.finishedDownloads >= downloads && !cache.isDownloading)
    }

    fun indexJson(): JsonObject {
        cache.flush()
        return Json.parseToJsonElement(File(root, "index.json").readText()).jsonObject
    }

    fun favoriteIds(): List<Long> = cache.cachedFavorites().map { it.track.id }

    fun assertIds(expected: List<Long>, actual: List<Long>) = assertEquals(expected, actual)
}
