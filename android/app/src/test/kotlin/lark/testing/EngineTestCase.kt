package lark.testing

import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestResult
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import lark.bridge.Item
import lark.bridge.NativeEvent
import lark.player.EngineTexts
import lark.player.MediaSource
import lark.player.NowPlayingSink
import lark.player.PlayEventQueue
import lark.player.PlayerEngine
import lark.player.QueueStore
import org.junit.Rule
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

/**
 * One engine wired to fakes, with a store in its own temporary directory (iOS `EngineTestCase`). Each test runs
 * in [engineTest]: the engine's coroutines run on the test's dispatcher, so [idle] is iOS `await engine.idle()`.
 * Robolectric: the engine builds `Uri`s for streams.
 */
@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
abstract class EngineTestCase {
    @get:Rule val folder = TemporaryFolder()

    lateinit var backend: FakeBackend
    val api = FakeLarkApi()
    val cache = FakeCache()
    val network = FakeNetwork()
    val scheduler = FakeScheduler()
    lateinit var store: QueueStore
    lateinit var events: PlayEventQueue
    lateinit var engine: PlayerEngine
    lateinit var dir: File
    val clock = FakeClock()
    var randomValue = 0.0
    val sent = mutableListOf<NativeEvent>()
    /** Set before [engineTest] (or before [makeEngine]) to give the engine a now-playing sink. */
    var nowPlaying: NowPlayingSink? = null
    lateinit var scope: TestScope
    val texts = EngineTexts.english

    fun engineTest(body: suspend TestScope.() -> Unit): TestResult = runTest {
        scope = this
        dir = folder.newFolder("engine")
        store = QueueStore(dir).also { it.user = 1 }
        makeEngine()
        body()
    }

    /** A fresh engine on the same store, as after a process launch. The old one says nothing more. */
    fun makeEngine() {
        if (::engine.isInitialized) engine.onEvent = null
        backend = FakeBackend()
        events = PlayEventQueue(api, store, scope, clock)
        engine = PlayerEngine(backend, api, cache, store, events, network, scheduler, clock, { randomValue }, nowPlaying, scope, texts)
        sent.clear()
        engine.onEvent = { sent.add(it) }
    }

    /** Runs every coroutine started so far, and those they start (iOS `await engine.idle()`). */
    fun idle() = scope.advanceUntilIdle()

    fun tmp(name: String) = File(dir, name)
    fun advanceClock(s: Double) = clock.advance(s)
    fun remote(id: Int): MediaSource = MediaSource.Remote(api.streamUrl(track(id), "high"), "T1")

    val states: List<NativeEvent.State> get() = sent.filterIsInstance<NativeEvent.State>()
    fun queues(kind: Item.Kind): List<NativeEvent.Queue> = sent.filterIsInstance<NativeEvent.Queue>().filter { it.kind == kind }
    val notices: List<String> get() = sent.filterIsInstance<NativeEvent.Notice>().map { it.text }
    val ids: List<String> get() = engine.music.items.map { it.id }
}

/** Records what the engine shows on the lock screen and the car (iOS `FakeSink`, at the engine's level). */
class FakeNowPlayingSink : NowPlayingSink {
    data class Shown(val item: Item, val positionMs: Long, val durationMs: Long?, val rate: Double, val playing: Boolean, val lyricLine: String?)
    var last: Shown? = null
        private set
    var writes = 0
        private set
    var cleared = 0
        private set

    override fun show(item: Item, positionMs: Long, durationMs: Long?, rate: Double, playing: Boolean, lyricLine: String?) {
        last = Shown(item, positionMs, durationMs, rate, playing, lyricLine); writes++
    }
    override fun clear() { last = null; writes++; cleared++ }

    /** What the title line shows: the car-lyrics line, else the song. */
    val title: String? get() = last?.let { it.lyricLine ?: it.item.title }
    /** The artist line: "song · artist" under a lyric line (as the controller writes it), else the artist. */
    val artist: String? get() = last?.let { if (it.lyricLine != null) "${it.item.title} · ${it.item.artist}" else it.item.artist }
}
