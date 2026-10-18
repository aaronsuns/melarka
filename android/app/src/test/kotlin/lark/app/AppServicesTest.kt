package lark.app

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.withContext
import lark.api.LarkError
import lark.bridge.BridgeView
import lark.bridge.NativeEvent
import lark.bridge.NativePrefs
import lark.bridge.WebMessage
import lark.player.MediaSource
import lark.testing.FakeCache
import lark.testing.FakeNetwork
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File
import java.util.Collections
import java.util.concurrent.TimeUnit

/** iOS `AppServicesTests`, `PageLoadFailureTests` (the services' part) and `StayOfflineTests` (the page). */
@OptIn(ExperimentalCoroutinesApi::class)
class AppServicesTest : ServicesTestCase() {
    /** `GET /api/v1/me` answers user [id] for any token. */
    private fun me(id: Long) = { r: okhttp3.mockwebserver.RecordedRequest ->
        if (r.requestUrl?.encodedPath == "/api/v1/me") ok("""{"id":$id}""") else status(404)
    }

    private fun accepted() = ok("""{"accepted":1}""")

    // MARK: Intents

    /** What the media service's cold-start hold asks: a shortcut's or car autoplay's run is still preparing playback. */
    @Test fun intentRunningWhileARunIsPreparing() = servicesTest {
        val s = makeServices()
        assertFalse(s.intentRunning)
        val gate = kotlinx.coroutines.CompletableDeferred<Unit>()
        s.runIntent { gate.await() }                 // a shuffle waiting for the server
        assertTrue(s.intentRunning)
        gate.complete(Unit)
        advanceUntilIdle()
        assertFalse(s.intentRunning)
        s.runIntent { }                              // nothing to wait for
        advanceUntilIdle()
        assertFalse(s.intentRunning)
    }


    /** Other work that must outlive its screen (Settings' server change) runs on the same scope but is no intent run. */
    @Test fun anAppTaskIsNoIntentRun() = servicesTest {
        val s = makeServices()
        val gate = kotlinx.coroutines.CompletableDeferred<Unit>()
        var done = false
        s.runAppTask { gate.await(); done = true }
        assertFalse(s.intentRunning)
        gate.complete(Unit)
        advanceUntilIdle()
        assertTrue(done)
    }

    /** A message for the user from work with no screen (a shortcut's late failure): observed, shown, then dismissed. */
    @Test fun aNoticeIsKeptUntilDismissed() = servicesTest {
        val s = makeServices()
        var changes = 0
        s.observe { changes++ }
        assertNull(s.notice)
        s.showNotice("The server could not be reached")
        assertEquals("The server could not be reached", s.notice)
        assertEquals(1, changes)
        s.dismissNotice()
        assertNull(s.notice)
        assertEquals(2, changes)
    }

    // MARK: Sign-out

    @Test fun signOutDeletesTheSessionCookieSoItCannotBeHarvestedAgain() = servicesTest {
        val s = makeServices(user = null, server = null)
        s.setServerUrl(serverUrl)
        handler = me(3)
        web.token = "T1"
        s.harvestCookies(userId = 3)
        assertEquals("T1", s.auth.token)

        s.signOut()
        assertNull(s.auth.token)
        assertEquals(listOf(serverUrl), web.deleted)
        assertNull(web.token)
        s.harvestCookies(userId = 3)
        assertNull(s.auth.token)
    }

    /** The API's 401 hook is the services' sign-out: token cleared AND the session cookie deleted. */
    @Test fun a401FromTheApiRunsSignOut() = servicesTest {
        val s = makeServices(user = null, server = null)
        s.setServerUrl(serverUrl)
        handler = me(1)
        web.token = "T9"
        s.harvestCookies(userId = 1)
        assertEquals("T9", s.auth.token)
        s.eventSink = { sent += it }

        val api = s.api()!!
        handler = { status(401) }
        try { api.queue(); fail("no 401") } catch (e: LarkError) { assertEquals(LarkError.Unauthorized, e) }
        assertNull(s.auth.token)
        assertEquals(listOf(serverUrl), web.deleted)
        assertEquals(listOf<NativeEvent>(NativeEvent.AuthRequired), sent)
    }

    /**
     * Sign-out: the listen in progress is posted while the token still works, then playback stops, the old user's
     * queues and pending events are wiped, the token is cleared, the web is told to sign in again, and the cookie goes.
     */
    @Test fun signOutStopsPlaybackWipesTheUsersDataAndSendsAuthRequired() = servicesTest {
        val log = Collections.synchronizedList(mutableListOf<String>())
        val s = makeServices()
        handler = { r ->
            if (r.requestUrl?.encodedPath == "/api/v1/events/play") log += "post, token ${s.auth.token}"
            accepted()
        }
        s.eventSink = { e ->
            sent += e
            if (e == NativeEvent.AuthRequired) log += "authRequired, token ${s.auth.token}, queue file ${queueFile(5).exists()}"
        }
        web.onDelete = { log += "cookie deleted" }

        s.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)
        assertTrue(queueFile(5).exists())
        assertTrue(sent.any { it is NativeEvent.State })         // engine events reach the bridge

        s.signOut()
        assertEquals("stop", backend.calls.last())
        assertFalse(queueFile(5).exists())
        assertFalse(eventsFile(5).exists())
        assertNull(s.auth.token)
        assertEquals(NativeEvent.AuthRequired, sent.last())
        val post = requestsTo("/api/v1/events/play").first()
        assertEquals("Bearer T1", post.getHeader("Authorization"))
        assertEquals(listOf("post, token T1", "authRequired, token null, queue file false", "cookie deleted"), log.toList())
    }

    /** A 401 during sign-out's own flush must not deadlock or loop. */
    @Test fun a401DuringTheSignOutFlushStillSignsOutOnce() = servicesTest {
        handler = { status(401) }
        val s = makeServices()
        s.eventSink = { sent += it }
        s.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)

        s.signOut()
        assertNull(s.auth.token)
        assertEquals(1, sent.count { it == NativeEvent.AuthRequired })
        assertFalse(queueFile(5).exists())
    }

    /** Another user signing in: the old user's queue is wiped and the new user's own one is used. */
    @Test fun aUserSwitchWipesTheOldUsersQueue() = servicesTest {
        val s = makeServices()
        s.handle(q(listOf(7), 0, 0, true))
        assertTrue(queueFile(5).exists())

        s.handle(WebMessage.Auth(true, 6))
        assertFalse(queueFile(5).exists())
        assertEquals("stop", backend.calls.last())
        assertEquals(0, s.engine!!.music.items.size)
        s.handle(q(listOf(8), 0, 0, false))
        assertTrue(queueFile(6).exists())
    }

    /** The user who signs in gets their own saved queues back. */
    @Test fun aUserSwitchRestoresThatUsersQueues() = servicesTest {
        makeServices(user = 6).handle(q(listOf(8, 9), 1, 5_000, false))      // user 6, an earlier run of the app
        assertTrue(queueFile(6).exists())
        val s = makeServices(user = 5)
        s.handle(q(listOf(7), 0, 0, false))
        s.handle(WebMessage.Auth(true, 6))
        assertEquals(listOf("8", "9"), s.engine!!.music.items.map { it.id })
        assertEquals(1, s.engine!!.music.index)
        assertEquals(6L, s.queueStore.user)
    }

    @Test fun theSameUserSigningInAgainKeepsTheQueue() = servicesTest {
        val s = makeServices()
        s.handle(q(listOf(7), 0, 0, false))
        s.handle(WebMessage.Auth(true, 5))
        assertEquals(listOf("7"), s.engine!!.music.items.map { it.id })
    }

    /** A server that hangs cannot hold sign-out up: the flush gets `signOutFlushTimeoutS` seconds. */
    @Test fun theSignOutFlushIsBoundedByATimeout() = servicesTest {
        handler = { accepted().setHeadersDelay(2, TimeUnit.SECONDS) }
        val s = makeServices()
        s.signOutFlushTimeoutS = 0.3
        s.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)
        val t0 = System.nanoTime()
        s.signOut()
        assertTrue((System.nanoTime() - t0) / 1e9 < 1.5)
        assertNull(s.auth.token)
        assertFalse(queueFile(5).exists())
    }

    /** Another user signing in while the old sign-out is still flushing: the new user's data and token are left alone. */
    @Test fun aLoginDuringTheSignOutFlushIsLeftAlone() = servicesTest {
        handler = { accepted().setHeadersDelay(1, TimeUnit.SECONDS) }
        val s = makeServices()
        s.eventSink = { sent += it }
        s.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)

        val signOut = launch { s.signOut() }
        withContext(Dispatchers.Default) { delay(200) }
        advanceUntilIdle()
        s.handle(WebMessage.Auth(true, 6))
        s.handle(q(listOf(8), 0, 0, false))
        waitUntil { signOut.isCompleted }

        assertEquals(6L, s.queueStore.user)
        assertEquals("T1", s.auth.token)
        assertTrue(queueFile(6).exists())
        assertFalse(queueFile(5).exists())
        assertFalse(sent.contains(NativeEvent.AuthRequired))
    }

    /** A page load may have set the session cookie (a sign-in form that reloads): it is harvested, and the sync scheduled. */
    @Test fun aPageLoadHarvestsTheCookie() = servicesTest {
        val refresh = RecordingRefresh()
        val s = makeServices(user = null, refresh = refresh)
        handler = me(7)
        web.token = "T7"
        s.pageCookiesChanged()
        s.idle()
        assertEquals("T7", s.auth.token)
        assertEquals(7L, s.auth.userId)
        assertEquals(listOf("schedule"), refresh.calls)
        s.pageCookiesChanged()                               // the same cookie: nothing new, nothing asked
        s.idle()
        assertEquals(1, requestsTo("/api/v1/me").size)
    }

    /** Sign-in schedules the background favorites sync; sign-out cancels it. */
    @Test fun signInSchedulesTheBackgroundSyncAndSignOutCancelsIt() = servicesTest {
        val refresh = RecordingRefresh()
        val s = makeServices(user = null, refresh = refresh, cache = null)
        assertNull(s.syncRun())                              // signed out: the background work has nothing to run
        handler = me(4)
        web.token = "T4"
        s.handle(WebMessage.Auth(true, 4))
        s.idle()
        assertEquals("T4", s.auth.token)
        assertEquals(listOf("schedule"), refresh.calls)
        assertNotNull(s.syncRun())
        s.signOut(flush = false)
        assertEquals(listOf("schedule", "cancel"), refresh.calls)
        assertNull(s.syncRun())
    }

    @Test fun theWebsPrefsAreKeptForAColdStart() = servicesTest {
        val prefs = newPrefs()
        makeServices(prefs = prefs).handle(WebMessage.SetPrefs(NativePrefs("medium", carLyrics = false, loudness = false)))
        val relaunched = makeServices(prefs = prefs)
        assertEquals(NativePrefs("medium", carLyrics = false, loudness = false), relaunched.engine!!.prefs)
    }

    @Test fun theCacheCapIsOneOfTheChoices() = servicesTest {
        val prefs = newPrefs()
        val s = makeServices(prefs = prefs)
        assertEquals(2, s.cacheCapGB)
        s.setCacheCap(5)
        assertEquals(5, makeServices(prefs = prefs).cacheCapGB)
        s.setCacheCap(3)
        assertEquals(5, s.cacheCapGB)
    }

    // MARK: The bridge

    private inner class FakeView : BridgeView {
        override val url: String get() = serverUrl
        val scripts = mutableListOf<String>()
        override fun evaluate(script: String) { scripts += script }
    }

    /** Events go to the page while the Activity is started; when it starts again the page gets both queues and states. */
    @Test fun eventsReachThePageOnlyWhileTheActivityIsStarted() = servicesTest {
        val s = makeServices()
        val view = FakeView()
        s.bridge(serverUrl).attach(view)
        s.scenePhaseChanged(ScenePhase.active)
        s.handle(q(listOf(7), 0, 0, false))
        assertTrue(view.scripts.isNotEmpty())
        view.scripts.clear()
        s.scenePhaseChanged(ScenePhase.background)
        s.handle(q(listOf(8), 0, 0, false))
        assertEquals(listOf<String>(), view.scripts)
        s.scenePhaseChanged(ScenePhase.active)
        assertEquals(4, view.scripts.size)                    // emitAll: queue and state of music and episodes
        assertTrue(view.scripts.all { it.startsWith("window.dispatchEvent(") })
        assertTrue(view.scripts.none { it.contains("T1") })   // the token never goes over the bridge
    }

    /** The page's messages reach the services through the bridge of the server's origin. */
    @Test fun theBridgeRoutesToTheServices() = servicesTest {
        val s = makeServices()
        val b = s.bridge(serverUrl)
        b.onMessage("""{"type":"openSettings"}""", serverUrl.trimEnd('/'), isMainFrame = true)
        assertTrue(s.showSettings)
        assertTrue(s.bridge(serverUrl) === b)                  // one bridge per server
    }

    // MARK: Leaving a server

    /** A server change signs out (events sent with the old token), drops the engine and forgets the server's web data. */
    @Test fun changeServerSignsOutDropsTheEngineAndTheWebData() = servicesTest {
        handler = { accepted() }
        val engines = mutableListOf<Any?>()
        val s = makeServices(onEngine = { engines += it })
        val old = s.engine!!
        s.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)
        s.changeServer()
        assertEquals(1, requestsTo("/api/v1/events/play").size)
        assertNull(s.serverUrl)
        assertNull(s.auth.token)
        assertNull(s.engine)
        assertEquals(listOf(old, null), engines)              // the playback host was given the engine, then nothing
        assertEquals(listOf(serverUrl), web.removed)
        assertFalse(s.changingServer)
    }

    /**
     * The old page's bridge is cut off when a server change starts: what the page posts while the sign-out flush is
     * still sending (the old shell is still on screen) or after reaches nothing.
     */
    @Test fun theDroppedBridgeRoutesNothing() = servicesTest {
        val release = java.util.concurrent.CountDownLatch(1)
        handler = { release.await(5, TimeUnit.SECONDS); accepted() }   // the flush's POST waits for the test
        val s = makeServices()
        val b = s.bridge(serverUrl)
        s.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)
        val change = launch { s.changeServer() }
        advanceUntilIdle()
        assertTrue(s.changingServer)                           // in the flush
        b.onMessage("""{"type":"openSettings"}""", serverUrl.trimEnd('/'), isMainFrame = true)
        assertFalse(s.showSettings)
        release.countDown()
        waitUntil { change.isCompleted }
        assertNull(s.serverUrl)
        b.onMessage("""{"type":"openSettings"}""", serverUrl.trimEnd('/'), isMainFrame = true)
        assertFalse(s.showSettings)
        assertNull(s.bridge)
    }

    /** A server change whose sign-out fails leaves the server as it was: its page's bridge works again. */
    @Test fun aFailedServerChangeReopensTheBridge() = servicesTest {
        val s = makeServices()
        val view = FakeView()
        val b = s.bridge(serverUrl).also { it.attach(view) }
        s.scenePhaseChanged(ScenePhase.active)
        web.onDelete = { throw IllegalStateException("cookie store") }
        assertTrue(runCatching { s.changeServer() }.isFailure)
        assertEquals(serverUrl, s.serverUrl)
        assertFalse(s.changingServer)
        assertTrue(s.bridge(serverUrl) === b)
        b.onMessage("""{"type":"openSettings"}""", serverUrl.trimEnd('/'), isMainFrame = true)
        assertTrue("the page reaches the services again", s.showSettings)
        view.scripts.clear()
        s.handle(q(listOf(8), 0, 0, false))
        assertTrue("and gets events again", view.scripts.isNotEmpty())
    }

    /** A dropped engine is inert: the network coming back does not make it play. */
    @Test fun aDroppedEngineIgnoresTheNetwork() = servicesTest {
        val s = makeServices()
        val old = s.engine!!
        stopForTheNetwork(s)
        val loads = backend.loads.size
        s.setServerUrl("https://other.lark.test/")
        assertNull(s.auth.token)
        network.isOnline = true
        advanceUntilIdle()
        assertEquals(loads, backend.loads.size)
        assertFalse(old.playing)
    }

    // MARK: Starting playback in the background

    /**
     * The network returns while the app is in the background and the media service is not running: Android refuses to
     * start it, so the engine stays paused by the system and plays when the user comes back.
     */
    @Test fun aNetworkReturnInTheBackgroundWithNoServiceWaitsForTheUser() = servicesTest {
        var running = false
        val s = makeServices()
        s.serviceRunning = { running }
        val e = s.engine!!
        s.scenePhaseChanged(ScenePhase.background)
        stopForTheNetwork(s)
        val loads = backend.loads.size
        network.isOnline = true
        assertEquals(loads, backend.loads.size)
        assertFalse(e.playing)
        assertTrue(e.pausedBySystem)
        network.isOnline = false; network.isOnline = true      // the Wi-Fi comes and goes: still waiting
        assertEquals(loads, backend.loads.size)
        s.scenePhaseChanged(ScenePhase.active)                  // the user is back
        assertEquals(loads + 1, backend.loads.size)
        assertTrue(e.playing)
        assertFalse(e.pausedBySystem)
    }

    @Test fun aNetworkReturnInTheBackgroundWithTheServiceRunningResumesAtOnce() = servicesTest {
        val s = makeServices()
        s.serviceRunning = { true }
        s.scenePhaseChanged(ScenePhase.background)
        stopForTheNetwork(s)
        val loads = backend.loads.size
        network.isOnline = true
        assertEquals(loads + 1, backend.loads.size)
        assertTrue(s.engine!!.playing)
    }

    @Test fun aNetworkReturnInTheForegroundResumesAtOnce() = servicesTest {
        val s = makeServices()
        s.serviceRunning = { false }
        s.scenePhaseChanged(ScenePhase.active)
        stopForTheNetwork(s)
        val loads = backend.loads.size
        network.isOnline = true
        assertEquals(loads + 1, backend.loads.size)
    }

    /** The user pausing meanwhile (the notification, a headset) wins: coming back plays nothing. */
    @Test fun aWaitingResumeIsDroppedByAPause() = servicesTest {
        val s = makeServices()
        s.serviceRunning = { false }
        s.scenePhaseChanged(ScenePhase.background)
        stopForTheNetwork(s)
        network.isOnline = true
        s.engine!!.pause()
        val loads = backend.loads.size
        s.scenePhaseChanged(ScenePhase.active)
        assertEquals(loads, backend.loads.size)
        assertFalse(s.engine!!.playing)
    }

    /** A stream that breaks offline with nothing on the phone: the engine stops and waits for the network. */
    private fun stopForTheNetwork(s: AppServices) {
        s.handle(q(listOf(7), 0, 0, true))
        backend.start()
        network.isOnline = false
        backend.fail(network = true)
        assertFalse(s.engine!!.playing)
    }

    @Test fun theBackgroundStartDecision() {
        assertTrue(AppServices.mayStartPlayback(foreground = true, serviceRunning = false))
        assertTrue(AppServices.mayStartPlayback(foreground = false, serviceRunning = true))
        assertTrue(AppServices.mayStartPlayback(foreground = true, serviceRunning = true))
        assertFalse(AppServices.mayStartPlayback(foreground = false, serviceRunning = false))
    }

    // MARK: The page could not load (iOS PageLoadFailureTests)

    private fun pageServices(network: FakeNetwork = this.network, cache: FakeCache = FakeCache(), prefs: android.content.SharedPreferences = newPrefs()) =
        makeServices(user = 3, prefs = prefs, cache = cache, network = network, server = "https://page.lark.test/",
            dataDirectory = File(dir, "page-${System.nanoTime()}"))

    @Test fun aFailedPageShowsTheOverlayAndRetriesWhenTheNetworkReturns() = servicesTest {
        val s = pageServices()
        val loads = mutableListOf<String>()
        s.loadPage = { loads += it }
        network.isOnline = false
        s.pageDidFail(android.webkit.WebViewClient.ERROR_HOST_LOOKUP, "net::ERR_INTERNET_DISCONNECTED")
        assertTrue(s.pageFailed)
        assertEquals(listOf<String>(), loads)                                // still offline: nothing to retry
        network.isOnline = true
        assertFalse(s.pageFailed)
        assertEquals(listOf("https://page.lark.test/"), loads)
        network.isOnline = false; network.isOnline = true                   // loaded fine meanwhile: no more retries
        assertEquals(1, loads.size)
    }

    @Test fun aCancelledLoadIsNotAFailure() = servicesTest {
        val s = pageServices()
        s.pageDidFail(android.webkit.WebViewClient.ERROR_UNKNOWN, "net::ERR_ABORTED")
        assertFalse(s.pageFailed)
    }

    @Test fun retryAndLoadedClearTheOverlay() = servicesTest {
        val s = pageServices()
        var loads = 0
        s.loadPage = { loads++ }
        s.pageDidFail(android.webkit.WebViewClient.ERROR_CONNECT, "net::ERR_CONNECTION_REFUSED")
        s.retryPage()
        assertEquals(1, loads)
        assertFalse(s.pageFailed)
        s.pageDidFail(android.webkit.WebViewClient.ERROR_CONNECT, "net::ERR_CONNECTION_REFUSED")
        s.pageDidLoad()
        assertFalse(s.pageFailed)
    }

    /** A 5xx for the page itself is a failed page too (the shell checks the status with `PageLoad`). */
    @Test fun aServerErrorPageIsAFailure() = servicesTest {
        val s = pageServices()
        s.pageDidFail(status = 502)
        assertTrue(s.pageFailed)
        s.pageDidLoad()
        s.pageDidFail(status = 404)
        assertFalse(s.pageFailed)
    }

    @Test fun offlineFavoritesFromTheOverlay() = servicesTest {
        val cache = FakeCache()
        val network = FakeNetwork().also { it.isOnline = false }
        val s = pageServices(network, cache)
        assertFalse(s.hasOfflineFavorites)
        cache.favorites = listOf(trackModel(1)); cache.local[1] = File("/tmp/1.m4a")
        assertTrue(s.hasOfflineFavorites)
        s.playOfflineFavorites()
        assertTrue(s.engine!!.playing)
        assertEquals("1", s.engine!!.current?.id)
        assertNotNull(s.offlinePlayer)
        assertEquals(MediaSource.File(File("/tmp/1.m4a")), backend.loads.last().src)
    }

    @Test fun theSettingsButtonOpensTheNativeSheet() = servicesTest {
        val s = pageServices()
        s.pageDidFail(android.webkit.WebViewClient.ERROR_HOST_LOOKUP, "net::ERR_NAME_NOT_RESOLVED")
        s.openSettings()
        assertTrue(s.showSettings)
    }

    // MARK: Stay Offline (iOS StayOfflineTests, the page)

    @Test fun thePageIsNotLoadedUntilStayOfflineIsTurnedOff() = servicesTest {
        val s = pageServices()
        val loads = mutableListOf<String>()
        val shown = mutableListOf<String>()
        s.loadPage = { loads += it }
        s.page = object : PageControl {
            override fun load(url: String) { shown += url }
        }
        s.setStayOffline(true)
        assertTrue(s.pageFailed)
        assertNotNull(s.offlinePlayer)
        assertEquals(listOf("about:blank"), shown)             // the page's own requests stop too
        s.retryPage()
        network.isOnline = false; network.isOnline = true      // the plane's Wi-Fi comes and goes
        assertEquals(listOf<String>(), loads)
        s.pageDidLoad()                                        // the empty page loaded: still the offline player
        assertTrue(s.pageFailed)
        s.setStayOffline(false)
        assertEquals(listOf("https://page.lark.test/"), loads)
    }

    @Test fun stayOfflineIsKeptAcrossLaunchesUntilTurnedOff() = servicesTest {
        val prefs = newPrefs()
        pageServices(prefs = prefs).setStayOffline(true)
        val relaunched = pageServices(prefs = prefs)
        assertTrue(relaunched.stayOffline)
        assertTrue(relaunched.pageFailed)
        relaunched.start()
        assertNotNull(relaunched.offlinePlayer)                // straight to the offline player
        relaunched.setStayOffline(false)
        assertFalse(pageServices(prefs = prefs).stayOffline)
    }

    @Test fun observersHearChanges() = servicesTest {
        val s = pageServices()
        var n = 0
        s.observe { n++ }
        s.openSettings()
        s.pageDidFail(android.webkit.WebViewClient.ERROR_CONNECT, "net::ERR_CONNECTION_REFUSED")
        assertTrue(n >= 2)
    }

    private class RecordingRefresh : RefreshScheduler {
        val calls = mutableListOf<String>()
        override fun schedule() { calls += "schedule" }
        override fun cancel() { calls += "cancel" }
    }
}
