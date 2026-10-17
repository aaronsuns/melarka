package lark.player

import kotlinx.serialization.json.JsonPrimitive
import lark.api.QueueAnswer
import lark.api.ServerQueue
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.bridge.get
import lark.testing.EngineTestCase
import lark.testing.FakeLarkApi.SaveCall
import lark.testing.episode
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of the engine-level cases of ios/Tests/QueueSyncTests.swift (the sync-level ones are QueueSyncTest): what
 * the engine sends to `PUT /queue`, and the server's queue adopted on the first hello (cold start, on-open shuffle).
 */
class QueueSyncEngineTest : EngineTestCase() {
    private fun serverQueue(ids: List<Long>, index: Int, pos: Long, by: String, at: Long) =
        QueueAnswer(ServerQueue(ids, index, pos, 3, by, at), ids.map { trackModel(it.toInt()) })
    private val nowS get() = clock.now().epochSecond

    @Test fun putIsDebouncedOneSecondAfterEdits() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, false))
        engine.handle(q(listOf(7, 9, 8), 0, null, false))
        scheduler.advance(0.9); idle()
        assertTrue(api.saveCalls.isEmpty())
        engine.handle(q(listOf(7, 9, 8, 10), 0, null, false))
        scheduler.advance(0.9); idle()
        assertTrue(api.saveCalls.isEmpty())
        scheduler.advance(0.1); idle()
        assertEquals(listOf(SaveCall(listOf(7, 9, 8, 10), 0, 0)), api.saveCalls)
    }

    @Test fun putEveryFifteenSecondsWhilePlaying() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.start()
        scheduler.advance(1.0); idle()
        assertEquals(1, api.saveCalls.size)
        advanceClock(10.0); backend.playTo(10_000); idle()
        assertEquals(1, api.saveCalls.size)
        backend.playTo(14_500); advanceClock(5.0); backend.playTo(15_000); idle()
        assertEquals(2, api.saveCalls.size)
        assertEquals(SaveCall(listOf(7, 8), 0, 15_000), api.saveCalls.last())
        engine.pause()
        advanceClock(60.0); engine.tick(); idle()
        scheduler.advance(1.0); idle()
        assertEquals(3, api.saveCalls.size)                 // the pause, then nothing while paused
        advanceClock(60.0); engine.tick(); scheduler.advance(1.0); idle()
        assertEquals(3, api.saveCalls.size)
    }

    @Test fun episodesAreNeverSent() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("abcdefghijk")), 0, 0, true, QueueSource.list))
        backend.start()
        advanceClock(30.0); backend.playTo(30_000); engine.tick()
        scheduler.advance(5.0); idle()
        assertTrue(api.saveCalls.isEmpty())
    }

    /**
     * A YouTube playlist played from search ("yt:" items) stays on the phone: the server keeps the last
     * library queue, which the web and the other devices restore.
     */
    @Test fun aQueueWithYouTubeTracksIsNotSent() = engineTest {
        val yt = Item(Item.Kind.track, "yt:mjuS9shGYhE", "流年", "王菲", "YouTube", 263_000)
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(yt), 0, 0, true, QueueSource.list))
        backend.start()
        advanceClock(30.0); backend.playTo(30_000); engine.tick()
        scheduler.advance(5.0); idle()
        assertTrue(api.saveCalls.isEmpty())
        engine.handle(q(listOf(7, 8), 0, 0, false))   // a library queue again: sent
        scheduler.advance(1.0); idle()
        assertEquals(listOf(SaveCall(listOf(7, 8), 0, 0)), api.saveCalls)
    }

    @Test fun coldStartWithAnEmptyLocalQueueAdoptsTheServers() = engineTest {
        api.queueAnswer = serverQueue(listOf(4, 5, 6), 1, 7_000, "device:2", nowS - 3600)
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(listOf("4", "5", "6"), ids); assertEquals(1, engine.music.index)
        assertEquals(7_000L, engine.music.positionMs); assertEquals(QueueSource.restored, engine.music.source)
        assertEquals(JsonPrimitive("T5"), engine.music.items[1].meta["title"])
        assertFalse(engine.playing); assertTrue(backend.loads.isEmpty())
        assertEquals(3, queues(Item.Kind.track).last().items.size)
        scheduler.advance(2.0); idle()
        assertTrue(api.saveCalls.isEmpty())                   // not echoed back
    }

    @Test fun serverTracksMissingFromTheAnswerAreDropped() = engineTest {
        api.queueAnswer = QueueAnswer(ServerQueue(listOf(4, 5, 6), 2, 0, 1, "device:2", 1), listOf(trackModel(4), trackModel(6)))
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(listOf("4", "6"), ids); assertEquals(1, engine.music.index)
    }

    @Test fun aNewerQueueFromAnotherDeviceIsAdoptedWhenNothingPlays() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, false))
        scheduler.advance(1.0); idle()             // our PUT: the server says we are device:1
        makeEngine()
        api.queueAnswer = serverQueue(listOf(4), 0, 0, "device:2", nowS + 6)
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(listOf("4"), ids)
    }

    @Test fun theServerQueueIsNotAdoptedWhenNotNewerEnough() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, false))
        makeEngine()
        api.queueAnswer = serverQueue(listOf(4), 0, 0, "device:2", nowS + 5)
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(listOf("7", "8"), ids)
    }

    @Test fun ourOwnQueueOnTheServerIsNotAdopted() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, false))
        scheduler.advance(1.0); idle()
        makeEngine()
        api.queueAnswer = serverQueue(listOf(4), 0, 0, "device:1", nowS + 60)
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(listOf("7", "8"), ids)
    }

    @Test fun whilePlayingTheServerQueueIsNeverAdopted() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.start()
        api.queueAnswer = serverQueue(listOf(4), 0, 0, "device:2", nowS + 600)
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(listOf("7", "8"), ids)
    }

    @Test fun onlyTheFirstHelloAfterLaunchRunsOnOpen() = engineTest {
        engine.handle(WebMessage.Hello("nothing"))
        idle()
        api.queueAnswer = serverQueue(listOf(4), 0, 0, "device:2", 1)
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(0, api.queueCalls)
    }

    @Test fun onOpenShuffleFavoritesPlaysOnlyWhenNothingPlays() = engineTest {
        cache.local[20] = tmp("20.m4a"); cache.favorites = listOf(trackModel(20))
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start()
        engine.handle(WebMessage.Hello("shuffle_favorites"))
        idle()
        assertEquals(listOf("7"), ids)

        makeEngine()
        engine.handle(WebMessage.Hello("shuffle_favorites"))
        idle()
        assertEquals("20", engine.music.items.first().id)
        assertEquals(QueueSource.favorites, engine.music.source)
    }
}
