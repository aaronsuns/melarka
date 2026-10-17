package lark.player

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.api.LarkError
import lark.api.PlayEvent
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.bridge.get
import lark.bridge.lenientJson
import lark.testing.EngineTestCase
import lark.testing.episode
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

/**
 * Port of ios/Tests/EngineRobustnessTests.swift: retries, stalls, the network coming back, episode fallbacks,
 * late callbacks, episode progress in the queue, and the smaller edges.
 */
class EngineRobustnessTest : EngineTestCase() {
    private fun playEpisodes(vararg items: Item) {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, items.toList(), 0, null, true, QueueSource.list))
        backend.start()
    }

    // MARK: 1. A retry that worked is over

    @Test fun pauseAfterASuccessfulRetryPausesTheAudio() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(12_000)
        backend.fail(network = true); scheduler.advance(2.0); backend.start()
        engine.pause()
        assertEquals("pause", backend.calls.last())
        assertFalse(engine.playing)
    }

    @Test fun aSecondBlipRetriesFromWhereItWasNow() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(12_000)
        backend.fail(network = true); scheduler.advance(2.0); backend.start()
        backend.playTo(40_000)
        backend.fail(network = true); scheduler.advance(2.0)
        assertEquals(40_000L, backend.loads.last().startMs)
    }

    @Test fun unrelatedBlipsDoNotUseUpTheRetries() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        repeat(5) { backend.start(); backend.fail(network = true); scheduler.advance(10.0) }
        assertEquals(6, backend.loads.size)                     // every blip got its retry
        assertEquals(remote(7), backend.loads.last().src)
        assertTrue(engine.playing)
    }

    @Test fun whileARetryWaitsTheSavedPositionIsTheRetryPosition() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(12_000)
        backend.fail(network = true)
        backend.positionMs = 0                       // a failed item reads 0
        engine.pause()
        assertEquals(12_000L, store.load()?.music?.positionMs)
    }

    // MARK: 2. Stall switch

    @Test fun aSixSecondStallSwitchesToACachedTrackAndRequeuesTheCurrent() = engineTest {
        cache.local[9] = tmp("9.m4a")
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        backend.start(); backend.buffering(true)
        scheduler.advance(5.9)
        assertEquals(1, backend.loads.size)
        scheduler.advance(0.1)
        assertEquals(MediaSource.File(tmp("9.m4a")), backend.loads.last().src)
        assertEquals(listOf("7", "9", "7", "8"), ids)
        assertEquals(1, engine.music.index)
        assertEquals(texts.stallNotice, notices.last())
    }

    @Test fun aStallThatEndsInTimeDoesNotSwitch() = engineTest {
        cache.local[9] = tmp("9.m4a")
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        backend.start(); backend.buffering(true)
        scheduler.advance(3.0); backend.buffering(false); scheduler.advance(10.0)
        assertEquals(1, backend.loads.size)
    }

    @Test fun aCachedTrackOrAPauseNeverStallSwitches() = engineTest {
        cache.local[7] = tmp("7.m4a"); cache.local[9] = tmp("9.m4a")
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        backend.start(); backend.buffering(true); scheduler.advance(10.0)
        assertEquals(1, backend.loads.size)
        engine.handle(q(listOf(1, 9), 0, 0, true))
        backend.start(); backend.buffering(true); engine.pause(); scheduler.advance(10.0)
        assertEquals(2, backend.loads.size)
    }

    // MARK: 3. The network coming back

    @Test fun comingBackOnlineRestartsAPlayerStoppedForTheNetwork() = engineTest {
        network.isOnline = false
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.fail(network = true)
        assertFalse(engine.playing)
        assertNotNull(states.last().error)
        network.isOnline = true
        assertEquals(remote(7), backend.loads.last().src); assertEquals(true, backend.loads.last().autoplay)
        assertTrue(engine.playing)
        assertNull(states.last().error)
    }

    @Test fun comingBackOnlineDoesNotStartAPausedPlayer() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); engine.pause()
        network.isOnline = false; network.isOnline = true
        assertEquals(1, backend.loads.size)
        assertFalse(engine.playing)
    }

    @Test fun comingBackOnlineRetriesAWaitingRefill() = engineTest {
        api.refillError = IOException("not connected")
        engine.handle(q(listOf(1, 2), 0, 0, true, QueueSource.radio))
        idle()
        api.refillError = null; api.radioAnswer = listOf(trackModel(3))
        network.isOnline = false; network.isOnline = true
        idle()
        assertEquals(2, api.radioCalls.size)
        assertEquals(listOf("1", "2", "3"), ids)
    }

    // MARK: 4. Episodes and no network

    @Test fun carPlayOfflineWithAnEpisodeActivePlaysCachedFavorites() = engineTest {
        playEpisodes(episode("abcdefghijk"))
        makeEngine()
        assertEquals(Item.Kind.episode, engine.active)
        network.isOnline = false
        cache.local[20] = tmp("20.m4a"); cache.favorites = listOf(trackModel(20))
        engine.resumeOrShuffleFavorites()
        assertEquals(MediaSource.File(tmp("20.m4a")), backend.loads.last().src)
        assertEquals(Item.Kind.track, engine.active)
    }

    @Test fun carPlayOfflineWithAnEpisodeActiveResumesTheMusicQueue() = engineTest {
        engine.handle(q(listOf(7), 0, 5_000, false))
        playEpisodes(episode("abcdefghijk"))
        makeEngine()
        network.isOnline = false
        engine.resumeOrShuffleFavorites()
        // Offline, an uncached track is not tried as a stream; nothing is on the
        // phone, so nothing plays and the notice says why.
        assertTrue(backend.loads.isEmpty())
        assertTrue(notices.contains(texts.nothingCachedNotice))
        // Something of the music queue cached: it plays, as music.
        engine.handle(q(listOf(7, 8), 0, 5_000, false))
        cache.local[8] = tmp("8.m4a")
        engine.resumeOrShuffleFavorites()
        assertEquals(MediaSource.File(tmp("8.m4a")), backend.loads.last().src)
        assertEquals(Item.Kind.track, engine.active)
    }

    @Test fun anEpisodeFailingOfflineFallsBackToMusic() = engineTest {
        cache.local[20] = tmp("20.m4a"); cache.favorites = listOf(trackModel(20))
        playEpisodes(episode("abcdefghijk"))
        network.isOnline = false
        backend.fail(network = true)
        idle()
        assertEquals(MediaSource.File(tmp("20.m4a")), backend.loads.last().src)
        assertEquals(Item.Kind.track, engine.active)
        assertEquals(texts.episodeFallbackNotice, notices.first())
        assertEquals(1, engine.episodes.items.size)            // the episode stays queued for later
    }

    @Test fun anEpisodeWhoseRetriesRunOutFallsBackToMusic() = engineTest {
        engine.handle(q(listOf(7), 0, 0, false))
        playEpisodes(episode("abcdefghijk"))
        for (d in listOf(2.0, 5.0, 10.0)) { backend.fail(network = true); scheduler.advance(d) }
        backend.fail(network = true)
        idle()
        assertEquals(Item.Kind.track, engine.active)
        assertEquals(remote(7), backend.loads.last().src)
    }

    // MARK: 5. Late callbacks from a replaced item

    @Test fun aLateFailureOfTheOldItemIsIgnored() = engineTest {
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        val old = backend.generation
        backend.start(); engine.next()
        backend.fail(network = false, generation = old)
        assertEquals(2, backend.loads.size)
        assertEquals(1, engine.music.index)
    }

    @Test fun aLateEndOfTheOldItemIsIgnored() = engineTest {
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        val old = backend.generation
        backend.start(); engine.next()
        backend.finish(generation = old)
        assertEquals(2, backend.loads.size)
        assertEquals(1, engine.music.index)
    }

    @Test fun aLatePauseOfTheOldItemIsIgnored() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        val old = backend.generation
        backend.start(); engine.next(); backend.start()
        backend.interrupt(generation = old)
        assertTrue(engine.playing)
        backend.interrupt()
        assertFalse(engine.playing)
    }

    /** Android: a transient focus loss then a permanent one reports the pause twice; only the first acts. */
    @Test fun aSecondPauseReportIsIgnored() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.start(); backend.playTo(10_000)
        backend.interrupt()
        assertFalse(engine.playing); assertTrue(engine.pausedBySystem)
        val events = sent.size
        val savedAt = store.load()?.savedAt
        backend.positionMs = 0                                 // whatever the player reads by then
        backend.interrupt()
        assertEquals(events, sent.size)                        // no second state, no second save
        assertEquals(10_000L, engine.music.positionMs)
        assertEquals(savedAt, store.load()?.savedAt)
        assertTrue(engine.pausedBySystem)
        engine.play()
        assertTrue(engine.playing)
    }

    @Test fun aRetryIsANewGeneration() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        val first = backend.generation
        backend.fail(network = true); scheduler.advance(2.0)
        assertNotEquals(first, backend.generation)
        backend.fail(network = false, generation = first)
        assertEquals(2, backend.loads.size)
    }

    // MARK: 6. Episode progress kept in the queue

    @Test fun episodeResumeKeepsTheSavedPositionAfterNextAndPrev() = engineTest {
        playEpisodes(episode("aaaaaaaaaaa"), episode("bbbbbbbbbbb"))
        backend.playTo(600_000)
        engine.next()
        assertEquals(JsonPrimitive(600), engine.episodes.items[0].meta["position_s"])
        assertEquals(JsonPrimitive(600), queues(Item.Kind.episode).last().items[0].meta["position_s"])
        backend.start(); backend.playTo(5_000)
        engine.prev(); engine.prev()
        assertEquals(0, engine.episodes.index)
        assertEquals(600_000L, backend.loads.last().startMs)
        makeEngine()
        assertEquals(JsonPrimitive(600), engine.episodes.items[0].meta["position_s"])
    }

    @Test fun aFinishedEpisodeStartsFromZeroWhenGoneBackTo() = engineTest {
        playEpisodes(episode("aaaaaaaaaaa", positionS = 120), episode("bbbbbbbbbbb"))
        assertEquals(0L, backend.loads.last().startMs)                  // positionMs nil: the web decides; 0 here
        backend.finish()
        assertEquals(JsonPrimitive(true), engine.episodes.items[0].meta["played"])
        assertEquals(JsonPrimitive(0), engine.episodes.items[0].meta["position_s"])
        engine.prev()
        assertEquals(0, engine.episodes.index)
        assertEquals(0L, backend.loads.last().startMs)
    }

    // MARK: 7. A shuffle with nothing to play says why

    @Test fun shuffleOfflineWithNothingCachedSaysWhy() = engineTest {
        network.isOnline = false
        engine.shuffleFavorites()
        assertEquals(texts.nothingCachedNotice, notices.last())
        assertEquals(texts.nothingCachedNotice, states.last { it.kind == Item.Kind.track }.error)
    }

    @Test fun shuffleWhenTheServerFailsSaysWhy() = engineTest {
        api.refillError = IOException("timed out")
        engine.shuffleFavorites()
        assertEquals(texts.shuffleFailedNotice, notices.last())
        assertNotNull(states.last { it.kind == Item.Kind.track }.error)
    }

    // MARK: 10. Bounds and schema

    @Test fun pendingEventsAreCappedDroppingTheOldest() = engineTest {
        api.postEventsError = LarkError.Http(503, null)
        store.saveEvents((0 until PlayEventQueue.MAX_PENDING).map { PlayEvent("e$it", 1, 1, 40, false, "high") })
        events.add(2, 2, 40, false, "high")
        assertEquals(PlayEventQueue.MAX_PENDING, events.pending.size)
        assertEquals("e1", events.pending.first().clientEventId)
        assertEquals(2L, events.pending.last().trackId)
    }

    @Test fun theSnapshotCarriesASchemaVersion() = engineTest {
        engine.handle(q(listOf(7), 0, 0, false))
        val obj = lenientJson.parseToJsonElement(tmp("queues-1.json").readText())
        assertEquals(JsonPrimitive(QueueSnapshot.CURRENT_VERSION), obj["version"])
    }

    @Test fun anUnknownSchemaVersionReadsAsEmpty() = engineTest {
        engine.handle(q(listOf(7), 0, 0, false))
        val f = tmp("queues-1.json")
        val obj = lenientJson.parseToJsonElement(f.readText()) as JsonObject
        f.writeText(JsonObject(obj + ("version" to JsonPrimitive(99))).toString())
        makeEngine()
        assertTrue(engine.music.items.isEmpty())
    }

    // MARK: 11. No source for an item

    @Test fun noTokenSkipsToACachedItem() = engineTest {
        api.token = null
        cache.local[9] = tmp("9.m4a")
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        assertEquals(1, backend.loads.size)
        assertEquals(MediaSource.File(tmp("9.m4a")), backend.loads.last().src)
    }
}
