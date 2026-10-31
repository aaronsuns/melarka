package lark.service

import android.view.KeyEvent
import lark.player.PlayerEngine
import lark.testing.EngineTestCase
import lark.testing.q
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/RemoteScanTests.swift: holding a car's or headphones' fast-forward or rewind moves the position while
 * it is held, faster after a while, and stops at the item's edges, on release, or on any other command. Here the hold
 * arrives as key down … key up ([PlaybackService.scanKey]).
 */
class RemoteScanTest : EngineTestCase() {
    private lateinit var commands: RemoteCommands
    private val tick = RemoteCommands.SCAN_TICK_S

    private fun start() {
        commands = RemoteCommands(scope, engine, scheduler)
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.durationMs = 200_000
    }

    @After fun tearDown() { if (::commands.isInitialized) commands.detach() }

    private fun key(action: Int, code: Int, repeat: Int = 0) = KeyEvent(0L, 0L, action, code, repeat)

    @Test fun holdingForwardStepsThenSpeedsUpUntilReleased() = engineTest {
        start()
        assertEquals(RemoteCommands.Status.success, commands.beginScan(true))
        assertEquals(5000L, backend.positionMs)                           // a step at once
        scheduler.advance(tick)
        assertEquals(10_000L, backend.positionMs)
        scheduler.advance(tick * 4)                                       // steps 0…5 are the slow ones
        assertEquals(30_000L, backend.positionMs)
        scheduler.advance(tick)
        assertEquals(45_000L, backend.positionMs)                         // then 15 s a step
        commands.endScan()
        scheduler.advance(5.0)
        assertEquals(45_000L, backend.positionMs)
        assertFalse(commands.isScanning)
        assertTrue(engine.playing)                                        // playing stays as it was
    }

    @Test fun rewindStopsAtTheStartWithoutGoingBack() = engineTest {
        start()
        engine.seek(7000)
        val loads = backend.loads.size
        assertEquals(RemoteCommands.Status.success, commands.beginScan(false))
        assertEquals(2000L, backend.positionMs)
        scheduler.advance(tick)
        assertEquals(0L, backend.positionMs)
        assertFalse(commands.isScanning)
        scheduler.advance(5.0)
        assertEquals(loads, backend.loads.size)                           // still the same item
        assertEquals("1", engine.current?.id)
    }

    @Test fun forwardStopsJustBeforeTheEnd() = engineTest {
        start()
        engine.seek(196_000)
        assertEquals(RemoteCommands.Status.success, commands.beginScan(true))
        assertEquals(200_000L - PlayerEngine.SCAN_END_MARGIN_MS, backend.positionMs)
        assertFalse(commands.isScanning)
        assertEquals("1", engine.current?.id)                             // the item finishes by itself
    }

    @Test fun anotherCommandEndsTheScan() = engineTest {
        start()
        commands.beginScan(true)
        assertEquals(RemoteCommands.Status.success, commands.pauseNow())
        assertFalse(commands.isScanning)
        val at = backend.positionMs
        scheduler.advance(5.0)
        assertEquals(at, backend.positionMs)
    }

    @Test fun aLostReleaseEndsTheScanByItself() = engineTest {
        start()
        backend.durationMs = 10_000_000
        commands.beginScan(true)
        scheduler.advance(tick * (RemoteCommands.SCAN_MAX_STEPS + 10))
        assertFalse(commands.isScanning)
        val fast = RemoteCommands.SCAN_MAX_STEPS - RemoteCommands.SCAN_FAST_AFTER
        assertEquals(RemoteCommands.SCAN_FAST_AFTER * RemoteCommands.SCAN_STEP_MS + fast * RemoteCommands.SCAN_FAST_STEP_MS,
            backend.positionMs)
    }

    @Test fun nothingPlayingIsNotActionable() = engineTest {
        commands = RemoteCommands(scope, scheduler) { null }
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, commands.beginScan(true))
        assertFalse(commands.isScanning)
    }

    @Test fun fastForwardAndRewindKeysScanWhileHeld() = engineTest {
        start()
        assertTrue(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD)))
        assertEquals(5000L, backend.positionMs)
        // Repeats while held don't restart it.
        assertTrue(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD, 1)))
        assertEquals(5000L, backend.positionMs)
        scheduler.advance(tick)
        assertEquals(10_000L, backend.positionMs)
        assertTrue(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD)))
        assertFalse(commands.isScanning)
        assertTrue(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_REWIND)))
        assertEquals(5000L, backend.positionMs)
        assertTrue(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_MEDIA_REWIND)))
        assertFalse(commands.isScanning)
    }

    @Test fun otherKeysAreLeftToMedia3() = engineTest {
        start()
        assertFalse(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_NEXT)))
        assertFalse(PlaybackService.scanKey(commands, key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE)))
        assertFalse(commands.isScanning)
    }
}
