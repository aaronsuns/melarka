package lark.service

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import lark.bridge.Item
import lark.core.Clock
import lark.player.NowPlayingSink
import java.time.Duration
import java.time.Instant

/**
 * What the lock screen, the notification and the car show (iOS `MPNowPlayingInfo`): the session's metadata and
 * position come from this. `artwork` is image data (never a URL: covers need the token), the same array object on every
 * write for the same item. `elapsedMs` is the position when it was written.
 */
data class NowPlayingInfo(
    /** `kind:id`: one item, whatever is shown for it. */
    val key: String,
    val kind: Item.Kind,
    val title: String,
    val artist: String,
    val album: String,
    val elapsedMs: Long,
    /** The player's duration, else the item's; null when neither is known. */
    val durationMs: Long?,
    /** The rate while playing, 0 while paused. */
    val playbackRate: Double,
    val defaultRate: Double,
    val artwork: ByteArray?,
)

/** Where now-playing info goes: the [NowPlayingHub] in the app, a counting fake in tests (iOS `NowPlayingSink`). */
interface NowPlayingInfoSink {
    var info: NowPlayingInfo?
}

/** The current [NowPlayingInfo], app-wide, for whichever media session shows it (the session comes and goes). */
class NowPlayingHub : NowPlayingInfoSink {
    private val listeners = mutableListOf<() -> Unit>()

    override var info: NowPlayingInfo? = null
        set(value) {
            field = value
            listeners.toList().forEach { it() }
        }

    fun addListener(fn: () -> Unit) { listeners.add(fn) }
    fun removeListener(fn: () -> Unit) { listeners.remove(fn) }
}

/**
 * The lock screen, the notification and the car display (Bluetooth AVRCP, Android Auto's now playing): a port of
 * `ios/Sources/Player/NowPlayingController.swift`, the engine's [NowPlayingSink]. With a car-lyrics line, the line is the
 * title and "song · artist" the artist, as on the web. Artwork is fetched once per item and kept for the last 20 items,
 * as one byte array reused on every write (some head units send the cover again when it changes). "No cover" (null from
 * `artwork`) is remembered; a fetch that threw (offline, a 5xx) is asked again after 30 s, then 60 s, … up to 10 minutes.
 * Main thread only; fetches run in `scope`.
 */
class NowPlayingController(
    private val sink: NowPlayingInfoSink,
    private val artwork: suspend (Item) -> ByteArray?,
    private val scope: CoroutineScope,
    private val clock: Clock = Clock.system,
) : NowPlayingSink {
    /** One cache entry: the cover, or null when there is none (not asked again while it is in the cache). */
    private class Cover(val bytes: ByteArray?)
    private val images = HashMap<String, Cover>()
    /** Fetches that threw: when to ask again, and the wait after the next failure. */
    private class Retry(val at: Instant, val nextWait: Double)
    private val retry = HashMap<String, Retry>()
    private val imageOrder = ArrayList<String>()              // least recently used first
    private val fetching = HashSet<String>()

    /** The last write: an arriving cover rewrites it, with the elapsed time moved on to now. */
    private class Last(val item: Item, val positionMs: Long, val durationMs: Long?, val rate: Double, val playing: Boolean,
                       val lyricLine: String?, val at: Instant)
    private var last: Last? = null

    override fun show(item: Item, positionMs: Long, durationMs: Long?, rate: Double, playing: Boolean, lyricLine: String?) {
        last = Last(item, positionMs, durationMs, rate, playing, lyricLine, clock.now())
        val key = key(item)
        val cached = images[key]
        if (cached != null) touch(key)
        val duration = durationMs ?: item.durationMs
        sink.info = NowPlayingInfo(
            key = key,
            kind = item.kind,
            title = lyricLine ?: item.title,
            artist = if (lyricLine == null) item.artist else if (item.artist.isEmpty()) item.title else "${item.title} · ${item.artist}",
            album = item.album,
            elapsedMs = maxOf(0L, positionMs),
            durationMs = duration.takeIf { it > 0 },
            playbackRate = if (playing) rate else 0.0,
            defaultRate = rate,
            artwork = cached?.bytes,
        )
        // After the write: a fetch that completes at once rewrites it with the cover.
        if (cached == null) fetchArtwork(item)
    }

    /** Nothing to show (stopped, signed out). */
    override fun clear() {
        last = null
        sink.info = null
    }

    private fun fetchArtwork(item: Item) {
        val key = key(item)
        if (key in fetching) return
        retry[key]?.let { if (clock.now() < it.at) return }
        fetching.add(key)
        scope.launch {
            val image = try {
                artwork(item)
            } catch (e: CancellationException) {
                fetching.remove(key)
                throw e
            } catch (_: Exception) {
                fetching.remove(key)
                val wait = retry[key]?.nextWait ?: ARTWORK_RETRY_S
                retry[key] = Retry(clock.now().plusMillis((wait * 1000).toLong()), minOf(wait * 2, ARTWORK_RETRY_MAX_S))
                if (retry.size > ARTWORK_CACHE_SIZE * 5) { val now = clock.now(); retry.entries.removeAll { it.value.at <= now } }
                return@launch
            }
            fetching.remove(key)
            retry.remove(key)
            images[key] = Cover(image)
            touch(key)
            while (imageOrder.size > ARTWORK_CACHE_SIZE) images.remove(imageOrder.removeAt(0))
            val l = last
            if (image != null && l != null && key(l.item) == key) {
                val moved = if (l.playing) (Duration.between(l.at, clock.now()).toMillis() * l.rate).toLong() else 0L
                show(l.item, l.positionMs + moved, l.durationMs, l.rate, l.playing, l.lyricLine)
            }
        }
    }

    private fun touch(key: String) {
        imageOrder.remove(key)
        imageOrder.add(key)
    }

    companion object {
        const val ARTWORK_CACHE_SIZE = 20
        const val ARTWORK_RETRY_S = 30.0
        const val ARTWORK_RETRY_MAX_S = 600.0

        fun key(item: Item) = "${item.kind.name}:${item.id}"
    }
}
