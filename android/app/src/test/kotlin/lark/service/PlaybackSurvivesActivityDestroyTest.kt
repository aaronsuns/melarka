package lark.service

import android.app.Application
import android.content.Context
import android.content.Intent
import androidx.media3.common.util.UnstableApi
import androidx.test.core.app.ApplicationProvider
import lark.app.MainActivity
import lark.testing.ServiceRig
import lark.testing.q
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * The media service owns playback, not the Activity: a song keeps playing when the Activity is destroyed or the task
 * is swiped away, and the app-wide engine and player outlive the service too.
 */
@UnstableApi
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
class PlaybackSurvivesActivityDestroyTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var rig: ServiceRig
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val engine get() = rig.engine

    @Before fun setUp() { rig = ServiceRig(context, tmp.newFolder("rig")) }
    @After fun tearDown() { rig.release() }

    @Test fun destroyingTheActivityKeepsPlaying() {
        val activity = Robolectric.buildActivity(MainActivity::class.java).setup()
        val service = rig.startService()
        rig.playTrack(1)
        rig.waitUntil { shadowOf(service.get()).lastForegroundNotification != null }   // the service is in the foreground
        activity.pause().stop().destroy()
        rig.settle()
        assertTrue(engine.playing)
        assertTrue(rig.player.isPlaying)
        assertFalse(shadowOf(service.get()).isStoppedBySelf)
        assertFalse(shadowOf(service.get()).isForegroundStopped)
        val session = service.get().session!!
        assertTrue(session.player.isPlaying)
        assertEquals("T1", session.player.mediaMetadata.title.toString())
        service.destroy()
    }

    /** Playback the page starts brings the service up, once, before the sound: it is never playing without it. */
    @Test fun playingStartsTheService() {
        val app = context as Application
        rig.engine.handle(q(listOf(1), 0, 0, false))
        assertNull(shadowOf(app).nextStartedService)            // a queue that is not played starts nothing
        rig.playTrack(1)
        assertEquals(PlaybackService::class.java.name, shadowOf(app).nextStartedService?.component?.className)
        val service = rig.startService()
        rig.settle()
        while (shadowOf(app).nextStartedService != null) Unit   // Media3's own start for the foreground
        PlaybackService.start(app)
        assertNull(shadowOf(app).nextStartedService)            // running: not asked again
        service.destroy()
    }

    /** Swiped away from the recent apps while playing: the service stays, the music plays on. */
    @Test fun swipingTheTaskAwayWhilePlayingKeepsTheService() {
        val service = rig.startService()
        rig.playTrack(1)
        rig.waitUntil { shadowOf(service.get()).lastForegroundNotification != null }
        service.get().onTaskRemoved(Intent(context, MainActivity::class.java))
        rig.settle()
        assertFalse(shadowOf(service.get()).isStoppedBySelf)
        assertTrue(engine.playing)
        assertTrue(rig.player.isPlaying)
        service.destroy()
    }

    /** Swiped away while paused: the service ends; the engine and its player are the app's and stay usable. */
    @Test fun theServiceEndingLeavesTheAppWidePlayer() {
        val service = rig.startService()
        rig.playTrack(1)
        engine.pause()
        rig.settle()
        service.get().onTaskRemoved(Intent(context, MainActivity::class.java))
        rig.settle()
        assertTrue(shadowOf(service.get()).isStoppedBySelf)
        service.destroy()
        engine.play()
        rig.waitUntil { rig.player.isPlaying }
        assertTrue(engine.playing)
        assertNotNull(engine.current)
        // A new service (the next play from the car) shows the same engine.
        val again = rig.startService()
        assertEquals("T1", again.get().session!!.player.mediaMetadata.title.toString())
        again.destroy()
    }
}
