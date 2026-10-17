package lark.player

import lark.api.LarkError
import lark.api.RandomFavorites
import lark.bridge.Item
import lark.bridge.NativeEvent
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.SleepRequest
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.episode
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/SleepTimerTests.swift: the sleep timer runs in the engine, on its scheduler, so it keeps
 * time with the screen off. At the deadline the volume fades to 0 over 10 s, playback pauses and the volume is
 * back at 1 for the next play. "End of this track" fades over the item's last 10 s and stops at its end.
 */
class SleepTimerTest : EngineTestCase() {
    private fun lastState(k: Item.Kind) = states.lastOrNull { it.kind == k }

    private fun playMusic(ids: List<Int> = listOf(1, 2, 3)) {
        engine.handle(q(ids, 0, 0, true))
        backend.start()
    }

    private fun sleep(r: SleepRequest) = engine.handle(WebMessage.SleepTimer(r))

    @Test fun minutesFadesThenPausesAndRestoresVolume() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        scheduler.advance(15 * 60 - 10.0)
        assertTrue(engine.playing)
        assertTrue("no fade before the last 10 s: ${backend.volumes}", backend.volumes.all { it == 1f })
        scheduler.advance(5.0)
        assertTrue(backend.volumes.last() < 1f)
        assertTrue(backend.volumes.last() > 0f)
        assertTrue(engine.playing)
        scheduler.advance(5.0)
        assertFalse(engine.playing)
        assertTrue(backend.calls.contains("pause"))
        assertEquals(1f, backend.volumes.last())
        assertTrue("the fade reaches silence before the pause", backend.volumes.contains(0f))
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
        assertNull(lastState(Item.Kind.episode)?.sleepRemainingMs)
    }

    /** The fade is 40 steps of 0.25 s, each quieter than the one before, and the pause comes after silence. */
    @Test fun fadeStepsGoDown() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        scheduler.advance(15 * 60.0)
        val fade = backend.volumes.dropLast(1)                  // the last one is the restore
        assertEquals(40, fade.size)
        assertEquals(39f / 40, fade.first(), 0.0001f)
        assertEquals(0f, fade.last())
        assertEquals(fade.sortedDescending(), fade)
        assertTrue(backend.calls.lastIndexOf("pause") >= 0)
    }

    @Test fun cancelDuringFadeRestoresVolumeAndKeepsPlaying() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        scheduler.advance(15 * 60 - 5.0)
        assertTrue(backend.volumes.last() < 1f)
        sleep(SleepRequest.Cancel)
        assertEquals(1f, backend.volumes.last())
        val volumes = backend.volumes.size
        scheduler.advance(60.0)
        assertTrue(engine.playing)
        assertFalse(backend.calls.contains("pause"))
        assertEquals("no step of the old fade runs after a cancel", volumes, backend.volumes.size)
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
    }

    /** A new choice replaces the one set: only one timer runs. */
    @Test fun aNewChoiceReplacesTheOldOne() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        sleep(SleepRequest.Minutes(30))
        scheduler.advance(15 * 60 + 1.0)
        assertTrue(engine.playing)
        assertTrue(backend.volumes.all { it == 1f })
        scheduler.advance(15 * 60.0)
        assertFalse(engine.playing)
    }

    @Test fun stateReportsRemaining() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        assertEquals(900_000L, lastState(Item.Kind.track)?.sleepRemainingMs)
        assertEquals(900_000L, lastState(Item.Kind.episode)?.sleepRemainingMs)          // engine-wide: both kinds say it
        advanceClock(60.0); scheduler.advance(60.0)
        backend.playTo(1_000)                                                 // a tick sends a state
        assertEquals(840_000L, lastState(Item.Kind.track)?.sleepRemainingMs)
    }

    @Test fun noTimerSaysNull() = engineTest {
        playMusic()
        val state = lastState(Item.Kind.track)!!
        assertNull(state.sleepRemainingMs)
        assertTrue("an explicit null: the web tells a new app from an old one by the key",
            (state as NativeEvent).toJson().endsWith(""""sleepRemainingMs":null}"""))
    }

    /** The minutes timer runs whether or not anything plays: paused at the deadline, it stays paused. */
    @Test fun deadlineWhilePausedStaysPaused() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        engine.pause()
        scheduler.advance(15 * 60.0)
        assertFalse(engine.playing)
        assertEquals(1f, backend.volumes.last())
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
        engine.play()
        assertTrue(engine.playing)
    }

    // MARK: - End of this track

    @Test fun endOfTrackStopsAtBoundaryWithNextLoadedPaused() = engineTest {
        playMusic()
        assertNotNull(backend.preloads.last())
        sleep(SleepRequest.EndOfTrack)
        assertNull("nothing preloaded: the player must not run into the next item", backend.preloads.last())
        assertEquals(200_000L, lastState(Item.Kind.track)?.sleepRemainingMs)
        backend.playTo(189_000)
        assertTrue(backend.volumes.all { it == 1f })
        backend.playTo(195_000)
        assertEquals(0.5f, backend.volumes.last(), 0.001f)
        assertEquals(5_000L, lastState(Item.Kind.track)?.sleepRemainingMs)
        val loads = backend.loads.size
        backend.finish()
        assertEquals(loads + 1, backend.loads.size)
        assertEquals("2", engine.current?.id)
        assertEquals("the next track is loaded, not played", false, backend.loads.last().autoplay)
        assertFalse(engine.playing)
        assertEquals(1f, backend.volumes.last())
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
        assertNotNull("disarmed: the next item is preloaded again", backend.preloads.last())
        engine.play()
        assertTrue(engine.playing)
    }

    /** Repeat one would replay the track: the sleep timer wins. */
    @Test fun endOfTrackBeatsRepeatOne() = engineTest {
        engine.handle(WebMessage.SetModes(false, RepeatMode.one))
        playMusic()
        sleep(SleepRequest.EndOfTrack)
        assertNull(backend.preloads.last())
        backend.finish()
        assertFalse(engine.playing)
        assertEquals(false, backend.loads.last().autoplay)
    }

    /** Skipping to another track carries the timer over to it, at full volume again. */
    @Test fun endOfTrackFollowsASkip() = engineTest {
        playMusic()
        sleep(SleepRequest.EndOfTrack)
        backend.playTo(195_000)
        assertTrue(backend.volumes.last() < 1f)
        engine.next()
        assertEquals(1f, backend.volumes.last())
        assertNull(backend.preloads.last())
        assertEquals(200_000L, lastState(Item.Kind.track)?.sleepRemainingMs)
        backend.start(); backend.finish()
        assertFalse(engine.playing)
        assertEquals("3", engine.current?.id)
    }

    @Test fun cancelEndOfTrackPreloadsAgain() = engineTest {
        playMusic()
        sleep(SleepRequest.EndOfTrack)
        backend.playTo(195_000)
        sleep(SleepRequest.Cancel)
        assertEquals(1f, backend.volumes.last())
        assertNotNull(backend.preloads.last())
        backend.finish()
        assertTrue(engine.playing)
        assertEquals(true, backend.loads.last().autoplay)
    }

    /** The last track of a list: it simply stops, as it would anyway, and the volume is back. */
    @Test fun endOfTrackOnTheLastTrack() = engineTest {
        playMusic(listOf(1))
        sleep(SleepRequest.EndOfTrack)
        backend.playTo(199_500)
        backend.finish()
        assertFalse(engine.playing)
        assertEquals(1f, backend.volumes.last())
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
    }

    // MARK: - Episodes

    @Test fun sleepAppliesToEpisodes() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("a"), episode("b")), 0, 0, true, QueueSource.list))
        backend.start()
        sleep(SleepRequest.Minutes(15))
        assertEquals(900_000L, lastState(Item.Kind.episode)?.sleepRemainingMs)
        scheduler.advance(15 * 60.0)
        assertFalse(engine.playing)
        assertEquals(1f, backend.volumes.last())
        assertTrue(backend.volumes.contains(0f))
    }

    /** "End of this episode" stops on the finished episode (as the web player does): nothing more plays. */
    @Test fun endOfEpisodeStaysOnTheFinishedEpisode() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("a", durationS = 100), episode("b", durationS = 100)), 0, 0,
            true, QueueSource.list))
        backend.start()
        sleep(SleepRequest.EndOfTrack)
        assertNull(backend.preloads.last())
        assertEquals(100_000L, lastState(Item.Kind.episode)?.sleepRemainingMs)
        backend.playTo(95_000)
        assertEquals(0.5f, backend.volumes.last(), 0.001f)
        val loads = backend.loads.size
        backend.finish()
        assertEquals("the next episode does not start", loads, backend.loads.size)
        assertFalse(engine.playing)
        assertEquals("a", engine.current?.id)
        assertEquals(1f, backend.volumes.last())
        assertNull(lastState(Item.Kind.episode)?.sleepRemainingMs)
    }

    // MARK: - The phone asleep

    /**
     * The scheduler's clock may stop while the phone sleeps (paused, screen off); the deadline is wall time. Played
     * again after the deadline passed: the timer is over (it would have paused a paused player), not a pause now.
     */
    @Test fun aDeadlinePassedWhileAsleepIsOverOnPlay() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        engine.pause()
        advanceClock(20 * 60.0)                                  // asleep: the scheduler did not move
        engine.play()
        assertTrue(engine.playing)
        assertNull("never 0:00 while it plays on", lastState(Item.Kind.track)?.sleepRemainingMs)
        assertTrue(backend.volumes.all { it == 1f })
        scheduler.advance(20 * 60.0)                             // the late job, once awake: cancelled
        assertTrue(engine.playing)
        assertFalse(backend.volumes.any { it < 1f })
    }

    /** Woken inside the last 10 s (by wall time): the fade starts at once, on the next play or tick. */
    @Test fun theFadeStartsByWallTimeWhenTheSchedulerIsLate() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        engine.pause()
        advanceClock(15 * 60 - 5.0)
        engine.play()
        assertTrue(engine.playing)
        scheduler.advance(10.0)
        assertFalse(engine.playing)
        assertTrue(backend.volumes.contains(0f))
        assertEquals(1f, backend.volumes.last())
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
        val volumes = backend.volumes.size
        scheduler.advance(15 * 60.0)                             // the original job never runs a second fade
        assertEquals(volumes, backend.volumes.size)
    }

    @Test fun aTickPastTheFadeStartStartsIt() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        advanceClock(15 * 60 - 8.0)
        backend.playTo(500)
        scheduler.advance(10.0)
        assertFalse(engine.playing)
        assertEquals(1f, backend.volumes.last())
    }

    // MARK: - End of track with the modes and never-stop

    /** Repeat all at the last item: the new pass starts with the first item, loaded but not played. */
    @Test fun endOfTrackUnderRepeatAllWrapsPaused() = engineTest {
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2), 1, 0, true))
        backend.start()
        sleep(SleepRequest.EndOfTrack)
        backend.finish()
        assertEquals("1", engine.current?.id)
        assertEquals(false, backend.loads.last().autoplay)
        assertFalse(engine.playing)
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
    }

    /** Never-stop at the end of a list: the cached favorite it picks is loaded, not played. */
    @Test fun endOfTrackWithANeverStopFavoriteLoadsItPaused() = engineTest {
        cache.favorites = listOf(trackModel(9))
        playMusic(listOf(1))
        sleep(SleepRequest.EndOfTrack)
        backend.finish()
        assertEquals("9", engine.current?.id)
        assertEquals(false, backend.loads.last().autoplay)
        assertFalse(engine.playing)
    }

    /** A favorites queue whose refill only arrives after the stop: the new tracks are queued, nothing plays. */
    @Test fun aRefillAfterTheStopStaysPaused() = engineTest {
        api.refillError = LarkError.BadResponse
        engine.handle(q(listOf(1), 0, 0, true, QueueSource.favorites))
        backend.start()
        idle()
        sleep(SleepRequest.EndOfTrack)
        backend.finish()
        assertFalse(engine.playing)
        val loads = backend.loads.size
        api.refillError = null
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(5), trackModel(6)))
        scheduler.advance(PlayerEngine.REFILL_RETRY)
        idle()
        assertTrue("the refill arrived", engine.music.items.any { it.id == "5" })
        assertFalse(engine.playing)
        assertEquals(loads, backend.loads.size)
    }

    // MARK: - Sign-out

    @Test fun resetDropsTheTimer() = engineTest {
        playMusic()
        sleep(SleepRequest.Minutes(15))
        scheduler.advance(15 * 60 - 5.0)
        engine.reset()
        assertEquals(1f, backend.volumes.last())
        assertNull(lastState(Item.Kind.track)?.sleepRemainingMs)
        val volumes = backend.volumes.size
        scheduler.advance(60.0)
        assertEquals("no step of the fade runs after a sign-out", volumes, backend.volumes.size)
    }
}
