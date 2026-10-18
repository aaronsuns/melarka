package lark.player

import lark.bridge.NativeEvent
import lark.testing.EngineTestCase
import lark.testing.q
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What the app services need of the engine's life: a dropped engine stays inert ([PlayerEngine.detach]), and a start
 * the app may not make in the background waits for the user ([PlayerEngine.canAutoStart], [PlayerEngine.resumeDeferred]).
 */
class EngineLifecycleTest : EngineTestCase() {
    /** A stream that breaks offline with nothing on the phone: stopped, waiting for the network. */
    private fun stopForTheNetwork() {
        engine.handle(q(listOf(5), 0, 0, true))
        backend.start()
        backend.playTo(10_000)
        network.isOnline = false
        backend.fail(network = true)
        assertFalse(engine.playing)
    }

    @Test fun aDetachedEngineIgnoresTheNetwork() = engineTest {
        stopForTheNetwork()
        val loads = backend.loads.size
        engine.detach()
        network.isOnline = true
        idle()
        assertEquals(loads, backend.loads.size)
        assertFalse(engine.playing)
    }

    @Test fun aResumeTheAppMayNotStartNowWaitsPausedBySystem() = engineTest {
        var allowed = false
        engine.canAutoStart = { allowed }
        var activations = 0
        engine.willStartPlayback = { activations++ }
        stopForTheNetwork()
        val loads = backend.loads.size
        activations = 0
        network.isOnline = true
        assertEquals(loads, backend.loads.size)
        assertEquals(0, activations)                           // no service start was even tried
        assertTrue(engine.pausedBySystem)
        val state = states.last()
        assertFalse(state.playing)
        assertNull(state.error)                                // not "offline" any more: paused, ready to go on
        allowed = true
        engine.resumeDeferred()
        assertEquals(remote(5), backend.loads.last().src)
        assertEquals(10_000L, backend.loads.last().startMs)
        assertTrue(engine.playing)
        assertFalse(engine.pausedBySystem)
        assertEquals(1, activations)
        engine.resumeDeferred()                                // once
        assertEquals(loads + 1, backend.loads.size)
    }

    @Test fun thePlayButtonTakesAWaitingResumeOver() = engineTest {
        engine.canAutoStart = { false }
        stopForTheNetwork()
        network.isOnline = true
        val loads = backend.loads.size
        engine.play()
        assertEquals(loads + 1, backend.loads.size)
        engine.resumeDeferred()
        assertEquals(loads + 1, backend.loads.size)            // nothing more
    }

    @Test fun aResetDropsAWaitingResume() = engineTest {
        engine.canAutoStart = { false }
        stopForTheNetwork()
        network.isOnline = true
        engine.reset()
        val loads = backend.loads.size
        engine.resumeDeferred()
        assertEquals(loads, backend.loads.size)
        assertFalse(sent.filterIsInstance<NativeEvent.State>().last().playing)
    }
}
