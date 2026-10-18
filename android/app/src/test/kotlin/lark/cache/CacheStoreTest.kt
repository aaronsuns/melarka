package lark.cache

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.double
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import kotlinx.serialization.json.put
import lark.api.LarkApi
import lark.api.LarkError
import lark.api.Track
import lark.auth.AuthStore
import lark.bridge.Item
import lark.testing.FakeCookies
import lark.testing.MemorySecretStore
import lark.testing.episode
import lark.testing.track
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okio.Buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.io.IOException

/** Port of CacheStoreTests (`CacheStoreTests`). */
class CacheStoreTest : CacheTestCase() {
    @Test fun evictsOldestNonFavoriteFirstThenOldestFavorite() = cacheTest {
        cache.capBytes = 300
        put(1, 100, true, t0); put(2, 100, false, at(1)); put(3, 100, false, at(2))
        put(4, 100, false, at(3))        // over cap -> 2 goes (oldest non-favorite), 1 stays
        assertNull(cache.localFile(2)); assertNotNull(cache.localFile(1))
        cache.capBytes = 100              // shrink -> 3, 4, then favorite 1 (only favorites left over cap)
        assertEquals(100, cache.usedBytes)
        assertNotNull(cache.localFile(1))
        assertNull(cache.localFile(3)); assertNull(cache.localFile(4))
        cache.capBytes = 50               // only a favorite is left: it goes too
        assertEquals(0, cache.usedBytes)
        assertNull(cache.localFile(1))
    }

    @Test fun oldestFavoriteGoesWhenOnlyFavoritesAreLeft() = cacheTest {
        cache.capBytes = 200
        put(1, 100, true, t0); put(2, 100, true, at(1))
        put(3, 100, true, at(2))
        assertNull(cache.localFile(1))
        assertNotNull(cache.localFile(2)); assertNotNull(cache.localFile(3))
        assertEquals(200, cache.usedBytes)
    }

    @Test fun evictedFilesAreDeletedFromDisk() = cacheTest {
        cache.capBytes = 100
        put(1, 100, false, t0)
        val first = cache.localFile(1)!!
        put(2, 100, false, at(1))
        assertFalse(first.exists())
    }

    @Test fun touchRefreshesRecency() = cacheTest {
        cache.capBytes = 200
        put(1, 100, false, t0); put(2, 100, false, at(1))
        clock.current = at(5)
        cache.touch(1)
        put(3, 100, false, at(6))         // 2 is now the least recently played
        assertNotNull(cache.localFile(1)); assertNull(cache.localFile(2))
        // The new recency is on disk too.
        assertEquals(at(5).epochSecond.toDouble(), indexJson()["1"]!!.jsonObject["lastPlayed"]!!.jsonPrimitive.double, 0.0)
    }

    @Test fun indexSurvivesRestartAndDropsMissingFiles() = cacheTest {
        put(1, 100, true, t0); put(2, 150, false, at(1))
        val index = indexJson()
        assertEquals(setOf("1", "2"), index.keys)
        val e1 = index["1"]!!.jsonObject
        assertEquals(setOf("file", "bytes", "lastPlayed", "favorite", "track", "meta"), e1.keys)
        assertEquals("files/1.m4a", e1["file"]!!.jsonPrimitive.content)   // relative to the user's folder
        assertEquals(100, e1["bytes"]!!.jsonPrimitive.long); assertTrue(e1["favorite"]!!.jsonPrimitive.boolean)

        cache = makeCache()                                                 // a relaunch
        assertEquals(250, cache.usedBytes)
        assertNotNull(cache.localFile(1)); assertNotNull(cache.localFile(2))
        assertEquals(listOf(1L), favoriteIds())

        cache.localFile(2)!!.delete()
        cache = makeCache()
        assertNull(cache.localFile(2))
        assertEquals(100, cache.usedBytes)
        assertEquals(setOf("1"), indexJson().keys)                          // the dropped entry is gone from disk too
    }

    @Test fun fileDeletedWhileRunningIsDropped() = cacheTest {
        put(1, 100, true, t0); put(2, 100, true, at(1))
        cache.localFile(1)!!.delete()
        assertNull(cache.localFile(1))                                      // not an instant playback failure
        assertEquals(100, cache.usedBytes)
        assertEquals(listOf(2L), favoriteIds())
        assertEquals(setOf("2"), indexJson().keys)
    }

    /** The system may empty the cache's folder at any time, the index included (a user clearing storage). */
    @Test fun purgedDirectoryEmptiesTheCacheAndItFillsAgain() = cacheTest {
        put(1, 100, true, t0)
        root.deleteRecursively()
        assertNull(cache.localFile(1))
        assertTrue(cache.cachedFavorites().isEmpty())
        assertEquals(0, cache.usedBytes)
        put(2, 100, true, at(1))                                            // the directories are made again
        assertNotNull(cache.localFile(2))
        cache = makeCache()
        assertEquals(listOf(2L), favoriteIds())
    }

    @Test fun relaunchWithoutAnIndexRemovesOrphanFilesAndStaging() = cacheTest {
        put(1, 100, true, t0)
        val file = cache.localFile(1)!!
        cache.flush()
        val staged = File(root, "staging/9.m4a")
        staged.parentFile!!.mkdirs()
        staged.writeBytes(byteArrayOf(1))
        File(root, "index.json").delete()
        cache = makeCache()
        assertEquals(0, cache.usedBytes)
        assertFalse(file.exists())                                          // nothing unaccounted for stays on disk
        assertFalse(staged.exists())
    }

    @Test fun corruptIndexIsAnEmptyCache() = cacheTest {
        root.mkdirs()
        File(root, "index.json").writeText("{not json")
        cache = makeCache()
        assertEquals(0, cache.usedBytes)
        put(1, 100, false, t0)
        assertNotNull(cache.localFile(1))
        assertEquals(setOf("1"), indexJson().keys)                          // rebuilt
    }

    /** An index entry pointing outside `files/` is dropped, and nothing outside the user's folder is deleted. */
    @Test fun neverDeletesOutsideTheUserFolder() = cacheTest {
        val outside = File(folder.root, "precious.txt").also { it.writeText("x") }
        root.mkdirs()
        File(root, "index.json").writeText("""{"1":{"file":"files/../../../../precious.txt","bytes":1,"lastPlayed":1.0,"favorite":false,""" +
            """"track":{"id":1,"title":"t","artist":"a","album":"b","duration_ms":1,"favorite":false},"meta":null}}""")
        cache = makeCache()
        assertNull(cache.localFile(1))
        cache.clear()
        cache.wipe()
        assertTrue(outside.exists())
        assertFalse(root.exists())
    }

    @Test fun everythingTheCacheWritesIsInsideItsFolder() = cacheTest {
        put(1, 100, true, t0)
        cache.flush()
        val base = folder.root.toPath()
        assertTrue(cache.localFile(1)!!.toPath().startsWith(root.toPath()))
        // Besides the test's own scratch files and the user's folder, nothing else appeared.
        assertEquals(setOf("base", "scratch"), folder.root.list()!!.toSet())
        assertTrue(File(root, "index.json").toPath().startsWith(base))
    }

    @Test fun storingAgainReplacesTheFile() = cacheTest {
        put(1, 100, false, t0)
        put(1, 40, false, at(1))
        assertEquals(40, cache.usedBytes)
        assertEquals(40, cache.localFile(1)!!.length())
    }

    @Test fun clearRemovesEverything() = cacheTest {
        put(1, 100, true, t0); put(2, 100, false, at(1))
        val file = cache.localFile(1)!!
        cache.clear()
        assertEquals(0, cache.usedBytes)
        assertNull(cache.localFile(1)); assertTrue(cache.cachedFavorites().isEmpty())
        assertFalse(file.exists())
        cache = makeCache()
        assertEquals(0, cache.usedBytes)
    }

    @Test fun wipeDeletesTheUsersFolder() = cacheTest {
        put(1, 100, true, t0)
        cache.flush()
        cache.wipe()
        assertFalse(root.exists())
        assertNull(cache.localFile(1))
    }

    @Test fun setFavoritesFlagsOnlyAndPatchesTheMeta() = cacheTest {
        put(1, 100, true, t0); put(2, 100, false, at(1))
        cache.setFavorites(setOf(2, 3))
        assertEquals(listOf(2L), favoriteIds())
        val f = cache.cachedFavorites().first()
        assertTrue(f.track.favorite); assertEquals(JsonPrimitive(true), (f.json as JsonObject)["favorite"])
        assertNotNull(cache.localFile(1))                                   // the file stays, as an ordinary LRU entry
        assertEquals(emptyList<Long>(), api.downloadCalls)                  // flags only: nothing is fetched
        cache = makeCache()
        assertEquals(listOf(2L), favoriteIds())
    }

    @Test fun cachedFavoritesReturnTheStoredMeta() = cacheTest {
        val f = File(scratch, "8.m4a").also { it.writeBytes(ByteArray(10) { 1 }) }
        val t = Track(8, "Song", "Art", "Alb", 1234, true)
        val meta = buildJsonObject { put("id", 8); put("title", "Song"); put("codec", "flac"); put("favorite", true) }
        cache.store(8, f, t, meta, true)
        cache = makeCache()
        val favs = cache.cachedFavorites()
        assertEquals(1, favs.size)
        assertEquals(t, favs[0].track); assertEquals(meta, favs[0].json)    // unknown fields survive for the web
    }

    // MARK: Prefetch (the lookahead)

    @Test fun prefetchDownloadsOnlyMissingAndNotTwiceConcurrently() = cacheTest {
        put(7, 100, false, t0)
        api.holdDownloads = true
        cache.prefetch(listOf(track(5), track(5), track(6), track(7)))      // 7 is cached; 5 is asked twice
        cache.prefetch(listOf(track(5)))                                    // still downloading: not again
        settle()
        assertEquals(2, api.heldCount)
        api.releaseDownloads()
        settle(downloads = 2)
        assertEquals(listOf(5L, 6L), api.downloadCalls.sorted())
        assertEquals(listOf("high", "high"), api.downloadQualities)         // the web's OFFLINE_QUALITY
        assertNotNull(cache.localFile(5)); assertNotNull(cache.localFile(6))
        cache.prefetch(listOf(track(5), track(6)))                          // cached now: nothing to do
        assertFalse(cache.isDownloading)
        assertEquals(2, api.downloadCalls.size)
    }

    @Test fun atMostTwoDownloadsAtOnce() = cacheTest {
        api.holdDownloads = true
        cache.prefetch(listOf(track(1), track(2), track(3), track(4)))
        settle()
        assertEquals(2, api.heldCount)
        assertEquals(2, api.downloadCalls.size)                             // the other two wait for a slot
        api.releaseDownloads()
        settle(downloads = 4)
        assertEquals(2, api.maxDownloading)
        assertEquals(listOf(1L, 2L, 3L, 4L), api.downloadCalls.sorted())
    }

    @Test fun prefetchSkipsOnCellularWhenNotAllowed() = cacheTest {
        // The lookahead is allowed on cellular (2 tracks); the favorites sync is not (FavoritesSyncTest).
        network.isExpensive = true
        cache.prefetch(listOf(track(1), track(2)))
        settle(downloads = 2)
        assertEquals(listOf(1L, 2L), api.downloadCalls.sorted())
        // Offline, or signed out: nothing is asked.
        network.isOnline = false
        cache.prefetch(listOf(track(3)))
        network.isOnline = true
        api.token = null
        cache.prefetch(listOf(track(4)))
        settle()
        assertEquals(2, api.downloadCalls.size)
        assertFalse(cache.isDownloading)
    }

    @Test fun prefetchStoresTheItemsMetaAndItsFavoriteFlag() = cacheTest {
        val fav = Item(Item.Kind.track, "3", "T3", "A", "B", 1000, buildJsonObject { put("id", 3); put("favorite", true) })
        cache.prefetch(listOf(fav, track(4), episode("abcdefghijk")))
        settle(downloads = 2)
        assertEquals(listOf(3L, 4L), api.downloadCalls.sorted())            // episodes are never cached
        assertEquals(listOf(3L), favoriteIds())
        assertEquals(fav.meta, cache.cachedFavorites().first().json)
    }

    @Test fun failedDownloadLeavesNothingAndCanBeRetried() = cacheTest {
        api.downloadErrors[5] = LarkError.Http(500, null)
        cache.prefetch(listOf(track(5)))
        settle(downloads = 1)
        assertNull(cache.localFile(5))
        assertEquals(0, cache.usedBytes)
        api.downloadErrors.clear()
        cache.prefetch(listOf(track(5)))
        settle(downloads = 2)
        assertNotNull(cache.localFile(5))
    }

    @Test fun downloadFinishingAfterClearIsDropped() = cacheTest {
        api.holdDownloads = true
        cache.prefetch(listOf(track(5)))
        settle()
        assertEquals(1, api.heldCount)
        cache.clear()
        api.releaseDownloads()
        settle()
        assertEquals(1, api.finishedDownloads)
        assertNull(cache.localFile(5))
        assertEquals(0, cache.usedBytes)
        assertEquals(emptyList<String>(), File(root, "staging").list()?.toList() ?: emptyList<String>())
    }

    @Test fun closedCacheDropsDownloadsInFlight() = cacheTest {
        api.holdDownloads = true
        cache.prefetch(listOf(track(5)))
        settle()
        assertEquals(1, api.heldCount)
        cache.close()                                                       // signed out: this user's cache is set aside
        api.releaseDownloads()
        settle()
        assertEquals(1, api.finishedDownloads)
        cache.prefetch(listOf(track(6)))                                    // and asks for nothing more
        settle()
        assertEquals(listOf(5L), api.downloadCalls)
        assertNull(makeCache().localFile(5))
    }

    /** The real API: `quality=high`, the token as a header and never in the URL, the file moved into `files/`. */
    @Test fun downloadGoesThroughLarkApiWithTheTokenHeader() = cacheTest {
        val server = MockWebServer().also { it.start() }
        try {
            val auth = AuthStore(MemorySecretStore().also { it.set("token", "T1") }, FakeCookies())
            val real = LarkApi(server.url("/").toString(), OkHttpClient(), auth)
            cache = CacheStore(root, real, network, clock, cacheScope, scheduler, { free })
            server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
                override fun dispatch(request: okhttp3.mockwebserver.RecordedRequest): MockResponse =
                    if (request.path!!.contains("/stream")) MockResponse().setHeader("Content-Type", "audio/mp4").setBody(Buffer().write(ByteArray(321) { 9 }))
                    else MockResponse().setResponseCode(404)               // the cover and the lyrics
            }
            val job = cache.download(Track(12, "T12", "A", "B", 200_000, false), kotlinx.serialization.json.JsonNull)!!.job
            job.join()
            val r = server.takeRequest()
            assertEquals("/api/v1/tracks/12/stream?quality=high", r.path)
            assertEquals("Bearer T1", r.getHeader("Authorization"))
            assertFalse(r.path!!.contains("T1"))
            val file = cache.localFile(12)!!
            assertEquals("12.m4a", file.name)
            assertEquals("files", file.parentFile!!.name)
            assertEquals(321, cache.usedBytes)
            assertEquals(emptyList<String>(), File(root, "staging").list()!!.toList())
        } finally { server.shutdown() }
    }

    @Test fun outOfSpaceErrorsAreRecognised() {
        assertTrue(CacheStore.isOutOfSpace(IOException("write failed: ENOSPC (No space left on device)")))
        assertTrue(CacheStore.isOutOfSpace(IOException("No space left on device")))
        assertTrue(CacheStore.isOutOfSpace(RuntimeException("x", IOException("write failed: ENOSPC (No space left on device)"))))
        assertFalse(CacheStore.isOutOfSpace(java.net.UnknownHostException("lark.test")))
        assertFalse(CacheStore.isOutOfSpace(LarkError.Http(500, null)))
    }

    // MARK: Covers (iOS covers its cover cache through the sync and wiring tests; here directly)

    @Test fun aDownloadAlsoKeepsTheCover() = cacheTest {
        api.artworkAnswer = ByteArray(5) { 3 }
        cache.prefetch(listOf(track(1)))
        settle(downloads = 1)
        assertEquals(5, cache.artworkFile(1)!!.length())
        assertEquals(emptyList<Long>(), cache.missingCovers(listOf(1)))
    }

    @Test fun noCoverIsRememberedForAWeek() = cacheTest {
        put(1, 10, true, t0)
        cache.cacheCover(1)                                                 // the fake answers no bytes: a marker
        assertNull(cache.artworkFile(1))
        assertEquals(emptyList<Long>(), cache.missingCovers(listOf(1)))
        clock.advance(7 * 24 * 3600 + 1.0)
        assertEquals(listOf(1L), cache.missingCovers(listOf(1)))
        api.artworkError = LarkError.Http(500, null)
        cache.cacheCover(1)
        assertEquals(listOf(1L), cache.missingCovers(listOf(1)))            // a network error: asked again next time
    }

    @Test fun aCoverGoesWithItsTrack() = cacheTest {
        api.artworkAnswer = ByteArray(5) { 3 }
        put(1, 10, true, t0)
        cache.cacheCover(1)
        val cover = File(root, "covers/1")
        assertTrue(cover.exists())
        cache.capBytes = 0
        assertFalse(cover.exists())
    }

    @Test fun staleTmpFilesOfCoversAreRemovedAtStart() = cacheTest {
        put(1, 10, true, t0)
        cache.flush()
        File(root, "covers").mkdirs()
        File(root, "covers/1.tmp").writeBytes(byteArrayOf(1))
        cache = makeCache()
        assertFalse(File(root, "covers/1.tmp").exists())
    }
}

/** The index is written at most once per `INDEX_WRITE_DELAY_S`, and flushed when it must be (`IndexWriteTests`). */
class IndexWriteTest : CacheTestCase() {
    @Test fun touchesAreCoalescedIntoOneWrite() = cacheTest {
        put(1, 100, true, t0)
        assertEquals(0, cache.indexWrites)                                  // not yet: the 5 s delay is pending
        cache.flush()
        assertEquals(1, cache.indexWrites)
        for (i in 1..10) { clock.current = at(i.toLong()); cache.touch(1) }
        assertEquals(1, cache.indexWrites)
        cache.flush()
        assertEquals(2, cache.indexWrites)
        cache.flush()                                                       // nothing new: no write
        assertEquals(2, cache.indexWrites)
        assertEquals(at(10).epochSecond.toDouble(), indexJson()["1"]!!.jsonObject["lastPlayed"]!!.jsonPrimitive.double, 0.0)
    }

    @Test fun theDelayedWriteLandsOnceOnItsOwn() = cacheTest {
        cache = makeCache(indexDelay = 0.1)
        put(1, 100, true, t0)
        for (i in 1..10) { clock.current = at(i.toLong()); cache.touch(1) }
        scheduler.advance(0.1)
        assertEquals(1, cache.indexWrites)
        scheduler.advance(0.3)
        assertEquals(1, cache.indexWrites)
        val e = kotlinx.serialization.json.Json.parseToJsonElement(File(root, "index.json").readText()).jsonObject["1"]!!.jsonObject
        assertEquals(at(10).epochSecond.toDouble(), e["lastPlayed"]!!.jsonPrimitive.double, 0.0)
    }

    @Test fun closeFlushes() = cacheTest {
        put(1, 100, true, t0)
        cache.close()
        assertEquals(1, cache.indexWrites)
        val reopened = CacheStore(root, api, network, clock, cacheScope, scheduler)
        assertNotNull(reopened.localFile(1))
    }

    /** After the system purges the files, noticing each missing one is not a write each. */
    @Test fun purgeRecoveryWritesTheIndexOnce() = cacheTest {
        for (id in 1..20) put(id, 10, true, at(id.toLong()))
        cache.flush()
        val writes = cache.indexWrites
        File(root, "files").deleteRecursively()
        for (id in 1L..20L) assertNull(cache.localFile(id))
        assertEquals(writes, cache.indexWrites)
        cache.flush()
        assertEquals(writes + 1, cache.indexWrites)
    }

    @Test fun theIndexIsWrittenAtomically() = cacheTest {
        put(1, 100, true, t0)
        cache.flush()
        assertEquals(listOf("index.json"), root.list()!!.filter { it.startsWith("index") })   // no temporary file left behind
    }
}

/** Disk space and headroom (`CacheSpaceTests`). */
class CacheSpaceTest : CacheTestCase() {
    @Test fun lookaheadSkipsWhenTheDiskIsNearlyFull() = cacheTest {
        free = 400L shl 20                                                  // under the 500 MB reserve
        cache.prefetch(listOf(track(1), track(2)))
        settle()
        assertEquals(emptyList<Long>(), api.downloadCalls)
        free = 100L shl 30
        cache.prefetch(listOf(track(1)))
        settle(downloads = 1)
        assertEquals(listOf(1L), api.downloadCalls)
    }

    @Test fun unknownFreeSpaceOnlyCountsTheCap() = cacheTest {
        free = null
        cache.prefetch(listOf(track(1)))
        settle(downloads = 1)
        assertEquals(listOf(1L), api.downloadCalls)
    }
}
