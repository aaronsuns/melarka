package lark.testing

import android.content.Context
import android.os.Looper
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.TestExoPlayerBuilder
import androidx.media3.test.utils.robolectric.TestPlayerRunHelper
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.player.Media3Backend
import lark.player.PlayEventQueue
import lark.player.PlayerEngine
import lark.player.QueueStore
import lark.service.NowPlayingController
import lark.service.NowPlayingHub
import lark.service.NowPlayingInfo
import lark.service.NowPlayingInfoSink
import lark.service.Playback
import lark.service.PlaybackHost
import lark.service.PlaybackService
import lark.service.RemoteCommands
import lark.service.SessionPlayer
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okio.Buffer
import org.robolectric.Robolectric
import org.robolectric.Shadows.shadowOf
import org.robolectric.android.controller.ServiceController
import java.io.File

/** Counts what the now-playing controller writes (iOS `FakeSink`), and passes it on to a hub a session player can read. */
class FakeInfoSink : NowPlayingInfoSink {
    val hub = NowPlayingHub()
    var writes = 0
        private set

    override var info: NowPlayingInfo?
        get() = hub.info
        set(value) { writes++; hub.info = value }

    val title: String? get() = info?.title
    val artist: String? get() = info?.artist
}

/** A [SessionPlayer] over an idle ExoPlayer (the engine under test drives a fake backend): what the session shows. */
@UnstableApi
class SessionPlayerRig(context: Context, remote: RemoteCommands, hub: NowPlayingHub) {
    val exo: ExoPlayer = TestExoPlayerBuilder(context).build()
    val player = SessionPlayer(exo, remote, hub)

    fun release() { player.release(); exo.release() }
}

/**
 * The app-wide playback parts as the app builds them, on a real ExoPlayer (`TestExoPlayerBuilder`, fake clock) configured
 * like the production one, with an engine wired to fakes, installed as [Playback]'s host so the media service uses it.
 * Episodes stream from a local MockWebServer (a WAV), never from the network.
 */
@OptIn(ExperimentalCoroutinesApi::class)
@UnstableApi
class ServiceRig(val context: Context, private val dir: File) {
    val backendRig = BackendRig(context)
    val player: ExoPlayer get() = backendRig.player
    val api = FakeLarkApi()
    val cache = FakeCache()
    val network = FakeNetwork()
    val clock = FakeClock()
    val scope = TestScope(UnconfinedTestDispatcher())
    val store = QueueStore(dir).also { it.user = 1 }
    val sink = FakeInfoSink()
    val host: PlaybackHost
    val remote: RemoteCommands get() = host.remote
    lateinit var engine: PlayerEngine
        private set
    val server = MockWebServer()

    init {
        Media3Backend.configure(player)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest) =
                MockResponse().setHeader("Content-Type", "audio/wav").setBody(Buffer().write(wavBytes(10.0, 8000)))
        }
        server.start()
        api.streamBase = server.url("/").toString().trimEnd('/')
        host = PlaybackHost(player, sink.hub, scope, backendRig.backend, sink = sink, clock = clock, artwork = { null },
            startService = { PlaybackService.start(context) })
        makeEngine()
        Playback.install(host)
    }

    /** A fresh engine on the same store and player (a relaunch of the process's engine). */
    fun makeEngine() {
        if (::engine.isInitialized) engine.onEvent = null
        engine = PlayerEngine(backendRig.backend, api, cache, store, PlayEventQueue(api, store, scope, clock), network,
            backendRig.scheduler, clock, { 0.0 }, host.nowPlayingSink, scope)
        host.engine = engine
    }

    private val services = mutableListOf<ServiceController<PlaybackService>>()

    /** A media service, created (as on a controller's bind or a media button); [release] ends any left running. */
    fun startService(): ServiceController<PlaybackService> =
        Robolectric.buildService(PlaybackService::class.java).create().also { services.add(it) }

    /** A track on the phone (a WAV of `seconds`), played until the player says it is playing. */
    fun playTrack(id: Int, seconds: Double = 30.0) {
        cache.local[id.toLong()] = silentWav(dir, seconds, 8000)
        engine.handle(q(listOf(id), 0, 0, true))
        waitUntil { player.isPlaying }
    }

    /** An episode, streamed from the local server. */
    fun playEpisode(id: String = "abcdefghijk") {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode(id)), 0, 0, true, QueueSource.list))
        waitUntil { player.isPlaying }
    }

    fun waitUntil(condition: () -> Boolean) = backendRig.waitUntil(condition)

    /** Lets the player handle everything asked of it so far, and the main looper run what that posted. */
    fun settle() {
        TestPlayerRunHelper.advance(player).untilPendingCommandsAreFullyHandled()
        shadowOf(Looper.getMainLooper()).idle()
    }

    fun release() {
        services.filter { it.get().session != null }.forEach { it.destroy() }   // a session left open breaks the next test's
        Playback.uninstall()
        backendRig.release()
        server.shutdown()
    }
}
