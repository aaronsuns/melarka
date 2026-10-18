package lark.app

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import lark.bridge.WebMessage
import lark.cache.CacheLayout
import lark.player.MediaSource
import lark.testing.q
import okhttp3.mockwebserver.MockResponse
import okio.Buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * iOS `CacheWiringTests`: the per-user cache, the sync triggers, sign-out, the per-server folders. (The server key is
 * `CacheLayoutTest`'s; the background work's own tests are `FavoritesRefreshWorkerTest`.)
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CacheWiringTest : ServicesTestCase() {
    private val caches: File get() = File(dir, "caches")
    /** This server's folder: `caches/<server key>`. */
    private val serverDir: File get() = File(caches, CacheLayout.serverKey(serverUrl))

    private fun services(user: Long = 5) = makeServices(user = user, cache = null)

    /** The server: these favorites, and a 50-byte AAC file for any stream (after [streamDelayMs]). */
    private fun serve(favorites: List<Int>, streamDelayMs: Long = 0) {
        val items = favorites.joinToString(",") { """{"id":$it,"title":"T$it","artist":"A","album":"B","duration_ms":1000,"favorite":true}""" }
        val page = """{"items":[$items],"next_cursor":""}"""
        handler = { r ->
            val path = r.requestUrl?.encodedPath ?: ""
            when {
                path == "/api/v1/me" -> ok("""{"id":5}""")
                path == "/api/v1/tracks" -> ok(page)
                path.endsWith("/stream") -> MockResponse().setHeader("Content-Type", "audio/mp4").setBody(Buffer().write(ByteArray(50) { 1 }))
                    .setHeadersDelay(streamDelayMs, TimeUnit.MILLISECONDS)
                else -> status(404)
            }
        }
    }

    private val listCalls: Int get() = requestsTo("/api/v1/tracks").size

    private suspend fun TestScope.cached(s: AppServices, id: Long) = waitUntil(state = { "track $id not cached" }) { s.cache.localFile(id) != null }

    @Test fun favoriteChangedSyncsTheCacheOfThatUser() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.handle(WebMessage.FavoriteChanged(2, true))
        cached(s, 2)
        assertEquals(File(serverDir, "5/files/2.m4a").path, s.cache.localFile(2)!!.path)   // lark/<server key>/<userId>
        assertEquals(listOf(2L), s.cache.cachedFavorites().map { it.track.id })
        val list = requestsTo("/api/v1/tracks").first()
        assertEquals("1", list.requestUrl?.queryParameter("favorite"))
    }

    @Test fun favoriteChangedOnCellularWaitsForWiFi() = servicesTest {
        serve(listOf(2))
        network.isExpensive = true
        val s = services()
        s.handle(WebMessage.FavoriteChanged(2, true))
        settle()
        assertEquals(0, requests.size)
        network.isExpensive = false                                   // onto Wi-Fi: the skipped sync runs
        cached(s, 2)
    }

    @Test fun unfavoriteIsFlaggedAtOnceEvenOffline() = servicesTest {
        serve(listOf(2, 3))
        val s = services()
        s.syncFavoritesNow()
        assertEquals(listOf(2L, 3L), s.cache.cachedFavorites().map { it.track.id }.sorted())
        network.isOnline = false
        s.handle(WebMessage.FavoriteChanged(2, false))
        assertEquals(listOf(3L), s.cache.cachedFavorites().map { it.track.id })    // an offline shuffle no longer picks it
        assertNotNull(s.cache.localFile(2))
    }

    @Test fun signOutSetsTheCacheAsideUntilThatUserSignsInAgain() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.syncFavoritesNow()
        val file = s.cache.localFile(2)!!

        s.signOut(flush = false)
        assertNull(s.cache.localFile(2))                              // not used while signed out ...
        assertTrue(s.cache.cachedFavorites().isEmpty())
        assertTrue(file.exists())                                     // ... but kept

        s.handle(WebMessage.Auth(true, 6))                            // another user: their own (empty) cache
        assertNull(s.cache.localFile(2))
        s.handle(WebMessage.Auth(true, 5))                            // the same user again: it is back
        assertEquals(file, s.cache.localFile(2))
        s.idle()
    }

    @Test fun signInSyncs() = servicesTest {
        serve(listOf(4))
        val s = services()
        s.handle(WebMessage.Auth(true, 5))
        cached(s, 4)
    }

    @Test fun launchSyncsAndActivationSyncsAtMostOnceAnHour() = servicesTest {
        serve(listOf())
        val s = services()
        s.start()                                                     // launch
        waitUntil { listCalls == 1 }
        s.scenePhaseChanged(ScenePhase.active)                        // the launch's own activation
        clock.advance(1800.0)
        s.scenePhaseChanged(ScenePhase.background); s.scenePhaseChanged(ScenePhase.active)
        settle()
        assertEquals(1, listCalls)
        clock.advance(1801.0)
        s.scenePhaseChanged(ScenePhase.active)
        waitUntil { listCalls == 2 }
    }

    @Test fun triggersDuringASyncRunOneMoreAfterIt() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.handle(WebMessage.FavoriteChanged(2, true))
        s.handle(WebMessage.FavoriteChanged(3, true))
        s.handle(WebMessage.FavoriteChanged(4, true))
        waitUntil { listCalls == 2 && !s.isSyncing }                  // one running, one queued: not three
        settle()
        assertEquals(2, listCalls)
    }

    @Test fun theEngineReadsTheUsersCache() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.syncFavoritesNow()
        network.isOnline = false
        s.handle(q(listOf(2), 0, 0, true))
        assertEquals("2", s.engine?.current?.id)
        assertEquals(MediaSource.File(File(serverDir, "5/files/2.m4a")), backend.loads.last().src)
    }

    // MARK: Per server

    /**
     * The same user id on another server is another library: nothing of the first server's cache is used, and its
     * folder is deleted. (Leaving a server also signs out of it, so the new server's cache starts once the user signs
     * in there.)
     */
    @Test fun anotherServerHasItsOwnCacheAndTheOldOneIsDeleted() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.syncFavoritesNow()
        assertNotNull(s.cache.localFile(2))
        val other = "http://other.local:${server.port}/"
        s.setServerUrl(other)
        assertNull(s.auth.token)                                      // the old server's token never goes to the new one
        assertNull(s.cache.localFile(2))
        assertTrue(s.cache.cachedFavorites().isEmpty())
        assertFalse(serverDir.exists())
        assertEquals(listOf(serverUrl), web.removed)                  // and its web data is gone
    }

    @Test fun theNewServersCacheIsItsOwnFolder() = servicesTest {
        serve(listOf(2))
        val prefs = newPrefs()
        val first = makeServices(user = null, cache = null, prefs = prefs, server = "https://first.lark.test/")
        File(caches, CacheLayout.serverKey("https://first.lark.test/") + "/5/files").mkdirs()
        first.setServerUrl(serverUrl)                                 // a server change, as from the server screen
        assertFalse(File(caches, CacheLayout.serverKey("https://first.lark.test/")).exists())
        web.token = "T2"
        first.handle(WebMessage.Auth(true, 5))
        cached(first, 2)
        assertEquals(File(serverDir, "5/files/2.m4a").path, first.cache.localFile(2)!!.path)
    }

    @Test fun legacyPerUserFoldersAreRemovedAtLaunch() = servicesTest {
        val legacy = File(caches, "5/files")
        legacy.mkdirs()
        File(legacy, "2.m4a").writeBytes(byteArrayOf(1))
        val s = services()
        assertFalse(File(caches, "5").exists())
        assertNull(s.cache.localFile(2))
    }

    // MARK: The first network answer

    /**
     * A cold background launch: the monitor has not answered yet. The sync waits for it (bounded), then runs inside
     * the call, so the background work covers it.
     */
    @Test fun backgroundSyncWaitsForTheFirstNetworkAnswer() = servicesTest {
        serve(listOf(2))
        network.answered = false; network.isExpensive = true          // what AndroidNetworkStatus reports before its answer
        val s = services()
        val run = launch { s.syncFavoritesNow() }
        settle(200)
        assertEquals(0, requests.size)
        network.answer(online = true, expensive = false)
        waitUntil { run.isCompleted }
        assertNotNull(s.cache.localFile(2))                           // done before the call returned
    }

    @Test fun theWaitForTheFirstAnswerIsBounded() = servicesTest {
        serve(listOf(2))
        network.answered = false
        val s = services()
        s.networkAnswerTimeoutS = 0.3
        s.syncFavoritesNow()
        assertEquals(0, requests.size)                                // unknown is not Wi-Fi
        network.answer(online = true, expensive = false)              // the skipped sync runs on the answer
        cached(s, 2)
    }

    // MARK: Cancelled syncs

    @Test fun aCancelledSyncDoesNotLeaveARerunBehind() = servicesTest {
        serve(listOf(2), streamDelayMs = 400)
        val s = services()
        val run = launch { s.syncFavoritesNow() }
        waitUntil { listCalls == 1 }
        s.syncFavorites()                                             // a trigger during the run: one more is due
        run.cancel()                                                  // the background work is stopped
        waitUntil { run.isCompleted }
        settle(600)
        assertEquals(1, listCalls)
        s.syncFavorites()                                             // a later trigger runs once, not twice
        waitUntil { listCalls == 2 }
        settle(900)
        assertEquals(2, listCalls)
        waitUntil { !s.isSyncing }
    }

    // MARK: Flushes

    /** Backgrounding writes the index at once (Android has no reliable termination callback: `onStop` is the last word). */
    @Test fun backgroundingFlushesTheIndex() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.syncFavoritesNow()
        val index = File(serverDir, "5/index.json")
        assertFalse(index.exists())                                   // coalesced: not written yet
        s.scenePhaseChanged(ScenePhase.background)
        assertTrue(index.exists())
    }

    @Test fun networkHasBothTheEngineAndTheCacheAsObservers() = servicesTest {
        val s = services()
        s.engine
        assertEquals(1, network.observers.size)    // the Stay Offline switch, which passes every change on to both
    }

    /** The background work's seam: the signed-in user's sync, which also writes the index when it ends. */
    @Test fun theBackgroundWorkRunsTheSyncAndFlushes() = servicesTest {
        serve(listOf(2))
        val s = services()
        s.syncRun()!!.run()
        assertNotNull(s.cache.localFile(2))
        assertTrue(File(serverDir, "5/index.json").exists())
    }
}
