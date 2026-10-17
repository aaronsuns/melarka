package lark.player

import lark.testing.EngineTestCase
import lark.testing.q
import lark.bridge.NativeEvent
import lark.bridge.WebMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

/**
 * Port of the engine-level cases of ios/Tests/PlayEventTests.swift (the queue-level ones are PlayEventQueueTest):
 * which listens become play events, and how `flushEvents` answers.
 */
class PlayEventTest : EngineTestCase() {
    private val eventsFile get() = tmp("events-1.json")

    private fun play(id: Int, seconds: Long) {
        engine.handle(q(listOf(id, 99), 0, 0, true))
        backend.start(); backend.playTo(seconds * 1000)
    }

    @Test fun fortySecondsThenNextIsNotASkip() = engineTest {
        play(7, 40)
        engine.next()
        idle()
        val e = api.postedEvents.last().first()
        assertEquals(7L, e.trackId); assertEquals(40L, e.playedSeconds); assertEquals(false, e.skipped)
        assertEquals("high", e.quality); assertEquals(clock.now().epochSecond, e.startedAt)
        assertEquals(36, e.clientEventId.length)
    }

    @Test fun tenSecondsThenNextIsASkip() = engineTest {
        play(7, 10)
        engine.next()
        idle()
        assertEquals(true, api.postedEvents.last().first().skipped)
        assertEquals(10L, api.postedEvents.last().first().playedSeconds)
    }

    @Test fun aNaturalEndIsNotASkip() = engineTest {
        play(7, 10)
        backend.finish()
        idle()
        assertEquals(false, api.postedEvents.last().first().skipped)
    }

    @Test fun seekingDoesNotCountAsListening() = engineTest {
        play(7, 5)
        engine.seek(100_000)
        backend.playTo(105_000)
        engine.next()
        idle()
        assertEquals(10L, api.postedEvents.last().first().playedSeconds)
    }

    @Test fun underOneSecondNoEvent() = engineTest {
        play(7, 0)
        backend.playTo(500)
        engine.next()
        idle()
        assertTrue(api.postedEvents.isEmpty())
    }

    @Test fun eventsPersistAndSurviveAFailedPost() = engineTest {
        api.postEventsError = IOException("not connected")
        play(7, 40)
        engine.next()
        idle()
        assertEquals(1, api.postedEvents.size)
        assertTrue(eventsFile.exists())
        val id = events.pending.first().clientEventId

        makeEngine()                                         // a relaunch: still pending
        assertEquals(listOf(id), events.pending.map { it.clientEventId })
        api.postEventsError = null
        events.flush()
        assertEquals(listOf(id), api.postedEvents.last().map { it.clientEventId })  // the same id: the server dedupes
        assertTrue(events.pending.isEmpty())
    }

    @Test fun tickFlushesPendingEventsEveryThirtySeconds() = engineTest {
        api.postEventsError = IOException("timed out")
        play(7, 40)
        engine.next()
        idle()
        api.postEventsError = null
        advanceClock(20.0); engine.tick(); idle()
        assertEquals(1, api.postedEvents.size)
        advanceClock(10.0); engine.tick(); idle()
        assertEquals(2, api.postedEvents.size)
        assertTrue(events.pending.isEmpty())
    }

    @Test fun flushEventsPostsThenRepliesFlushedEvenWhenThePostFails() = engineTest {
        api.postEventsError = IOException("timed out")
        play(7, 40)
        engine.handle(WebMessage.FlushEvents("f1"))
        idle()
        assertEquals(40L, api.postedEvents.last().first().playedSeconds)   // the listen in progress is recorded
        assertTrue(sent.contains(NativeEvent.Flushed("f1")))
        assertEquals(1, events.pending.size)

        backend.playTo(70_000)                                          // the web's finishListen("switch"): no restart,
        api.postEventsError = null                                      // so one play is never counted twice
        engine.next()
        idle()
        assertEquals(listOf(40L), api.postedEvents.last().map { it.playedSeconds })
    }
}
