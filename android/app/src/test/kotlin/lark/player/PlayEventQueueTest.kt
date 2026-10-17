package lark.player

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import lark.testing.FakeClock
import lark.testing.FakeLarkApi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException

/** Port of the queue-level PlayEventTests (the listen-to-event rules, skip and seek, belong to the engine task). */
@OptIn(ExperimentalCoroutinesApi::class)
class PlayEventQueueTest {
    @get:Rule val tmp = TemporaryFolder()
    private val api = FakeLarkApi()
    private val clock = FakeClock()
    private lateinit var dir: File
    private lateinit var store: QueueStore

    @Before fun setUp() { dir = File(tmp.root, "lark"); store = QueueStore(dir).also { it.user = 1 } }

    private fun kotlinx.coroutines.test.TestScope.queue(s: QueueStore = store) = PlayEventQueue(api, s, this, clock)
    private fun PlayEventQueue.addOne(id: Long = 7, played: Long = 40) = add(id, 1700, played, false, "high")

    @Test fun anEventGetsAUuidOnceAndIsSent() = runTest {
        val q = queue()
        q.add(7, 1700, 40, false, "high")
        advanceUntilIdle()
        val e = api.postedEvents.last().single()
        assertEquals(7L, e.trackId); assertEquals(40L, e.playedSeconds); assertEquals(false, e.skipped)
        assertEquals("high", e.quality); assertEquals(1700L, e.startedAt)
        assertEquals(36, e.clientEventId.length)
        assertEquals(e.clientEventId.lowercase(), e.clientEventId)
        assertTrue(q.pending.isEmpty())
        assertFalse(File(dir, "events-1.json").exists())
    }

    @Test fun eventsPersistAndSurviveAFailedPost() = runTest {
        api.postEventsError = IOException("offline")
        val q = queue()
        q.addOne()
        advanceUntilIdle()
        assertEquals(1, api.postedEvents.size)
        assertTrue(File(dir, "events-1.json").exists())
        val id = q.pending.first().clientEventId

        val relaunched = queue()                                    // a relaunch: still pending
        assertEquals(listOf(id), relaunched.pending.map { it.clientEventId })
        api.postEventsError = null
        relaunched.flush()
        assertEquals(listOf(id), api.postedEvents.last().map { it.clientEventId })   // the same id: the server dedupes
        assertTrue(relaunched.pending.isEmpty())
    }

    @Test fun tickFlushesPendingEventsEveryThirtySeconds() = runTest {
        api.postEventsError = IOException("timeout")
        val q = queue()
        q.addOne(); advanceUntilIdle()
        api.postEventsError = null
        clock.advance(20.0); q.flushIfDue(); advanceUntilIdle()
        assertEquals(1, api.postedEvents.size)
        clock.advance(10.0); q.flushIfDue(); advanceUntilIdle()
        assertEquals(2, api.postedEvents.size)
        assertTrue(q.pending.isEmpty())
    }

    @Test fun nothingPendingNothingSent() = runTest {
        val q = queue()
        q.flushIfDue(); q.flush(); advanceUntilIdle()
        assertTrue(api.postedEvents.isEmpty())
    }

    @Test fun fewerAcceptedThanSentIsNotAnError() = runTest {
        api.accepted = 0                                    // the server skipped it (an unknown track)
        val q = queue()
        q.add(1, 1, 5, true, "high")
        q.flush()
        assertTrue(q.pending.isEmpty())
    }

    @Test fun offlineEventsWaitOnDiskAndTheFirstTickBackOnlineSendsThem() = runTest {
        val q = queue()
        var online = false
        q.online = { online }
        q.addOne(); q.flushIfDue(); advanceUntilIdle()
        assertTrue(api.postedEvents.isEmpty())
        assertEquals(1, q.pending.size)
        online = true
        q.flushIfDue(); advanceUntilIdle()
        assertEquals(1, api.postedEvents.size)
        assertTrue(q.pending.isEmpty())
    }

    @Test fun aFlushInFlightIsWaitedForAndNothingIsSentTwice() = runTest {
        val gate = CompletableDeferred<Unit>()
        api.gate = gate
        val q = queue()
        q.addOne(1); runCurrent()
        assertEquals(1, api.postedEvents.size)              // in flight
        val second = launch { q.flush() }; runCurrent()
        assertEquals(1, api.postedEvents.size)              // waits for the first
        gate.complete(Unit); advanceUntilIdle(); second.join()
        assertEquals(1, api.postedEvents.size)              // nothing left for the second
        assertTrue(q.pending.isEmpty())
    }

    @Test fun anEventAddedWhileAPostIsInFlightSurvivesIt() = runTest {
        val gate = CompletableDeferred<Unit>()
        api.gate = gate
        val q = queue()
        q.online = { false }
        q.addOne(1)
        val first = launch { q.flush() }; runCurrent()
        q.addOne(2)
        gate.complete(Unit); first.join()
        assertEquals(listOf(2L), q.pending.map { it.trackId })
    }

    @Test fun pastTwoThousandTheOldestAreDropped() = runTest {
        val q = queue()
        q.online = { false }
        for (i in 1..(PlayEventQueue.MAX_PENDING + 3)) q.addOne(i.toLong())
        val ids = q.pending.map { it.trackId }
        assertEquals(PlayEventQueue.MAX_PENDING, ids.size)
        assertEquals(4L, ids.first()); assertEquals((PlayEventQueue.MAX_PENDING + 3).toLong(), ids.last())
    }

    @Test fun noUserKeepsEventsInMemoryOnly() = runTest {
        val q = queue(QueueStore(dir))
        q.online = { false }
        q.addOne()
        assertEquals(1, q.pending.size)
        assertTrue(dir.list().isNullOrEmpty())
    }

    @Test fun clearForgetsEverythingOnDiskToo() = runTest {
        val q = queue()
        q.online = { false }
        q.addOne()
        q.clear()
        assertTrue(q.pending.isEmpty())
        assertFalse(File(dir, "events-1.json").exists())
    }
}
