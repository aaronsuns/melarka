package lark.core

import lark.testing.FakeNetwork
import lark.testing.FakeScheduler
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Stay Offline (a flight): offline whatever the phone's network says, nothing tried until it is turned off. */
class StayOfflineNetworkTest {
    @Test fun theSwitchIsOfflineWhateverThePathSays() {
        val path = FakeNetwork().apply { answered = false }
        val net = SwitchableNetworkStatus(path)
        var changes = 0
        net.observe { changes += 1 }
        assertTrue(net.isOnline)
        assertFalse(net.answered)
        net.stayOffline = true
        assertFalse(net.isOnline)
        assertTrue(net.answered)                       // a cold launch waits for nothing
        assertEquals(1, changes)
        path.isOnline = false; path.isOnline = true    // the plane's Wi-Fi comes and goes: still offline
        assertFalse(net.isOnline)
        assertEquals(3, changes)                       // the path's changes are passed on too
        net.stayOffline = false
        assertTrue(net.isOnline)
        assertEquals(4, changes)
    }

    @Test fun settingTheSwitchToItsCurrentValueIsNotAChange() {
        val net = SwitchableNetworkStatus(FakeNetwork())
        var changes = 0
        net.observe { changes += 1 }
        net.stayOffline = false
        assertEquals(0, changes)
    }

    @Test fun expensiveIsThePathsOwn() {
        val path = FakeNetwork()
        val net = SwitchableNetworkStatus(path, stayOffline = true)
        path.answer(online = true, expensive = true)
        assertTrue(net.isExpensive)
    }

    @Test fun fakeNetworkAnswerNotifiesOnceAndSetsAnswered() {
        val n = FakeNetwork().apply { answered = false }
        var calls = 0
        n.observe { calls += 1 }
        n.answer(online = false, expensive = true)
        assertTrue(n.answered); assertFalse(n.isOnline); assertTrue(n.isExpensive)
        assertEquals(1, calls)
    }

    @Test fun fakeSchedulerRunsJobsInTimeOrderAndHonoursCancel() {
        val s = FakeScheduler()
        val log = mutableListOf<String>()
        s.after(2.0) { log += "b" }
        s.after(1.0) { log += "a"; s.after(0.5) { log += "a2" } }
        val c = s.after(1.0) { log += "x" }
        c.cancel()
        s.advance(1.0); assertEquals(listOf("a"), log)
        s.advance(1.0); assertEquals(listOf("a", "a2", "b"), log)
        assertEquals(0, s.pending)
    }

    @Test fun fakeSchedulerEveryRepeatsUntilCancelled() {
        val s = FakeScheduler()
        var n = 0
        val c = s.every(1.0) { n += 1 }
        s.advance(3.5); assertEquals(3, n)
        c.cancel()
        s.advance(5.0); assertEquals(3, n)
    }
}
