package lark.player

import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.bridge.get
import lark.testing.EngineTestCase
import lark.testing.episode
import lark.testing.q
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of the engine-level cases of ios/Tests/QueueStoreTests.swift (the store-level ones are QueueStoreTest):
 * the engine saves both queues, a new engine restores them paused, and a reset wipes them.
 */
class QueueStoreEngineTest : EngineTestCase() {
    private val queueFile get() = tmp("queues-1.json")

    @Test fun setQueueWritesThePerUserFile() = engineTest {
        assertFalse(queueFile.exists())
        engine.handle(q(listOf(7, 8), 1, 3_000, false))
        assertTrue(queueFile.exists())
        assertEquals(listOf("7", "8"), store.load()?.music?.items?.map { it.id })
    }

    @Test fun aNewEngineRestoresBothQueuesTheActiveKindAndThePositionPaused() = engineTest {
        engine.handle(q(listOf(7, 8), 1, 3_000, true))
        backend.start()
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("abcdefghijk")), 0, 60_000, true, QueueSource.list))
        backend.start(); backend.playTo(70_000)
        engine.tick()
        makeEngine()
        assertEquals(listOf("7", "8"), ids); assertEquals(1, engine.music.index)
        assertEquals(3_000L, engine.music.positionMs)
        assertEquals(listOf("abcdefghijk"), engine.episodes.items.map { it.id }); assertEquals(70_000L, engine.episodes.positionMs)
        assertEquals(JsonPrimitive("abcdefghijk"), engine.episodes.items.first().meta["video_id"])
        assertEquals(Item.Kind.episode, engine.active)
        assertFalse(engine.playing)
        assertTrue(backend.loads.isEmpty())       // nothing touches the network until play
    }

    @Test fun tickWhilePlayingSavesThePosition() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(21_000)
        assertEquals(0L, store.load()?.music?.positionMs)
        engine.tick()
        assertEquals(21_000L, store.load()?.music?.positionMs)
    }

    @Test fun pauseSavesThePosition() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(8_000)
        engine.pause()
        assertEquals(8_000L, store.load()?.music?.positionMs)
    }

    @Test fun resetStopsAndDeletesTheFiles() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start(); backend.playTo(40_000)
        engine.next()                                       // a pending play event
        assertTrue(tmp("events-1.json").exists())
        engine.reset()
        assertFalse(queueFile.exists())
        assertFalse(tmp("events-1.json").exists())
        assertTrue(engine.music.items.isEmpty()); assertTrue(engine.episodes.items.isEmpty())
        assertFalse(engine.playing)
        assertEquals("stop", backend.calls.last())
        assertTrue(events.pending.isEmpty())
        assertEquals(0, queues(Item.Kind.track).last().items.size)
    }
}
