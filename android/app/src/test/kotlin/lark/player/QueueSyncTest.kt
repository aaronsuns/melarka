package lark.player

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import lark.api.ServerQueue
import lark.testing.FakeClock
import lark.testing.FakeLarkApi
import lark.testing.FakeLarkApi.SaveCall
import lark.testing.FakeScheduler
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

/** Port of the sync-level QueueSyncTests; the engine-level ones (cold start adoption, hello) belong to the engine task. */
@OptIn(ExperimentalCoroutinesApi::class)
class QueueSyncTest {
    private val api = FakeLarkApi()
    private val clock = FakeClock()
    private val scheduler = FakeScheduler()
    private var ids = listOf(7L, 8L)
    private var positionMs = 0L
    private var yt = false
    private var networkOn = true

    private fun TestScope.sync() = QueueSync(api, scheduler, clock, this).also { s ->
        s.snapshot = { Snapshot(ids, 0, positionMs) }
        s.online = { networkOn && !yt }
    }
    private fun Snapshot(ids: List<Long>, i: Int, p: Long) = QueueSync.Snapshot(ids, i, p)

    @Test fun putIsDebouncedOneSecondAfterEdits() = runTest {
        val s = sync()
        ids = listOf(7, 9, 8); s.edited()
        scheduler.advance(0.9); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
        ids = listOf(7, 9, 8, 10); s.edited()
        scheduler.advance(0.9); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
        scheduler.advance(0.1); advanceUntilIdle()
        assertEquals(listOf(SaveCall(listOf(7, 9, 8, 10), 0, 0)), api.saveCalls)
    }

    @Test fun putEveryFifteenSecondsWhilePlaying() = runTest {
        val s = sync()
        s.edited(); s.playingTick()                      // playback starts: the first tick only starts the clock
        scheduler.advance(1.0); advanceUntilIdle()
        assertEquals(1, api.saveCalls.size)
        clock.advance(10.0); positionMs = 10_000; s.playingTick(); advanceUntilIdle()
        assertEquals(1, api.saveCalls.size)
        positionMs = 14_500; clock.advance(5.0); positionMs = 15_000; s.playingTick(); advanceUntilIdle()
        assertEquals(2, api.saveCalls.size)
        assertEquals(SaveCall(listOf(7, 8), 0, 15_000), api.saveCalls.last())
        // paused: no ticks, nothing goes out
        clock.advance(60.0); scheduler.advance(1.0); advanceUntilIdle()
        assertEquals(2, api.saveCalls.size)
    }

    @Test fun theFirstTickOnlyStartsTheClock() = runTest {
        val s = sync()
        s.playingTick(); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
    }

    @Test fun nothingToSendSendsNothing() = runTest {      // episodes are never sent: the snapshot has nothing
        val s = sync()
        s.snapshot = { null }
        s.edited(); scheduler.advance(5.0); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
    }

    /** A YouTube playlist played from search ("yt:" items) stays on the phone: the server keeps the last library queue. */
    @Test fun aQueueWithYouTubeTracksIsNotSent() = runTest {
        val s = sync()
        yt = true
        s.edited(); s.playingTick()
        clock.advance(30.0); s.playingTick()
        scheduler.advance(5.0); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
        yt = false                                          // a library queue again: sent
        s.edited(); scheduler.advance(1.0); advanceUntilIdle()
        assertEquals(listOf(SaveCall(listOf(7, 8), 0, 0)), api.saveCalls)
    }

    @Test fun aPushSkippedOfflineIsSentWhenTheNetworkIsBack() = runTest {
        val s = sync()
        networkOn = false
        s.edited(); scheduler.advance(1.0); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
        s.reconnected(); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())                 // still offline
        networkOn = true
        s.reconnected(); advanceUntilIdle()
        assertEquals(1, api.saveCalls.size)
        s.reconnected(); advanceUntilIdle()                 // nothing owed any more
        assertEquals(1, api.saveCalls.size)
    }

    @Test fun reconnectedWithNothingOwedSendsNothing() = runTest {
        sync().reconnected(); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
    }

    @Test fun theStoredQueuesDeviceNameIsReported() = runTest {
        val s = sync()
        val names = mutableListOf<String>()
        s.onOwnDevice = { names.add(it) }
        api.deviceName = "device:7"
        s.push(); advanceUntilIdle()
        assertEquals(listOf("device:7"), names)
    }

    @Test fun aFailedPutIsLeftForTheNextEdit() = runTest {
        val s = sync()
        val names = mutableListOf<String>()
        s.onOwnDevice = { names.add(it) }
        api.saveError = IOException("5xx")
        s.push(); advanceUntilIdle()
        assertEquals(1, api.saveCalls.size)
        assertTrue(names.isEmpty())
        api.saveError = null
        s.push(); advanceUntilIdle()
        assertEquals(listOf("device:1"), names)
    }

    @Test fun cancelStopsTheDebounceAndSilencesAnswersInFlight() = runTest {
        val s = sync()
        val names = mutableListOf<String>()
        s.onOwnDevice = { names.add(it) }
        s.edited(); s.cancel()
        scheduler.advance(5.0); advanceUntilIdle()
        assertTrue(api.saveCalls.isEmpty())
        s.push(); s.cancel(); advanceUntilIdle()            // sign-out while a PUT is in flight
        assertTrue(names.isEmpty())
        s.playingTick(); clock.advance(60.0); s.playingTick()   // the clock restarted: first tick only starts it
        advanceUntilIdle()
        assertEquals(1, api.saveCalls.size)   // only the push after the restart; the cancelled one never went out
    }

    // shouldAdopt

    private fun server(ids: List<Long>, by: String, at: Long) = ServerQueue(ids, 0, 0, 3, by, at)

    @Test fun anEmptyLocalQueueAdoptsTheServersWhateverItsAge() {
        assertTrue(QueueSync.shouldAdopt(server(listOf(4, 5), "device:2", 1), true, 1_000_000, null, false))
        assertTrue(QueueSync.shouldAdopt(server(listOf(4, 5), "device:1", 1), true, 1_000_000, "device:1", false))
    }

    @Test fun anEmptyServerQueueIsNeverAdopted() {
        assertFalse(QueueSync.shouldAdopt(server(emptyList(), "device:2", 99), true, 0, null, false))
    }

    @Test fun aNewerQueueFromAnotherDeviceIsAdoptedWhenNothingPlays() {
        assertTrue(QueueSync.shouldAdopt(server(listOf(4), "device:2", 106), false, 100, "device:1", false))
    }

    @Test fun theServerQueueIsNotAdoptedWhenNotNewerEnough() {
        assertFalse(QueueSync.shouldAdopt(server(listOf(4), "device:2", 105), false, 100, "device:1", false))
    }

    @Test fun ourOwnQueueOnTheServerIsNotAdopted() {
        assertFalse(QueueSync.shouldAdopt(server(listOf(4), "device:1", 160), false, 100, "device:1", false))
    }

    @Test fun whilePlayingTheServerQueueIsNeverAdopted() {
        assertFalse(QueueSync.shouldAdopt(server(listOf(4), "device:2", 700), false, 100, "device:1", true))
        assertFalse(QueueSync.shouldAdopt(server(listOf(4), "device:2", 700), true, 100, "device:1", true))
    }
}
