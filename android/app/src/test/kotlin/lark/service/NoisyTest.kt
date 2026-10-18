package lark.service

import android.content.Context
import android.content.Intent
import android.media.AudioManager
import androidx.media3.common.util.UnstableApi
import androidx.test.core.app.ApplicationProvider
import lark.player.Media3Backend
import lark.player.MediaSource
import lark.testing.BackendRig
import lark.testing.EngineTestCase
import lark.testing.ServiceRig
import lark.testing.focusListener
import lark.testing.q
import lark.testing.silentWav
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Port of the route-change cases of ios/Tests/AudioSessionTests.swift: the output going away (headphones unplugged, a
 * Bluetooth device disconnecting: Android's "audio becoming noisy") pauses as a user pause, so nothing brings it back
 * by itself, not even the end of an interruption. Other audio changes keep playing.
 */
@UnstableApi
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
class NoisyTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var rig: ServiceRig
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val engine get() = rig.engine

    @Before fun setUp() { rig = ServiceRig(context, tmp.newFolder("rig")) }
    @After fun tearDown() { rig.release() }

    private fun unplug() = broadcast(context, Intent(AudioManager.ACTION_AUDIO_BECOMING_NOISY))

    @Test fun headphonesOutPausesAndStaysPaused() {
        rig.playTrack(1)
        unplug()
        rig.waitUntil { !engine.playing }
        assertFalse(engine.pausedBySystem)                     // a later end of an interruption does not bring it back
        assertFalse(rig.player.playWhenReady)
        focusListener(context).onAudioFocusChange(AudioManager.AUDIOFOCUS_GAIN)
        rig.settle()
        assertFalse(engine.playing)
        assertFalse(rig.player.isPlaying)
    }

    /** Unplugged while a call holds the focus: the end of the call does not resume either. */
    @Test fun headphonesOutDuringAnInterruptionStaysPaused() {
        rig.playTrack(1)
        focusListener(context).onAudioFocusChange(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        rig.waitUntil { !engine.playing }
        assertTrue(engine.pausedBySystem)
        unplug()
        rig.waitUntil { !rig.player.playWhenReady }
        assertFalse(engine.pausedBySystem)
        focusListener(context).onAudioFocusChange(AudioManager.AUDIOFOCUS_GAIN)
        rig.settle()
        assertFalse(engine.playing)
        assertFalse(rig.player.isPlaying)
    }

    @Test fun otherAudioChangesKeepPlaying() {
        rig.playTrack(1)
        broadcast(context, Intent(AudioManager.ACTION_HEADSET_PLUG).putExtra("state", 1))
        broadcast(context, Intent(AudioManager.ACTION_SCO_AUDIO_STATE_UPDATED))
        rig.settle()
        assertTrue(engine.playing)
        assertTrue(rig.player.isPlaying)
    }
}

/** The backend tells the engine a lost output apart from any other pause. */
@UnstableApi
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
class NoisyBackendTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var rig: BackendRig
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Before fun setUp() { rig = BackendRig(context).also { Media3Backend.configure(it.player) } }
    @After fun tearDown() { rig.release() }

    @Test fun becomingNoisyIsAPauseForGood() {
        rig.backend.load(MediaSource.File(silentWav(tmp.root, 10.0, 8000)), 0, true, 1f, 1f, 1)
        rig.waitUntil { rig.delegate.started }
        broadcast(context, Intent(AudioManager.ACTION_AUDIO_BECOMING_NOISY))
        rig.waitUntil { rig.delegate.has("pausedForGood:1") }
        assertFalse(rig.delegate.events.toString(), rig.delegate.events.any { it.startsWith("paused:") })
    }

    /** Another app took the focus for good (iOS: an interruption that ends without `.shouldResume`): nothing resumes it. */
    @Test fun aPermanentFocusLossIsAPauseForGood() {
        rig.backend.load(MediaSource.File(silentWav(tmp.root, 10.0, 8000)), 0, true, 1f, 1f, 1)
        rig.waitUntil { rig.delegate.started }
        focusListener(context).onAudioFocusChange(AudioManager.AUDIOFOCUS_LOSS)
        rig.waitUntil { rig.delegate.has("pausedForGood:1") }
        assertFalse(rig.delegate.events.toString(), rig.delegate.events.any { it.startsWith("paused:") })
    }
}

/** The engine side: a pause for good (a lost output, a permanent focus loss) is the user's pause (iOS `routeChange(.oldDeviceUnavailable)`). */
class RouteLossEngineTest : EngineTestCase() {
    @Test fun headphonesOutPauses() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        backend.pausedForGood()
        assertFalse(engine.playing)
        assertFalse(engine.pausedBySystem)
        assertEquals("pause", backend.calls.last())
    }

    @Test fun aRouteLossAfterASystemPauseMakesItTheUsers() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        backend.interrupt()
        assertTrue(engine.pausedBySystem)
        backend.pausedForGood()
        assertFalse(engine.pausedBySystem)
    }

    @Test fun aRouteLossWhilePausedByTheUserChangesNothing() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        engine.pause()
        val events = sent.size
        backend.pausedForGood()
        assertEquals(events, sent.size)
    }

    @Test fun aLateRouteLossOfTheOldItemIsIgnored() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        val old = backend.generation
        backend.start(); engine.next(); backend.start()
        backend.pausedForGood(generation = old)
        assertTrue(engine.playing)
    }
}

/**
 * Sends a broadcast without Robolectric's line on stderr ("Intent = …; Matching wrappers: …", printed unconditionally
 * while it looks for receivers), so test output stays clean.
 */
internal fun broadcast(context: Context, intent: Intent) {
    val err = System.err
    System.setErr(java.io.PrintStream(java.io.OutputStream.nullOutputStream()))
    try { context.sendBroadcast(intent) } finally { System.setErr(err) }
}
