package lark.service

import android.content.Context
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import kotlinx.coroutines.CoroutineScope
import lark.bridge.Item
import lark.core.Clock
import lark.core.MainScheduler
import lark.core.loggingMainScope
import lark.player.Media3Backend
import lark.player.PlayerEngine
import okhttp3.OkHttpClient

/**
 * The playback parts that live as long as the process, not the Activity or the media service: the ExoPlayer the engine
 * drives, what is shown now ([nowPlaying], written by [nowPlayingSink], the engine's [lark.player.NowPlayingSink]),
 * the remote commands, and the [engine] (built by the app services, Task 10, and rebuilt for another server). The
 * media service shows them while it runs; destroying the Activity or the service stops none of them.
 *
 * The player's audio attributes follow the active kind: music is `CONTENT_TYPE_MUSIC` (ducks under a navigation
 * prompt), an episode `CONTENT_TYPE_SPEECH` (pauses instead), both `USAGE_MEDIA` with audio focus handled by the player.
 */
@UnstableApi
class PlaybackHost(
    val player: Player,
    val nowPlaying: NowPlayingHub,
    val scope: CoroutineScope,
    /** The engine's backend over [player] (null in tests that drive the engine with a fake). */
    val backend: Media3Backend?,
    sink: NowPlayingInfoSink = nowPlaying,
    clock: Clock = Clock.system,
    artwork: suspend (Item) -> ByteArray?,
    remote: RemoteCommands? = null,
    /** Brings the media service up: the engine runs it right before sound starts (its `willStartPlayback`). */
    private val startService: () -> Unit = {},
) {
    /**
     * The engine commands and the session go to. Setting it attaches the remote commands to it and takes its
     * `willStartPlayback`: the media service is started right before sound starts, so playback the page starts has the
     * service, its notification and the foreground from its first second.
     */
    var engine: PlayerEngine? = null
        set(value) {
            field?.willStartPlayback = null
            field = value
            if (value != null) {
                remote.attach(value)
                value.willStartPlayback = startService
            } else {
                remote.detach()
            }
        }

    /**
     * Builds the engine when a command finds none (the app services' `engine`, set by the application): the car's play
     * after a server change, before anything else asked for the new server's engine, still plays (iOS
     * `installRemoteCommands` resolves `AppServices.engine`). Null: commands use [engine] as it is.
     */
    var engineProvider: (() -> PlayerEngine?)? = null

    val remote: RemoteCommands = remote ?: RemoteCommands(scope) { engine ?: engineProvider?.invoke() }

    /** The engine's now-playing sink. */
    val nowPlayingSink = NowPlayingController(sink, artwork, scope, clock)

    /**
     * `MediaSession.Callback.onPlaybackResumption` for a play: the engine's synchronous resume from disk, no network.
     * With no engine yet (a cold start, or after a server change) it is built first ([engineProvider]), as for a command.
     */
    var resumption: () -> Unit = { (engine ?: engineProvider?.invoke())?.resumeFromDisk() }

    /**
     * A shortcut's or car autoplay's run is still preparing playback (`AppServices.intentRunning`, set by the
     * application): its shuffle may be asking the server after the shortcut's activity has gone. The media service's
     * cold-start hold waits for it.
     */
    var intentStarting: () -> Boolean = { false }

    private var kind: Item.Kind? = null

    init {
        this.remote.addListener { applyKind() }
        applyKind()
    }

    /**
     * The new kind's attributes, focus still handled by the player. Not toggled off and on to force a new focus request:
     * Media3 1.9.4's `AudioFocusManager` builds its platform request once and never rebuilds it, so a toggle would only
     * abandon and re-request the same request (and let another app's audio blip in between). What makes an episode
     * pause rather than duck is current anyway: the player's own duck handling reads the current content type, and the
     * platform's automatic ducking skips players whose track is speech (the renderer's track gets the new attributes).
     * A kind change also reloads the player (stop, prepare), which abandons and requests the focus again.
     */
    private fun applyKind() {
        val k = remote.kind
        if (k == kind) return
        kind = k
        player.setAudioAttributes(audioAttributes(k), true)
    }

    companion object {
        fun audioAttributes(kind: Item.Kind): AudioAttributes = AudioAttributes.Builder()
            .setUsage(C.USAGE_MEDIA)
            .setContentType(if (kind == Item.Kind.episode) C.AUDIO_CONTENT_TYPE_SPEECH else C.AUDIO_CONTENT_TYPE_MUSIC)
            .build()

        private const val TAG = "PlaybackHost"

        /**
         * The production parts: a [Media3Backend] on the main thread, covers from the engine's server, and a main-thread
         * scope that logs a failed task (the remote commands' cold-start shuffle runs there) as the app services' does.
         */
        fun create(context: Context, http: OkHttpClient): PlaybackHost {
            val backend = Media3Backend.create(context, MainScheduler(), http)
            lateinit var host: PlaybackHost
            val app = context.applicationContext
            host = PlaybackHost(backend.player, NowPlayingHub(), loggingMainScope(TAG), backend,
                artwork = { item -> ArtworkLoader { host.engine?.client }.load(item) },
                startService = { PlaybackService.start(app) })
            return host
        }
    }
}

/**
 * The process's playback host, built on first use (the media service's `onCreate`, or the app services). The stream
 * client comes from [httpClient]: the application sets the app's one client (`LarkHttp`, trusting the user's CAs as the
 * network security config says) before anything is built.
 */
@UnstableApi
object Playback {
    @Volatile var httpClient: () -> OkHttpClient = { OkHttpClient() }

    /**
     * Runs once the host is first built (set by the application): the app services are brought up then, so a media
     * service started with no Activity (a Bluetooth play) finds the engine.
     */
    @Volatile var onCreated: ((Context) -> Unit)? = null
    private var host: PlaybackHost? = null

    fun host(context: Context): PlaybackHost =
        host ?: PlaybackHost.create(context.applicationContext, httpClient()).also {
            host = it
            onCreated?.invoke(context.applicationContext)
        }

    /** Tests: the host the service is to use. */
    fun install(host: PlaybackHost) { this.host = host }
    fun uninstall() { host = null }
}
