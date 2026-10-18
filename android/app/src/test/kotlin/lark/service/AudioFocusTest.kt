package lark.service

import android.content.Context
import android.media.AudioManager
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.test.core.app.ApplicationProvider
import lark.bridge.Item
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.ServiceRig
import lark.testing.focusListener
import lark.testing.q
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Port of the interruption cases of ios/Tests/AudioSessionTests.swift onto Android's audio focus: the app-wide player
 * (ExoPlayer with `handleAudioFocus`) and the engine, with the system's focus changes sent to the listener ExoPlayer
 * registered. A call or another app's transient claim pauses and its end resumes; a permanent loss pauses for good;
 * a pause by the user in between is not undone. Episodes are speech: they pause where music ducks.
 */
@UnstableApi
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
class AudioFocusTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var rig: ServiceRig
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val engine get() = rig.engine
    private val player get() = rig.player

    @Before fun setUp() { rig = ServiceRig(context, tmp.newFolder("rig")) }
    @After fun tearDown() { rig.release() }

    private fun focus(change: Int) = focusListener(context).onAudioFocusChange(change)

    /** Launch builds the session but never takes focus: opening Melarka to browse never stops another app's audio. */
    @Test fun creatingTheServiceTakesNoFocus() {
        engine.handle(q(listOf(3), 0, 0, false))            // a restored queue, not playing
        val service = rig.startService()
        rig.settle()
        assertNull(shadowOf(context.getSystemService(AudioManager::class.java)).lastAudioFocusRequest)
        service.destroy()
    }

    @Test fun transientLossPausesAndItsEndResumes() {
        rig.playTrack(1)
        focus(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        rig.waitUntil { !engine.playing }
        assertTrue(engine.pausedBySystem)
        assertFalse(player.isPlaying)
        focus(AudioManager.AUDIOFOCUS_GAIN)
        rig.waitUntil { engine.playing && player.isPlaying }
        assertEquals("1", engine.current?.id)
    }

    @Test fun permanentLossPausesAndStaysPaused() {
        rig.playTrack(1)
        focus(AudioManager.AUDIOFOCUS_LOSS)
        rig.waitUntil { !engine.playing }
        assertFalse(player.playWhenReady)
        focus(AudioManager.AUDIOFOCUS_GAIN)
        rig.settle()
        assertFalse(engine.playing)
        assertFalse(player.isPlaying)
    }

    /** The user paused during the interruption (the lock screen): its end must not start playback. */
    @Test fun aUserPauseDuringTheLossIsNotUndone() {
        rig.playTrack(1)
        focus(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        rig.waitUntil { !engine.playing }
        engine.pause()
        focus(AudioManager.AUDIOFOCUS_GAIN)
        rig.settle()
        assertFalse(engine.playing)
        assertFalse(player.isPlaying)
    }

    /** Not playing when the interruption began (paused by the user): nothing resumes. */
    @Test fun aLossWhilePausedDoesNotResume() {
        rig.playTrack(1)
        engine.pause()
        focus(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        focus(AudioManager.AUDIOFOCUS_GAIN)
        rig.settle()
        assertFalse(engine.playing)
        assertFalse(player.isPlaying)
    }

    /** A navigation prompt over music: music ducks and plays on. */
    @Test fun musicDucks() {
        rig.playTrack(1)
        focus(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT_CAN_DUCK)
        rig.settle()
        assertTrue(engine.playing)
        assertTrue(player.isPlaying)
    }

    /** The same prompt over an episode: speech pauses (nothing is missed) and goes on after it. */
    @Test fun anEpisodePausesInsteadOfDucking() {
        rig.playEpisode()
        focus(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT_CAN_DUCK)
        rig.waitUntil { !engine.playing }
        focus(AudioManager.AUDIOFOCUS_GAIN)
        rig.waitUntil { engine.playing && player.isPlaying }
    }

    @Test fun theAudioAttributesFollowTheActiveKind() {
        assertEquals(C.AUDIO_CONTENT_TYPE_MUSIC, player.audioAttributes.contentType)
        rig.playEpisode()
        assertEquals(C.USAGE_MEDIA, player.audioAttributes.usage)
        assertEquals(C.AUDIO_CONTENT_TYPE_SPEECH, player.audioAttributes.contentType)
        engine.handle(WebMessage.Stop(Item.Kind.episode))
        assertEquals(C.AUDIO_CONTENT_TYPE_MUSIC, player.audioAttributes.contentType)
        rig.playTrack(1)
        assertEquals(C.USAGE_MEDIA, player.audioAttributes.usage)
        assertEquals(C.AUDIO_CONTENT_TYPE_MUSIC, player.audioAttributes.contentType)
        // Focus is still handled by the player after a change.
        focus(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        rig.waitUntil { !engine.playing }
    }
}

/**
 * The engine side of ios/Tests/AudioSessionTests.swift's activation cases: `willStartPlayback` (where iOS activates its
 * audio session; on Android the media service is brought up there) runs once, synchronously, right before sound
 * starts, and never for a restored queue that is not played.
 */
class PlaybackStartTest : EngineTestCase() {
    private val starts = mutableListOf<Pair<Int, Int>>()     // (loads, calls) before each start

    private fun watch() { engine.willStartPlayback = { starts.add(backend.loads.size to backend.calls.size) } }

    private fun playing() {
        watch()
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        assertTrue(engine.playing)
        assertEquals(1, starts.size)
        assertEquals(0, starts[0].first)                       // before the load
        starts.clear()
    }

    @Test fun aRestoredQueueWithoutPlayDoesNotStart() = engineTest {
        makeEngine()
        watch()
        engine.handle(WebMessage.Hello("none"))
        engine.handle(q(listOf(3), 0, 0, false))
        assertTrue(starts.isEmpty())
    }

    @Test fun playStartsOnceBeforeThePlayer() = engineTest {
        playing()
        engine.pause()
        val calls = backend.calls.size
        engine.handle(WebMessage.Play(Item.Kind.track))
        assertEquals(1, starts.size)
        assertEquals(calls, starts[0].second)                  // before backend.play
        assertEquals("play", backend.calls.last())
    }

    @Test fun nextAndANetworkRetryStart() = engineTest {
        playing()
        engine.next()
        assertEquals(1, starts.size)
        backend.fail(network = true)
        scheduler.advance(2.0)                                  // the retry's load
        assertEquals(2, starts.size)
    }

    /** ExoPlayer reports the pause first (its own focus handling) and resumes by itself: the engine follows both. */
    @Test fun thePlayersOwnPauseAndResumeAreFollowed() = engineTest {
        playing()
        backend.interrupt()
        assertFalse(engine.playing)
        assertTrue(engine.pausedBySystem)
        backend.start()
        assertTrue(engine.playing)
    }
}
