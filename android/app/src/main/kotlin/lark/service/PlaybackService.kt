package lark.service

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.util.Log
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.CommandButton
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionError
import androidx.media3.session.SessionResult
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import lark.app.MainActivity
import lark.app.R
import lark.bridge.Item
import lark.bridge.RepeatMode

/**
 * The media service: the MediaSession over the app-wide player ([Playback]'s host), the notification and lock screen
 * (Media3's default notification, foreground while playing), headset, Bluetooth and car buttons. It does not own the
 * engine or the player: when it ends (swiped away while paused), they stay, and the next play brings it back. While it
 * plays, the Activity can be destroyed and the task swiped away without stopping the music.
 */
@UnstableApi
class PlaybackService : MediaSessionService() {
    /** The session while the service runs. */
    var session: MediaSession? = null
        private set
    private lateinit var host: PlaybackHost
    private val onRemote: () -> Unit = { session?.setMediaButtonPreferences(mediaButtons(this, host.remote)) }

    override fun onCreate() {
        super.onCreate()
        running = true
        host = Playback.host(this)
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        session = MediaSession.Builder(this, SessionPlayer(host.player, host.remote, host.nowPlaying))
            .setCallback(SessionCallback(host))
            .setSessionActivity(open)
            .setMediaButtonPreferences(mediaButtons(this, host.remote))
            .build()
            // Tracked from the start, not only once a controller binds: playback the page or the car started puts the
            // service in the foreground with its notification.
            .also { addSession(it) }
        host.remote.addListener(onRemote)
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onDestroy() {
        host.remote.removeListener(onRemote)
        session?.let {
            it.player.release()          // the session's view only: the app-wide player plays on
            it.release()
        }
        session = null
        running = false
        super.onDestroy()
    }

    /** The custom commands, playback resumption. Player commands reach [SessionPlayer]. */
    class SessionCallback(private val host: PlaybackHost) : MediaSession.Callback {
        override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
            val commands = MediaSession.ConnectionResult.DEFAULT_SESSION_COMMANDS.buildUpon()
            RemoteCommands.CUSTOM_ACTIONS.forEach { commands.add(SessionCommand(it, Bundle.EMPTY)) }
            return MediaSession.ConnectionResult.AcceptedResultBuilder(session).setAvailableSessionCommands(commands.build()).build()
        }

        override fun onCustomCommand(session: MediaSession, controller: MediaSession.ControllerInfo, customCommand: SessionCommand,
                                     args: Bundle): ListenableFuture<SessionResult> =
            Futures.immediateFuture(when (host.remote.custom(customCommand.customAction)) {
                RemoteCommands.Status.success -> SessionResult(SessionResult.RESULT_SUCCESS)
                RemoteCommands.Status.noActionableNowPlayingItem -> SessionResult(SessionError.ERROR_INVALID_STATE)
                RemoteCommands.Status.commandFailed -> SessionResult(SessionError.ERROR_NOT_SUPPORTED)
            })

        /**
         * Asked when a controller plays with no current item and the player could take one. [SessionPlayer] takes no
         * items (its play is the engine's cold start), so Media3 does not call this for a play; it is wired to the
         * host's resumption hook for when it does, and answers a metadata request with the current item, if any.
         */
        override fun onPlaybackResumption(session: MediaSession, controller: MediaSession.ControllerInfo,
                                          isForPlayback: Boolean): ListenableFuture<MediaSession.MediaItemsWithStartPosition> {
            if (isForPlayback) host.resumption()
            val item = session.player.currentMediaItem
            return Futures.immediateFuture(MediaSession.MediaItemsWithStartPosition(listOfNotNull(item),
                if (item == null) C.INDEX_UNSET else 0, C.TIME_UNSET))
        }
    }

    companion object {
        private const val TAG = "PlaybackService"

        /** The service exists (created and not yet destroyed). */
        @Volatile var running = false
            private set

        /**
         * Brings the service up when the engine is about to play and it is not running (the page started playback, or
         * the network came back). Android refuses a start from the background (API 26+): that is logged, and the
         * session comes with the next controller or media button instead.
         */
        fun start(context: Context) {
            if (running) return
            try {
                context.startService(Intent(context, PlaybackService::class.java))
            } catch (_: IllegalStateException) {
                Log.w(TAG, "media service: not allowed to start from the background")
            }
        }

        /**
         * The notification's own buttons: for an episode, back 15 s and forward 30 s in place of previous and next (as
         * the iPhone's lock screen); for music, shuffle and repeat (showing their state) in the overflow.
         */
        fun mediaButtons(context: Context, remote: RemoteCommands): List<CommandButton> =
            if (remote.kind == Item.Kind.episode) listOf(
                button(RemoteCommands.SKIP_BACK_15, CommandButton.ICON_SKIP_BACK_15, context.getString(R.string.media_back_15), CommandButton.SLOT_BACK),
                button(RemoteCommands.SKIP_FORWARD_30, CommandButton.ICON_SKIP_FORWARD_30, context.getString(R.string.media_forward_30), CommandButton.SLOT_FORWARD),
            ) else listOf(
                button(RemoteCommands.SHUFFLE, if (remote.shuffle) CommandButton.ICON_SHUFFLE_ON else CommandButton.ICON_SHUFFLE_OFF,
                    context.getString(R.string.media_shuffle), CommandButton.SLOT_OVERFLOW),
                when (remote.repeatMode) {
                    RepeatMode.off -> button(RemoteCommands.REPEAT, CommandButton.ICON_REPEAT_OFF, context.getString(R.string.media_repeat_off), CommandButton.SLOT_OVERFLOW)
                    RepeatMode.all -> button(RemoteCommands.REPEAT, CommandButton.ICON_REPEAT_ALL, context.getString(R.string.media_repeat_all), CommandButton.SLOT_OVERFLOW)
                    RepeatMode.one -> button(RemoteCommands.REPEAT, CommandButton.ICON_REPEAT_ONE, context.getString(R.string.media_repeat_one), CommandButton.SLOT_OVERFLOW)
                },
            )

        private fun button(action: String, icon: Int, name: String, slot: Int) = CommandButton.Builder(icon)
            .setSessionCommand(SessionCommand(action, Bundle.EMPTY))
            .setDisplayName(name)
            .setSlots(slot)
            .build()
    }
}
