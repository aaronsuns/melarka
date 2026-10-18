package lark.cache

import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import lark.api.ApiTrack
import lark.api.LarkError
import lark.api.Track
import lark.player.toItem
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

/** Port of FavoritesSyncTests. */
class FavoritesSyncTest : CacheTestCase() {
    private lateinit var sync: FavoritesSync

    override fun beforeCache() {}

    private fun syncTest(body: suspend kotlinx.coroutines.test.TestScope.() -> Unit) = cacheTest {
        sync = FavoritesSync(api, cache, network)
        body()
    }

    private fun models(ids: IntRange, durationMs: Long = 200_000) = ids.map { trackModel(it, durationMs) }

    @Test fun syncFlagsAndDownloadsMissingOnWiFi() = syncTest {
        api.favoritesAnswer = models(1..3)
        put(1, 100, false, t0)                                              // api favorites [1,2,3], cache has 1 -> downloads 2,3
        sync.run()
        assertEquals(listOf(2L, 3L), api.downloadCalls.sorted())
        assertEquals(listOf("high", "high"), api.downloadQualities)
        assertEquals(listOf(1L, 2L, 3L), favoriteIds())                     // flags {1,2,3}
        assertFalse(cache.isDownloading)                                    // run() returns once they are stored
        sync.run()                                                          // nothing missing: no download
        assertEquals(2, api.downloadCalls.size)
    }

    @Test fun unfavoritedKeepsFileButLosesFlag() = syncTest {
        api.favoritesAnswer = models(1..2)
        sync.run()
        assertEquals(listOf(1L, 2L), favoriteIds())
        api.favoritesAnswer = models(2..2)
        sync.run()
        assertEquals(listOf(2L), favoriteIds())
        assertNotNull(cache.localFile(1))                                   // an ordinary LRU entry now
        assertEquals(listOf(1L, 2L), api.downloadCalls.sorted())
        // ... and so it goes before any favorite.
        clock.current = at(100)
        cache.touch(1)
        cache.capBytes = 100
        assertNull(cache.localFile(1)); assertNotNull(cache.localFile(2))
    }

    @Test fun aReAddedFavoriteIsNotDownloadedAgain() = syncTest {
        api.favoritesAnswer = models(1..2)
        sync.run()
        api.favoritesAnswer = models(2..2)                                  // 1 unfavorited: the file stays
        sync.run()
        api.favoritesAnswer = models(1..2)                                  // and favorited again
        sync.run()
        assertEquals(listOf(1L, 2L), api.downloadCalls.sorted())
        assertEquals(listOf(1L, 2L), favoriteIds())
    }

    @Test fun syncSkippedOffOrOnExpensiveNetwork() = syncTest {
        api.favoritesAnswer = models(1..1)
        network.isExpensive = true
        sync.run()
        network.isExpensive = false; network.isOnline = false
        sync.run()
        assertEquals(0, api.favoritesCalls)
        assertEquals(emptyList<Long>(), api.downloadCalls)
        api.token = null; network.isOnline = true
        sync.run()
        assertEquals(0, api.favoritesCalls)
    }

    @Test fun syncWaitsForTheFirstNetworkAnswer() = syncTest {
        api.favoritesAnswer = models(1..1)
        network.answered = false                                            // unknown is not Wi-Fi
        sync.run()
        assertEquals(0, api.favoritesCalls)
    }

    @Test fun syncStopsDownloadingWhenTheNetworkTurnsExpensive() = syncTest {
        api.favoritesAnswer = models(1..4)
        api.holdDownloads = true
        val run = launch { sync.run() }
        settle()
        assertEquals(2, api.heldCount)
        network.isExpensive = true                                          // onto cellular mid-sync
        api.releaseDownloads()
        run.join()
        assertEquals(listOf(1L, 2L), api.downloadCalls.sorted())            // the two already running finish; no more start
        assertEquals(listOf(1L, 2L), favoriteIds())
    }

    @Test fun cachedFavoritesReturnsMetaForQueueItems() = syncTest {
        // cachedFavorites() feeds NextChooser and the car: the API's raw JSON, as the web would get it.
        val raw = buildJsonObject {
            put("id", 4); put("title", "T4"); put("artist", "A"); put("album", "B"); put("duration_ms", 1000); put("favorite", true); put("codec", "flac")
        }
        val t = Track(4, "T4", "A", "B", 1000, true)
        api.favoritesAnswer = listOf(ApiTrack(t, raw))
        sync.run()
        network.isOnline = false
        val favs = cache.cachedFavorites()
        assertEquals(1, favs.size)
        assertEquals(t, favs[0].track); assertEquals(raw, favs[0].json)
        val item = favs[0].toItem()
        assertEquals("4", item.id); assertEquals(raw, item.meta)
        assertEquals("4.m4a", cache.localFile(4)?.name)
    }

    @Test fun syncRefreshesTheMetaOfCachedFavorites() = syncTest {
        put(1, 100, true, t0)
        val newer = Track(1, "Renamed", "A", "B", 1000, true)
        api.favoritesAnswer = listOf(ApiTrack(newer, buildJsonObject { put("id", 1); put("title", "Renamed") }))
        sync.run()
        assertEquals("Renamed", cache.cachedFavorites().first().track.title)
        assertEquals(JsonPrimitive("Renamed"), (cache.cachedFavorites().first().json as JsonObject)["title"])
        assertEquals(emptyList<Long>(), api.downloadCalls)
    }

    @Test fun failedListChangesNothing() = syncTest {
        put(1, 100, true, t0)
        api.favoritesError = LarkError.Http(502, null)
        sync.run()
        assertEquals(listOf(1L), favoriteIds())                             // not taken as "no favorites"
    }

    /** With more favorites than the cap holds, a sync stops instead of evicting favorites it just fetched. */
    @Test fun syncStopsWhenFavoritesFillTheCap() = syncTest {
        // 200 s tracks are estimated at 6.4 MB (AAC 256k) before they are fetched; these are that size.
        cache.capBytes = 15_000_000
        api.favoritesAnswer = models(1..6)
        for (id in 1L..6L) api.downloadBytes[id] = 6_400_000
        sync.run()
        assertEquals(2, api.downloadCalls.size)                             // a third would not fit beside them
        assertEquals(2, cache.cachedFavorites().size)
        assertTrue(cache.usedBytes <= 15_000_000)
        sync.run()                                                          // and the next sync does not churn
        assertEquals(2, api.downloadCalls.size)
    }

    @Test fun cancelledSyncStartsNoMoreDownloads() = syncTest {
        api.favoritesAnswer = models(1..5)
        api.holdDownloads = true
        val run = launch { sync.run() }
        settle()
        assertEquals(2, api.heldCount)
        run.cancelAndJoin()
        api.releaseDownloads()
        settle()
        assertEquals(2, api.downloadCalls.size)
    }

    // MARK: Free space, cancelling and Data Saver

    @Test fun syncKeepsTheFreeSpaceReserve() = syncTest {
        // 510 MB free before anything is cached: one 6.4 MB favorite fits above the 500 MB reserve, not two.
        free = null
        cache = CacheStore(root, api, network, clock, cacheScope, scheduler, { (510L shl 20) - cache.usedBytes })
        sync = FavoritesSync(api, cache, network)
        api.favoritesAnswer = models(1..4)
        for (id in 1L..4L) api.downloadBytes[id] = 6_400_000
        sync.run()
        assertEquals(listOf(1L), api.downloadCalls)
    }

    @Test fun syncStopsOnTheFirstOutOfSpace() = syncTest {
        api.favoritesAnswer = models(1..6, 1000)
        api.downloadErrors[1] = IOException("write failed: ENOSPC (No space left on device)")
        sync.run()
        assertTrue(api.downloadCalls.size <= 2)                             // 2 may already hold the other slot
        assertFalse(api.downloadCalls.contains(3L))
        // The next sync tries again (space may have been freed), and stops again if it has not.
        api.downloadErrors.clear()
        sync.run()
        assertEquals(6, cache.cachedFavorites().size)
    }

    /** Favorites fill to about 90% of the cap: the lookahead and estimate errors fit without evicting a favorite. */
    @Test fun syncLeavesHeadroomUnderTheCap() = syncTest {
        cache.capBytes = 64_000_000                                         // ten 6.4 MB favorites would fill it exactly
        api.favoritesAnswer = models(1..10)
        for (id in 1L..10L) api.downloadBytes[id] = 6_400_000
        sync.run()
        assertEquals(9, api.downloadCalls.size)
        assertTrue(cache.usedBytes <= 57_600_000)
    }

    /** Cancelling the sync (background work stopping) leaves a lookahead download of the same track alone. */
    @Test fun cancellingTheSyncSparesTheLookahead() = syncTest {
        api.holdDownloads = true
        cache.prefetch(listOf(lark.testing.track(3), lark.testing.track(4)))   // both slots held
        settle()
        assertEquals(2, api.heldCount)
        cache.prefetch(listOf(lark.testing.track(1)))                       // the lookahead's 1 waits for a slot
        api.favoritesAnswer = models(1..2)
        val run: Job = launch { sync.run() }
        settle()
        assertEquals(1, api.favoritesCalls)
        run.cancelAndJoin()
        api.releaseDownloads()
        settle(downloads = 3)
        assertEquals(listOf(1L, 3L, 4L), api.downloadCalls.sorted())        // 1 (the lookahead's) still came; 2 (the sync's) did not
    }

    @Test fun syncKeepsCoversAndLyricsOfCachedFavorites() = syncTest {
        put(1, 100, true, t0)                                               // cached before covers and lyrics were kept
        api.favoritesAnswer = models(1..1)
        api.artworkAnswer = ByteArray(4) { 2 }
        api.lyricsAnswers[1] = lark.testing.lyricsDoc()
        sync.run()
        assertNotNull(cache.artworkFile(1))
        assertNotNull(cache.cachedLyrics(1))
        assertEquals(emptyList<Long>(), api.downloadCalls)
    }
}
