package lark.player

import kotlinx.serialization.json.JsonPrimitive
import lark.api.RandomFavorites
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.WebMessage
import lark.bridge.get
import lark.testing.EngineTestCase
import lark.testing.FakeLarkApi.ExcludeCall
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Port of ios/Tests/NeverStopTests.swift. */
class NeverStopTest : EngineTestCase() {
    @Test fun failedItemIsSkipped() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        sent.clear()
        backend.fail(network = false)
        assertEquals(remote(8), backend.loads.last().src)
        assertEquals(listOf("7", "8"), queues(Item.Kind.track).last().items.map { it.id })
        assertEquals(1, queues(Item.Kind.track).last().index)
    }

    @Test fun aFailedItemIsPassedOverForThirtyMinutes() = engineTest {
        engine.handle(q(listOf(7, 8, 7, 9), 0, 0, true))
        backend.fail(network = false)                 // 7 failed → 8
        backend.start(); backend.finish()            // 8 ends: the second 7 is passed over
        assertEquals(remote(9), backend.loads.last().src)
        advanceClock(31 * 60.0)
        engine.handle(q(listOf(1, 7), 0, 0, true))
        backend.start(); backend.finish()
        assertEquals(remote(7), backend.loads.last().src)
    }

    @Test fun threeFailuresInARowOnlyLocalChoicesCount() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4, 5), 0, 0, true))
        backend.fail(network = false); backend.fail(network = false); backend.fail(network = false)
        assertEquals(3, backend.loads.size)
        assertFalse(engine.playing)
        assertNotNull(states.last().error)
    }

    @Test fun threeFailuresInARowPlaysACachedOneWhenThereIsOne() = engineTest {
        cache.local[5] = tmp("5.m4a")
        engine.handle(q(listOf(1, 2, 3, 4, 5), 0, 0, true))
        backend.fail(network = false); backend.fail(network = false); backend.fail(network = false)
        assertEquals(MediaSource.File(tmp("5.m4a")), backend.loads.last().src)
        assertEquals(listOf("1", "2", "3", "5", "4"), ids)   // moved up, 4 stays to play later
        backend.start()
        backend.finish()                                                         // a success resets the count
        assertEquals(remote(4), backend.loads.last().src)
    }

    @Test fun offlineSkipsToNextCachedItem() = engineTest {
        network.isOnline = false
        val f = tmp("9.m4a"); cache.local[9] = f
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        engine.next()
        assertEquals(MediaSource.File(f), backend.loads.last().src)
        assertEquals("9", engine.music.items[engine.music.index].id)
    }

    @Test fun offlineNothingCachedInQueueFallsBackToCachedFavorites() = engineTest {
        network.isOnline = false
        cache.local[20] = tmp("20.m4a"); cache.local[21] = tmp("21.m4a")
        cache.favorites = listOf(trackModel(20), trackModel(21))
        randomValue = 0.99
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.fail(network = true)                  // offline: no retry, straight to what is on the phone
        assertEquals(MediaSource.File(tmp("21.m4a")), backend.loads.last().src)
        assertEquals(listOf("7", "21", "7", "8"), ids)   // 7 is requeued, to play once online
        assertEquals(texts.offlineNotice, notices.last())
        assertEquals(JsonPrimitive("T21"), engine.music.items[1].meta["title"])
    }

    @Test fun cachedFavoriteInTheRecentHistoryIsPassedOver() = engineTest {
        network.isOnline = false
        cache.local[20] = tmp("20.m4a"); cache.local[21] = tmp("21.m4a")
        cache.favorites = listOf(trackModel(20), trackModel(21))
        randomValue = 0.0
        engine.handle(q(listOf(20, 7, 8), 1, 0, true))
        engine.next()
        assertEquals(MediaSource.File(tmp("21.m4a")), backend.loads.last().src)
    }

    @Test fun networkErrorRetriesSameItemThenMovesToCached() = engineTest {
        cache.local[9] = tmp("9.m4a")
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        backend.start(); backend.playTo(12_000)
        backend.fail(network = true)
        assertEquals(1, backend.loads.size)
        scheduler.advance(1.9)
        assertEquals(1, backend.loads.size)
        scheduler.advance(0.1)
        assertEquals(2, backend.loads.size)
        assertEquals(remote(7), backend.loads.last().src); assertEquals(12_000L, backend.loads.last().startMs)
        assertEquals(true, backend.loads.last().autoplay)
        backend.fail(network = true); scheduler.advance(5.0)
        assertEquals(3, backend.loads.size); assertEquals(12_000L, backend.loads.last().startMs)
        backend.fail(network = true); scheduler.advance(10.0)
        assertEquals(4, backend.loads.size)
        backend.fail(network = true)
        assertEquals(MediaSource.File(tmp("9.m4a")), backend.loads.last().src)
    }

    @Test fun aUserActionCancelsAPendingRetry() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.fail(network = true)
        engine.next()
        val n = backend.loads.size
        scheduler.advance(30.0)
        assertEquals(n, backend.loads.size)
    }

    @Test fun endOfFavoritesQueueRefills() = engineTest {
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(30), trackModel(8), trackModel(31)))
        engine.handle(q(listOf(5, 6, 7, 8), 0, 0, true, QueueSource.favorites))
        idle()
        assertTrue(api.randomFavoritesCalls.isEmpty())       // 3 upcoming: not yet
        backend.start(); backend.finish()                     // 2 upcoming
        idle()
        assertEquals(listOf(ExcludeCall(20, listOf(6, 7, 8))), api.randomFavoritesCalls)
        assertEquals(listOf("5", "6", "7", "8", "30", "31"), ids)   // 8 is still upcoming: not twice
        assertEquals(6, queues(Item.Kind.track).last().items.size)
        assertEquals(JsonPrimitive("T31"), engine.music.items.last().meta["title"])
    }

    /**
     * The page plays one favorite (`playList([track], 0, source: favorites)`): the refill must not queue that same song
     * right after itself, or Next (the car's, the lock screen's) plays the song again (SmokeFlowTest's flake: the
     * server's shuffle of all favorites, the current one first, appended as is).
     */
    @Test fun aOneSongFavoritesQueueNeverRefillsWithTheCurrentSongNext() = engineTest {
        api.favoritesLibrary = listOf(trackModel(5), trackModel(6), trackModel(7))   // the shuffle put the current first
        engine.handle(q(listOf(5), 0, 0, true, QueueSource.favorites))
        idle()
        assertEquals(listOf(ExcludeCall(20, listOf(5))), api.randomFavoritesCalls)
        assertEquals(listOf("5", "6", "7"), ids)
        engine.next()
        assertEquals("6", engine.current?.id)
    }

    @Test fun favoritesRefillThatFellBackBecomesAShuffle() = engineTest {
        api.randomFavoritesAnswer = RandomFavorites("all", listOf(trackModel(30)))
        engine.handle(q(listOf(5, 6), 0, 0, true, QueueSource.favorites))
        idle()
        assertEquals(QueueSource.shuffle, engine.music.source)
    }

    @Test fun radioAndShuffleRefill() = engineTest {
        api.radioAnswer = listOf(trackModel(40), trackModel(5))
        engine.handle(q(listOf(5, 6), 0, 0, true, QueueSource.radio))
        idle()
        assertEquals(listOf(ExcludeCall(10, listOf(5, 6))), api.radioCalls)
        assertEquals(listOf("5", "6", "40"), ids)           // radio never repeats a queued track

        api.randomTracksAnswer = listOf(trackModel(50))
        engine.handle(q(listOf(1, 2), 0, 0, true, QueueSource.shuffle))
        idle()
        assertEquals(listOf(ExcludeCall(20, listOf(1, 2))), api.randomTracksCalls)
        assertEquals(listOf("1", "2", "50"), ids)
    }

    @Test fun listQueueIsNeverRefilledAndAnExhaustedRadioStopsAsking() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true, QueueSource.list))
        idle()
        assertTrue(api.radioCalls.isEmpty() && api.randomTracksCalls.isEmpty() && api.randomFavoritesCalls.isEmpty())

        api.radioAnswer = emptyList()
        engine.handle(q(listOf(1, 2), 0, 0, true, QueueSource.radio))
        idle()
        backend.start(); backend.finish()
        idle()
        assertEquals(1, api.radioCalls.size)
    }

    @Test fun aFailedRefillIsRetriedAfterThirtySeconds() = engineTest {
        api.refillError = java.io.IOException("not connected")
        engine.handle(q(listOf(1, 2), 0, 0, true, QueueSource.radio))
        idle()
        assertEquals(1, api.radioCalls.size)
        api.refillError = null; api.radioAnswer = listOf(trackModel(3))
        scheduler.advance(30.0)
        idle()
        assertEquals(2, api.radioCalls.size)
        assertEquals(listOf("1", "2", "3"), ids)
    }

    @Test fun shuffleFavoritesPrefersTheCacheThenTheServer() = engineTest {
        cache.local[20] = tmp("20.m4a"); cache.local[21] = tmp("21.m4a")
        cache.favorites = listOf(trackModel(20), trackModel(21))
        engine.shuffleFavorites()
        assertEquals(QueueSource.favorites, engine.music.source)
        assertEquals(setOf("20", "21"), ids.toSet())
        assertFalse(api.randomFavoritesCalls.any { it.n == 50 })   // no network for the start
        assertTrue("expected a cached file", backend.loads.last().src is MediaSource.File)
        assertEquals(true, backend.loads.last().autoplay)

        cache.favorites = emptyList()
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(30), trackModel(31)))
        engine.shuffleFavorites()
        assertEquals(ExcludeCall(50, emptyList()), api.randomFavoritesCalls.first { it.n == 50 })
        assertEquals(listOf("30", "31"), ids.take(2))
    }

    @Test fun resumeOrShuffleFavoritesResumesTheRestoredQueue() = engineTest {
        engine.handle(q(listOf(7, 8), 1, 9_000, false))
        makeEngine()
        cache.favorites = listOf(trackModel(20))
        engine.resumeOrShuffleFavorites()
        assertEquals(remote(8), backend.loads.last().src); assertEquals(9_000L, backend.loads.last().startMs)
        assertEquals(true, backend.loads.last().autoplay)
        assertTrue(engine.playing)
    }

    /**
     * Shuffle and repeat at their defaults (or turned back off): the refill and the favorite fallback are
     * exactly what they were before the modes existed.
     */
    @Test fun defaultModesLeaveNeverStopAsItWas() = engineTest {
        assertEquals(PlayModes(), engine.modes)
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(WebMessage.SetModes(false, RepeatMode.off))
        assertEquals(PlayModes(), engine.modes)
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(30), trackModel(31)))
        engine.handle(q(listOf(5, 6, 7), 0, 0, true, QueueSource.favorites))
        idle()
        assertEquals(listOf(ExcludeCall(20, listOf(5, 6, 7))), api.randomFavoritesCalls)
        assertEquals(listOf("5", "6", "7", "30", "31"), ids)

        // The end of a list offline: a cached favorite is put after it (never a wrap, never a stop).
        network.isOnline = false
        cache.local[8] = tmp("8.m4a"); cache.local[20] = tmp("20.m4a")
        cache.favorites = listOf(trackModel(20))
        engine.handle(q(listOf(8), 0, 0, true))
        backend.start(); backend.finish()
        assertEquals(listOf("8", "20"), ids)
        assertEquals(MediaSource.File(tmp("20.m4a")), backend.loads.last().src)
        assertEquals(texts.offlineNotice, notices.last())
    }
}
