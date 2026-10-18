package lark.service

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.CommandButton
import androidx.media3.session.DefaultMediaNotificationProvider
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
import lark.car.ShortcutActivity

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
        foregroundStartPending = false
        firstStart = true
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

    // MARK: Cold start: the foreground within 5 s

    private val handler = Handler(Looper.getMainLooper())
    /** The cold-start notification is up and Media3's own has not taken over yet. */
    private var holding = false
    private var holdSince = 0L
    /** No start has reached [onStartCommand] since [onCreate]: only that one can be a cold start. */
    private var firstStart = false

    /**
     * A start with `startForegroundService` (a media button with the app not running, or car autoplay from the
     * background) must call `startForeground` within 5 s, even when nothing turns out to be playable (no server yet,
     * signed out, offline with nothing cached). The service goes into the foreground at once with a plain notification
     * on Media3's channel and id, which Media3's media notification replaces when playback starts; when nothing plays,
     * it is removed and the service stops.
     *
     * Only the first start after [onCreate] is such a cold start. Later `MEDIA_BUTTON` starts come to a service that is
     * already running, most of them from Media3's own notification buttons (Android 8-12 sends them as service starts):
     * they go to Media3 alone, so a paused notification's Previous or Play neither swaps the notification nor stops
     * the service.
     */
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val first = firstStart
        firstStart = false
        val coldStart = first && intent != null && !isPlaybackOngoing &&
            (intent.action == Intent.ACTION_MEDIA_BUTTON || intent.getBooleanExtra(EXTRA_FOREGROUND_START, false))
        if (coldStart) hold()
        val r = super.onStartCommand(intent, flags, startId)
        if (coldStart && holding) handler.post(checkHold)       // after the media button's play, which Media3 posted
        return r
    }

    private fun hold() {
        holdSince = SystemClock.elapsedRealtime()
        try {
            ServiceCompat.startForeground(this, DefaultMediaNotificationProvider.DEFAULT_NOTIFICATION_ID, holdNotification(this),
                if (Build.VERSION.SDK_INT >= 29) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK else 0)
            holding = true
        } catch (e: IllegalStateException) {
            // ForegroundServiceStartNotAllowedException (API 31+): nothing can play from here; the start is dropped.
            Log.w(TAG, "media service: not allowed into the foreground (${e.javaClass.simpleName})")
            stopSelf()
        }
    }

    /**
     * Until Media3 takes over (its notification put the service in the foreground) or nothing is going to play: playing,
     * a shuffle from the server still running (a play command's, or a shortcut's or car autoplay's run), or a cold start
     * waiting for the network's first answer keep it (the waits for at most [HOLD_MAX_MS]).
     */
    private val checkHold: Runnable = object : Runnable {
        override fun run() {
            if (!holding) return
            if (isPlaybackOngoing) { holding = false; return }
            val engine = host.engine
            val waiting = host.remote.starting || host.intentStarting() || engine?.awaitingNetworkAnswer == true
            if (engine?.playing == true || (waiting && SystemClock.elapsedRealtime() - holdSince < HOLD_MAX_MS)) {
                handler.postDelayed(this, HOLD_CHECK_MS)
                return
            }
            holding = false
            ServiceCompat.stopForeground(this@PlaybackService, ServiceCompat.STOP_FOREGROUND_REMOVE)
            if (engine?.playing != true) stopSelf()
        }
    }

    /**
     * The shortcuts' activity (`ShortcutActivity`) runs in a task of its own that goes as soon as it finishes, and Android
     * tells every started service of the app: that is not the user swiping Melarka away. Media3's default would pause
     * the playback the shortcut is starting (the session does not show it as playing yet) and stop the service. Any
     * other task (Melarka's own, swiped away) gets Media3's default: the service stops unless something is playing.
     */
    override fun onTaskRemoved(rootIntent: Intent?) {
        if (rootIntent?.component?.className == ShortcutActivity::class.java.name) return
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        handler.removeCallbacks(checkHold)
        holding = false
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
         * items (its play is the engine's cold start, [RemoteCommands.handlePlay]), so Media3 does not call this for a
         * play today; when it does, it answers at once from disk: for a play, the host's resumption hook (the engine's
         * synchronous `resumeFromDisk`, no network), then the engine's active queue, its index and position (the
         * restored one, or what the resume chose); nothing when there is no queue.
         */
        override fun onPlaybackResumption(session: MediaSession, controller: MediaSession.ControllerInfo,
                                          isForPlayback: Boolean): ListenableFuture<MediaSession.MediaItemsWithStartPosition> {
            if (isForPlayback) host.resumption()
            return Futures.immediateFuture(resumed(host))
        }
    }

    companion object {
        private const val TAG = "PlaybackService"
        /** Set on a [startInForeground] start: hold the foreground until playback starts or nothing will. */
        const val EXTRA_FOREGROUND_START = "lark.FOREGROUND_START"
        const val HOLD_CHECK_MS = 1_000L
        /** How long a cold start waiting for the server or the network keeps the notification. */
        const val HOLD_MAX_MS = 30_000L

        /** The engine's active queue as media items (the session's resumption answer). */
        fun resumed(host: PlaybackHost): MediaSession.MediaItemsWithStartPosition {
            val e = host.engine
            val q = e?.let { if (it.active == Item.Kind.episode) it.episodes else it.music }
            if (e == null || q == null || q.items.isEmpty()) return MediaSession.MediaItemsWithStartPosition(emptyList(), C.INDEX_UNSET, C.TIME_UNSET)
            val items = q.items.map {
                MediaItem.Builder().setMediaId(it.id).setMediaMetadata(MediaMetadata.Builder()
                    .setTitle(it.title).setArtist(it.artist).setAlbumTitle(it.album).build()).build()
            }
            return MediaSession.MediaItemsWithStartPosition(items, q.index, e.activePositionMs)
        }

        /** The plain notification the service shows while a cold start decides (replaced by Media3's). */
        fun holdNotification(context: Context): android.app.Notification {
            val nm = context.getSystemService(NotificationManager::class.java)
            if (nm.getNotificationChannel(DefaultMediaNotificationProvider.DEFAULT_CHANNEL_ID) == null)
                nm.createNotificationChannel(NotificationChannel(DefaultMediaNotificationProvider.DEFAULT_CHANNEL_ID,
                    context.getString(DefaultMediaNotificationProvider.DEFAULT_CHANNEL_NAME_RESOURCE_ID), NotificationManager.IMPORTANCE_LOW))
            return NotificationCompat.Builder(context, DefaultMediaNotificationProvider.DEFAULT_CHANNEL_ID)
                .setSmallIcon(androidx.media3.session.R.drawable.media3_notification_small_icon)
                .setContentTitle(context.getString(R.string.app_name))
                .setContentText(context.getString(R.string.cold_start_notification))
                .setOngoing(true)
                .setSilent(true)
                .build()
        }

        /**
         * Starts the service from the background into the foreground (car autoplay: a companion device appeared, or the
         * car's Bluetooth connected below Android 12). Android 12+ allows it for an app with a companion device
         * association that holds `REQUEST_COMPANION_START_FOREGROUND_SERVICES_FROM_BACKGROUND`; a refusal is logged and
         * returns false (nothing plays: playback without the service would be stopped by the system anyway).
         */
        fun startInForeground(context: Context): Boolean {
            if (running) return true
            return try {
                ContextCompat.startForegroundService(context, Intent(context, PlaybackService::class.java).putExtra(EXTRA_FOREGROUND_START, true))
                foregroundStartPending = true
                true
            } catch (e: IllegalStateException) {
                Log.w(TAG, "media service: not allowed to start in the foreground from the background (${e.javaClass.simpleName})")
                false
            }
        }

        /** [startInForeground] asked for the service and it is not created yet. */
        @Volatile private var foregroundStartPending = false

        /** The service exists (created and not yet destroyed). */
        @Volatile var running = false
            private set

        /**
         * Brings the service up when the engine is about to play and it is not running (the page started playback, or
         * the network came back). Android refuses a start from the background (API 26+): that is logged, and the
         * session comes with the next controller or media button instead.
         */
        fun start(context: Context) {
            if (running || foregroundStartPending) return
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
