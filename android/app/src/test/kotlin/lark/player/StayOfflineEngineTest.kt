package lark.player

import lark.testing.EngineTestCase
import lark.testing.q
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * iOS `StayOfflineTests`, the engine's part: offline (the Stay Offline switch or no network) it sends nothing, and
 * what waited goes out once the network is back. (The switch itself is `StayOfflineNetworkTest`; the page is
 * `AppServicesTest`.)
 */
class StayOfflineEngineTest : EngineTestCase() {
    @Test fun theQueueIsNotSentOfflineAndGoesOutWhenTheNetworkIsBack() = engineTest {
        cache.local[7] = tmp("7.m4a"); cache.local[8] = tmp("8.m4a")
        network.isOnline = false
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.start()
        scheduler.advance(1.0); idle()
        advanceClock(60.0); backend.playTo(60_000); engine.tick(); idle()
        assertEquals(0, api.saveCalls.size)
        network.isOnline = true
        idle()
        assertEquals(1, api.saveCalls.size)                   // once, the queue as it is now
        assertEquals(listOf(7L, 8L), api.saveCalls.last().trackIds)
    }

    @Test fun playEventsWaitOfflineAndAreSentOnline() = engineTest {
        network.isOnline = false
        events.add(trackId = 7, startedAt = 1, playedSeconds = 60, skipped = false, quality = "high")
        idle()
        advanceClock(60.0); engine.tick(); idle()
        advanceClock(60.0); engine.tick(); idle()
        assertEquals(0, api.postedEvents.size)
        network.isOnline = true
        advanceClock(60.0); engine.tick(); idle()
        assertEquals(1, api.postedEvents.size)
    }
}
