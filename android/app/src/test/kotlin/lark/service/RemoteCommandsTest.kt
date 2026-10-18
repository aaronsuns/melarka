package lark.service

import android.content.Context
import android.os.Bundle
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.CommandButton
import androidx.media3.session.MediaSession
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionResult
import androidx.test.core.app.ApplicationProvider
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.WebMessage
import lark.player.PlayModes
import lark.testing.EngineTestCase
import lark.testing.FakeInfoSink
import lark.testing.SessionPlayerRig
import lark.testing.episode
import lark.testing.q
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/RemoteCommandsModesTests.swift (the lock screen's and the car's repeat and shuffle, mapped onto
 * the engine's modes and off while an episode plays), and of how the media session reaches [RemoteCommands]: its
 * player commands, the notification's custom buttons and playback resumption.
 */
@UnstableApi
class RemoteCommandsTest : EngineTestCase() {
    private val sink = FakeInfoSink()
    private lateinit var commands: RemoteCommands
    private var rig: SessionPlayerRig? = null
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    override fun beforeEngine() {
        nowPlaying = NowPlayingController(sink, { null }, scope, clock)
    }

    private fun attach() { commands = RemoteCommands(scope, engine) }
    private fun session(): Player = (rig ?: SessionPlayerRig(context, commands, sink.hub).also { rig = it }).player

    @After fun tearDown() {
        if (::commands.isInitialized) commands.detach()
        rig?.release(); rig = null
    }

    private fun playEpisode() =
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("abcdefghijk")), 0, 0, true, QueueSource.list))

    // MARK: RemoteCommandsModesTests

    @Test fun repeatCommandSetsTheEngineMode() = engineTest {
        attach()
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        assertEquals(RemoteCommands.Status.success, commands.changeRepeatNow(RepeatMode.all))
        assertEquals(RepeatMode.all, engine.modes.repeatMode)
        assertEquals(RepeatMode.all, commands.repeatMode)
        assertEquals(RemoteCommands.Status.success, commands.changeRepeatNow(RepeatMode.one))
        assertEquals(RepeatMode.one, engine.modes.repeatMode)
        assertEquals(RemoteCommands.Status.success, commands.changeRepeatNow(RepeatMode.off))
        assertEquals(RepeatMode.off, engine.modes.repeatMode)
        assertFalse(engine.modes.shuffle)                    // the other mode is left alone
    }

    @Test fun shuffleCommandSetsTheEngineMode() = engineTest {
        attach()
        engine.handle(q(listOf(1, 2, 3, 4), 0, 0, true))
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        val loads = backend.loads.size
        assertEquals(RemoteCommands.Status.success, commands.changeShuffleNow(true))
        assertTrue(engine.modes.shuffle)
        assertEquals(RepeatMode.all, engine.modes.repeatMode)
        assertTrue(commands.shuffle)
        assertEquals(loads, backend.loads.size)              // the current item plays on
        assertEquals(RemoteCommands.Status.success, commands.changeShuffleNow(false))
        assertFalse(engine.modes.shuffle)
        assertFalse(commands.shuffle)
        assertEquals(RemoteCommands.Status.success, commands.changeShuffleNow(true))   // iOS: any type but off turns it on
        assertTrue(engine.modes.shuffle)
    }

    /** The page's buttons (`setModes`) show on the lock screen too, and a restored engine's modes on attach. */
    @Test fun theCommandsShowTheEngineModes() = engineTest {
        attach()
        engine.handle(WebMessage.SetModes(true, RepeatMode.one))
        assertEquals(RepeatMode.one, commands.repeatMode)
        assertTrue(commands.shuffle)
        commands.detach()
        makeEngine()                                          // restored with the saved modes
        attach()
        assertEquals(RepeatMode.one, commands.repeatMode)
        engine.handle(WebMessage.SetModes(false, RepeatMode.off))
        assertEquals(RepeatMode.off, commands.repeatMode)
        assertFalse(commands.shuffle)
    }

    @Test fun bothAreOffWhileAnEpisodePlays() = engineTest {
        attach()
        assertTrue(commands.modesEnabled)
        playEpisode()
        assertFalse(commands.modesEnabled)
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, commands.changeRepeatNow(RepeatMode.one))
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, commands.changeShuffleNow(true))
        assertEquals(PlayModes(), engine.modes)
        engine.handle(WebMessage.Stop(Item.Kind.episode))
        assertTrue(commands.modesEnabled)
    }

    // MARK: The session's player commands

    @Test fun sessionCommandsFollowTheActiveKind() = engineTest {
        attach()
        val p = session()
        engine.handle(q(listOf(1, 2), 0, 0, true))
        for (c in listOf(Player.COMMAND_PLAY_PAUSE, Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_PREVIOUS,
                Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM, Player.COMMAND_GET_CURRENT_MEDIA_ITEM, Player.COMMAND_GET_METADATA,
                Player.COMMAND_SET_SHUFFLE_MODE, Player.COMMAND_SET_REPEAT_MODE)) assertTrue("$c", p.isCommandAvailable(c))
        assertFalse(p.isCommandAvailable(Player.COMMAND_SEEK_FORWARD))
        assertFalse(p.isCommandAvailable(Player.COMMAND_SEEK_BACK))
        playEpisode()
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_FORWARD))
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_BACK))
        assertFalse(p.isCommandAvailable(Player.COMMAND_SET_SHUFFLE_MODE))
        assertFalse(p.isCommandAvailable(Player.COMMAND_SET_REPEAT_MODE))
        // The engine owns the queue, the volume (the sleep fade) and the rate: controllers never set them.
        for (c in listOf(Player.COMMAND_SET_MEDIA_ITEM, Player.COMMAND_CHANGE_MEDIA_ITEMS, Player.COMMAND_SET_VOLUME,
                Player.COMMAND_SET_SPEED_AND_PITCH)) assertFalse("$c", p.isCommandAvailable(c))
    }

    @Test fun sessionPlayerRoutesToTheEngine() = engineTest {
        attach()
        val p = session()
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.start()
        p.pause()
        assertFalse(engine.playing)
        p.play()
        assertTrue(engine.playing)
        assertEquals("play", backend.calls.last())
        p.seekTo(42_500)
        assertEquals("seek:42500", backend.calls.last())
        p.seekToNext()
        assertEquals("2", engine.current?.id)
        backend.positionMs = 0
        p.seekToPrevious()
        assertEquals("1", engine.current?.id)
        p.shuffleModeEnabled = true
        assertTrue(engine.modes.shuffle)
        assertTrue(p.shuffleModeEnabled)
        p.repeatMode = Player.REPEAT_MODE_ONE
        assertEquals(RepeatMode.one, engine.modes.repeatMode)
        assertEquals(Player.REPEAT_MODE_ONE, p.repeatMode)
        p.repeatMode = Player.REPEAT_MODE_ALL
        assertEquals(RepeatMode.all, engine.modes.repeatMode)
    }

    @Test fun episodeSeekButtonsSkip() = engineTest {
        attach()
        val p = session()
        playEpisode()
        backend.start()
        backend.positionMs = 60_000
        p.seekForward()
        assertEquals("seek:90000", backend.calls.last())
        p.seekBack()
        assertEquals("seek:75000", backend.calls.last())
    }

    /** The session's play with nothing current is the engine's cold start (iOS `handlePlay`), not a playlist of its own. */
    @Test fun sessionPlayWithNothingLoadedResumesTheRestoredQueue() = engineTest {
        engine.handle(q(listOf(5, 6), 1, 7000, false))
        makeEngine()
        sink.info = null                                      // a new process: nothing shown yet
        attach()
        val p = session()
        assertEquals(0, p.mediaItemCount)
        p.play()
        idle()
        assertEquals(1, backend.loads.size)
        assertEquals(7000L, backend.loads[0].startMs)
        assertEquals("6", engine.current?.id)
        assertEquals(1, p.mediaItemCount)
    }

    // MARK: The notification's buttons (custom commands)

    @Test fun customCommandsSkipForEpisodes() = engineTest {
        attach()
        playEpisode()
        backend.start()
        backend.positionMs = 60_000
        assertEquals(RemoteCommands.Status.success, commands.custom(RemoteCommands.SKIP_FORWARD_30))
        assertEquals("seek:90000", backend.calls.last())
        assertEquals(RemoteCommands.Status.success, commands.custom(RemoteCommands.SKIP_BACK_15))
        assertEquals("seek:75000", backend.calls.last())
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, commands.custom(RemoteCommands.SHUFFLE))
        assertEquals(RemoteCommands.Status.commandFailed, commands.custom("lark.UNKNOWN"))
    }

    @Test fun customCommandsToggleShuffleAndCycleRepeatForMusic() = engineTest {
        attach()
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        assertEquals(RemoteCommands.Status.success, commands.custom(RemoteCommands.SHUFFLE))
        assertTrue(engine.modes.shuffle)
        commands.custom(RemoteCommands.SHUFFLE)
        assertFalse(engine.modes.shuffle)
        val seen = (1..3).map { commands.custom(RemoteCommands.REPEAT); engine.modes.repeatMode }
        assertEquals(listOf(RepeatMode.all, RepeatMode.one, RepeatMode.off), seen)
    }

    @Test fun theNotificationButtonsFollowTheKindAndTheModes() = engineTest {
        attach()
        engine.handle(q(listOf(1, 2), 0, 0, true))
        var buttons = PlaybackService.mediaButtons(context, commands)
        assertEquals(listOf(RemoteCommands.SHUFFLE, RemoteCommands.REPEAT), buttons.map { it.sessionCommand?.customAction })
        assertEquals(CommandButton.ICON_SHUFFLE_OFF, buttons[0].icon)
        assertEquals(CommandButton.ICON_REPEAT_OFF, buttons[1].icon)
        assertEquals("Shuffle", buttons[0].displayName.toString())
        assertEquals("Repeat: Off", buttons[1].displayName.toString())
        engine.handle(WebMessage.SetModes(true, RepeatMode.one))
        buttons = PlaybackService.mediaButtons(context, commands)
        assertEquals(CommandButton.ICON_SHUFFLE_ON, buttons[0].icon)
        assertEquals(CommandButton.ICON_REPEAT_ONE, buttons[1].icon)
        assertEquals("Repeat One", buttons[1].displayName.toString())
        playEpisode()
        buttons = PlaybackService.mediaButtons(context, commands)
        assertEquals(listOf(RemoteCommands.SKIP_BACK_15, RemoteCommands.SKIP_FORWARD_30), buttons.map { it.sessionCommand?.customAction })
        assertEquals(CommandButton.ICON_SKIP_BACK_15, buttons[0].icon)
        assertEquals(CommandButton.ICON_SKIP_FORWARD_30, buttons[1].icon)
        // In place of previous and next, as the iPhone's lock screen does for episodes.
        assertEquals(CommandButton.SLOT_BACK, buttons[0].slots[0])
        assertEquals(CommandButton.SLOT_FORWARD, buttons[1].slots[0])
        assertEquals("Back 15 seconds", buttons[0].displayName.toString())
    }

    // MARK: The session callback

    private fun controller() = MediaSession.ControllerInfo.createTestOnlyControllerInfo(
        "lark.test", 0, 0, 0, 0, false, Bundle.EMPTY, false)

    @Test fun customCommandsReachTheEngineThroughTheSession() = engineTest {
        attach()
        val host = PlaybackHost(rigPlayer(), sink.hub, scope, null, sink = sink, clock = clock, artwork = { null }, remote = commands)
        val session = MediaSession.Builder(context, session()).build()
        try {
            val cb = PlaybackService.SessionCallback(host)
            val connection = cb.onConnect(session, controller())
            for (a in RemoteCommands.CUSTOM_ACTIONS)
                assertTrue(a, connection.availableSessionCommands.contains(SessionCommand(a, Bundle.EMPTY)))
            engine.handle(q(listOf(1, 2), 0, 0, true))
            val r = cb.onCustomCommand(session, controller(), SessionCommand(RemoteCommands.SHUFFLE, Bundle.EMPTY), Bundle.EMPTY).get()
            assertEquals(SessionResult.RESULT_SUCCESS, r.resultCode)
            assertTrue(engine.modes.shuffle)
        } finally {
            session.release()
        }
    }

    /** The resumption hook (Task 11 owns its behaviour): run for a play, not for a metadata request. */
    @Test fun playbackResumptionCallsTheHook() = engineTest {
        attach()
        var calls = 0
        val host = PlaybackHost(rigPlayer(), sink.hub, scope, null, sink = sink, clock = clock, artwork = { null }, remote = commands)
        host.resumption = { calls++ }
        val session = MediaSession.Builder(context, session()).build()
        try {
            val cb = PlaybackService.SessionCallback(host)
            cb.onPlaybackResumption(session, controller(), false).get()
            assertEquals(0, calls)
            cb.onPlaybackResumption(session, controller(), true).get()
            assertEquals(1, calls)
        } finally {
            session.release()
        }
    }

    /** The default hook is the engine's synchronous resume from disk. */
    @Test fun theDefaultResumptionHookResumesFromDisk() = engineTest {
        engine.handle(q(listOf(5, 6), 1, 7000, false))
        makeEngine()
        attach()
        val host = PlaybackHost(rigPlayer(), sink.hub, scope, null, sink = sink, clock = clock, artwork = { null }, remote = commands)
        host.engine = engine
        host.resumption()
        assertEquals(1, backend.loads.size)
        assertEquals("6", engine.current?.id)
    }

    private fun rigPlayer(): Player = (rig ?: SessionPlayerRig(context, commands, sink.hub).also { rig = it }).exo
}
