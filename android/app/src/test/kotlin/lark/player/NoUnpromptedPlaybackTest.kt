package lark.player

import lark.api.RandomFavorites
import lark.service.RemoteCommands
import lark.testing.EngineTestCase
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/NoUnpromptedPlaybackTests.swift: music never starts on a phone nobody touched. Sound may start by
 * itself only right after the user's own playback was cut off ([PlayerEngine.AUTO_RESUME_WINDOW]); a call or meeting
 * that ends, or the network that comes back, later than that leaves it stopped until the user starts it.
 */
class NoUnpromptedPlaybackTest : EngineTestCase() {
    /** Well past [PlayerEngine.AUTO_RESUME_WINDOW]. */
    private val anHourLater = 3600.0

    private fun assertSilent(what: String) {
        assertFalse("playing after $what", engine.playing)
        assertFalse("backend playing after $what", backend.isPlaying)
        assertFalse("a load with autoplay after $what", backend.loads.any { it.autoplay })
        assertFalse("backend.play after $what", backend.calls.contains("play"))
    }

    private fun clearBackendLog() { backend.loads.clear(); backend.calls.clear() }

    // MARK: The end of a long interruption (ExoPlayer resumes by itself when the focus comes back)

    @Test fun theFocusComingBackAfterALongCallDoesNotResume() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        backend.interrupt()                                   // a call: a transient focus loss
        assertTrue(engine.pausedBySystem)
        advanceClock(anHourLater)
        backend.start()                                       // the call ended: ExoPlayer resumed on its own
        assertFalse(engine.playing)
        assertEquals("pause", backend.calls.last())
        assertFalse(engine.pausedBySystem)
    }

    @Test fun aSystemPauseOutlastingTheWindowBecomesAUserPause() = engineTest {
        engine.handle(q(listOf(1), 0, 0, true))
        backend.start()
        backend.interrupt()
        advanceClock(anHourLater)
        scheduler.advance(anHourLater)
        assertFalse(engine.pausedBySystem)
        assertEquals("pause", backend.calls.last())          // playWhenReady off: the focus returning does nothing
        assertFalse(engine.playing)
    }

    @Test fun aShortCallStillResumesAndLaterSongsPlayOn() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        backend.interrupt()
        advanceClock(60.0)
        backend.start()                                       // a one-minute call: resumes, as on every music app
        assertTrue(engine.playing)
        assertFalse(engine.pausedBySystem)
        advanceClock(anHourLater)
        backend.finish()                                      // an hour later the next song starts as usual
        backend.start()
        assertTrue(engine.playing)
        assertEquals("2", engine.current?.id)
    }

    // MARK: The network coming back long after a stop

    @Test fun anInterruptedStreamDoesNotResumeWhenTheNetworkReturnsMuchLater() = engineTest {
        engine.handle(q(listOf(5), 0, 0, true))
        backend.start()
        network.isOnline = false
        backend.fail(network = true)
        assertFalse(engine.playing)
        clearBackendLog()
        advanceClock(anHourLater)
        network.isOnline = true
        idle()
        assertSilent("the network returning an hour after a broken stream")
    }

    @Test fun aResumeHeldForTheUserIsDroppedOnceTooLate() = engineTest {
        engine.canAutoStart = { false }                       // in the background, no media service
        engine.handle(q(listOf(5), 0, 0, true))
        backend.start()
        network.isOnline = false
        backend.fail(network = true)
        network.isOnline = true                               // held for the user
        clearBackendLog()
        advanceClock(anHourLater)
        engine.canAutoStart = { true }
        engine.resumeDeferred()                               // the app opened an hour later
        assertSilent("opening the app an hour after the network came back")
        assertFalse(engine.pausedBySystem)
    }

    @Test fun aColdStartWithNothingCachedDoesNotShuffleWhenTheNetworkReturnsMuchLater() = engineTest {
        network.isOnline = false
        makeEngine()
        RemoteCommands.handlePlay(engine, this)
        idle()
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(4)))
        clearBackendLog()
        advanceClock(anHourLater)
        network.isOnline = true
        idle()
        assertSilent("the network returning an hour after a cold start offline")
        assertTrue(api.randomFavoritesCalls.isEmpty())
    }
}
