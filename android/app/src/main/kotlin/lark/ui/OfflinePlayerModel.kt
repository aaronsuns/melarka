package lark.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import lark.api.ApiTrack
import lark.api.LyricsDoc
import lark.api.LyricsLine
import lark.app.OfflinePlayer
import lark.bridge.Item
import lark.bridge.NativeEvent
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.WebMessage
import lark.player.PlayerEngine
import lark.player.toItem
import java.io.File
import java.text.Collator
import java.util.Locale

/**
 * What the offline player drives: the engine, through the same messages the web sends ([WebMessage]), so the engine's
 * rules (shuffle after the chosen song, never-stop, the cache first) apply exactly as they do for the web.
 */
interface OfflinePlayerControl {
    fun handle(m: WebMessage)
    /** Re-sends both queues and states (the model is seeded from them). */
    fun emitAll()
}

/** The engine as the offline player drives it. */
class EngineControl(private val engine: PlayerEngine) : OfflinePlayerControl {
    override fun handle(m: WebMessage) = engine.handle(m)
    override fun emitAll() = engine.emitAll()
}

/**
 * The native offline player (port of iOS `OfflinePlayerModel`; the can't-connect screen's Play Offline Favorites): the
 * cached favorites as a list, the music queue, and transport and modes for what the engine is playing. Everything comes
 * from the cache index and the engine's own events ([receive]), the same `state` and `queue` events the web mirrors;
 * nothing touches the network except [retry], which asks the app to load the web page again. Main thread; its state is
 * Compose state, so the screen follows it.
 *
 * [artwork]: a cached track's cover file (`AppServices.cachedArtwork`), null for none; [lyrics]: its cached lyrics.
 */
class OfflinePlayerModel(
    private val player: OfflinePlayerControl,
    favorites: () -> List<ApiTrack>,
    retry: () -> Unit,
    private val artwork: (Long) -> File? = { null },
    lyrics: (Long) -> LyricsDoc? = { null },
) : OfflinePlayer {
    private val favoritesSource = favorites
    private val retryAction = retry
    private val lyricsFor = lyrics

    /** One entry of the queue still to come, with its place in the whole queue (a tap jumps there). */
    data class Upcoming(val index: Int, val item: Item)

    /** The cached favorites, in title order. */
    var favorites: List<Item> by mutableStateOf(emptyList()); private set
    /** The music queue and its current index, as the engine last said. */
    var queue: List<Item> by mutableStateOf(emptyList()); private set
    var index: Int by mutableIntStateOf(0); private set
    var playing: Boolean by mutableStateOf(false); private set
    var buffering: Boolean by mutableStateOf(false); private set
    var positionMs: Long by mutableLongStateOf(0L); private set
    var durationMs: Long by mutableLongStateOf(0L); private set
    var shuffle: Boolean by mutableStateOf(false); private set
    var repeatMode: RepeatMode by mutableStateOf(RepeatMode.off); private set
    /** The engine's reason the music stopped (offline with nothing cached, …), if any. */
    var error: String? by mutableStateOf(null); private set
    /** Retry Connection is waiting for the page. */
    var connecting: Boolean by mutableStateOf(false); private set
    /** The last retry failed: the server still can't be reached. */
    var stillOffline: Boolean by mutableStateOf(false); private set

    private var currentId: String? by mutableStateOf(null)
    private var source = QueueSource.list
    /** Which song [lyrics] were read for (`Unit`: none yet). */
    private var lyricsKey: Any? = Unit

    init { reload() }

    /**
     * The current song's lyrics on the phone; null when none are cached (or it is instrumental). Read once per song, when
     * the song changes ([receive]), never while the screen draws.
     */
    var lyrics: OfflineLyrics? by mutableStateOf(null); private set

    private fun followSong() {
        val id = current?.trackId
        if (id == lyricsKey) return
        lyricsKey = id
        lyrics = id?.let { lyricsFor(it) }?.let { OfflineLyrics.of(it) }
    }

    /** The item's cover on the phone, null when there is none (the screen draws a tile instead). */
    fun artworkFile(item: Item): File? = item.trackId?.let(artwork)

    /** The song the engine is on (paused or playing), null with an empty music queue. */
    val current: Item?
        get() {
            val id = currentId ?: return null
            return queue.getOrNull(index)?.takeIf { it.id == id }
        }

    /** What follows the current song, in the engine's order. */
    val upNext: List<Upcoming>
        get() = if (index + 1 < queue.size) (index + 1 until queue.size).map { Upcoming(it, queue[it]) } else emptyList()

    /** Reads the cache index again (cheap: no network, no waiting). */
    fun reload() {
        favorites = favoritesSource().map { it.toItem() }.sortedWith(titleOrder())
    }

    // MARK: Engine events

    /** The engine's events (forwarded by `AppServices.send`): music only; episodes are left to the web. */
    override fun receive(event: NativeEvent) {
        when (event) {
            is NativeEvent.State -> if (event.kind == Item.Kind.track) {
                currentId = event.itemId
                index = event.index
                playing = event.playing
                buffering = event.buffering
                positionMs = maxOf(0L, event.positionMs)
                durationMs = maxOf(0L, event.durationMs)
                shuffle = event.shuffle
                repeatMode = event.repeat
                error = event.error
            }
            is NativeEvent.Queue -> if (event.kind == Item.Kind.track) {
                queue = event.items
                index = event.index
                source = event.source
                currentId = event.items.getOrNull(event.index)?.id
            }
            else -> {}
        }
        followSong()
    }

    // MARK: Actions

    /** A tap in the favorites list: the list becomes the queue, from that song (the web's `playList`). */
    fun play(item: Item) {
        val i = favorites.indexOfFirst { it.id == item.id }
        if (i < 0) return
        player.handle(WebMessage.SetQueue(Item.Kind.track, favorites, i, 0, true, QueueSource.favorites))
    }

    /** A tap in Up Next: that entry of the same queue plays (the web's `jump`). */
    fun jump(to: Int) {
        if (to !in queue.indices) return
        player.handle(WebMessage.SetQueue(Item.Kind.track, queue, to, 0, true, source))
    }

    fun togglePlay() = player.handle(if (playing) WebMessage.Pause(Item.Kind.track) else WebMessage.Play(Item.Kind.track))
    fun next() = player.handle(WebMessage.Next(Item.Kind.track))
    fun previous() = player.handle(WebMessage.Prev(Item.Kind.track))
    fun seek(ms: Long) = player.handle(WebMessage.Seek(Item.Kind.track, maxOf(0L, ms)))

    fun toggleShuffle() = player.handle(WebMessage.SetModes(!shuffle, repeatMode))

    /** off → all → one → off, as the web's button. */
    fun cycleRepeat() {
        val next = when (repeatMode) {
            RepeatMode.off -> RepeatMode.all
            RepeatMode.all -> RepeatMode.one
            RepeatMode.one -> RepeatMode.off
        }
        player.handle(WebMessage.SetModes(shuffle, next))
    }

    // MARK: Retry Connection

    /** Asks the app to load the web page again; the app closes this player once the page has loaded. */
    fun retry() {
        retryStarted()
        retryAction()
    }

    /** A load of the page began (this player's button, or the app's own retry when the network came back). */
    override fun retryStarted() {
        connecting = true
        stillOffline = false
    }

    /** The page failed again. */
    override fun retryFailed() {
        connecting = false
        stillOffline = true
    }

    companion object {
        /** "1:05", "1:02:05". */
        fun time(ms: Long): String {
            val s = maxOf(0L, ms) / 1000
            val h = s / 3600
            val m = (s % 3600) / 60
            val sec = s % 60
            return if (h > 0) String.format(Locale.ROOT, "%d:%02d:%02d", h, m, sec) else String.format(Locale.ROOT, "%d:%02d", m, sec)
        }

        /**
         * Titles as iOS `localizedStandardCompare` orders them: in the phone's language, letter case aside, numbers by
         * value ("Track 9" before "Track 10"); equal titles by id.
         */
        fun titleOrder(locale: Locale = Locale.getDefault()): Comparator<Item> {
            val collator = Collator.getInstance(locale).apply { strength = Collator.SECONDARY }
            val natural = Comparator<String> { a, b -> naturalCompare(a, b, collator) }
            return compareBy<Item, String>(natural) { it.title }.thenBy { it.id }
        }

        private val CHUNK = Regex("\\d+|\\D+")

        private fun naturalCompare(a: String, b: String, collator: Collator): Int {
            val x = CHUNK.findAll(a).map { it.value }.toList()
            val y = CHUNK.findAll(b).map { it.value }.toList()
            for (i in 0 until minOf(x.size, y.size)) {
                val p = x[i]
                val q = y[i]
                val c = if (p[0].isDigit() && q[0].isDigit()) {
                    p.trimStart('0').length.compareTo(q.trimStart('0').length).takeIf { it != 0 } ?: p.trimStart('0').compareTo(q.trimStart('0'))
                } else {
                    collator.compare(p, q)
                }
                if (c != 0) return c
            }
            return x.size.compareTo(y.size)
        }
    }
}

/** Lyrics as the offline player shows them: timed lines that follow the song, or plain text. */
class OfflineLyrics private constructor(
    /** Timed lines; empty for plain lyrics. */
    val lines: List<LyricsLine>,
    val offsetMs: Long,
    /** Plain lyrics (no timestamps); null for timed ones. */
    val text: String?,
) {
    val synced: Boolean get() = lines.isNotEmpty()

    /** The line playing at [atMs] (its time has come), null before the first. */
    fun index(atMs: Long): Int? {
        val ms = atMs - offsetMs
        var lo = 0
        var hi = lines.size
        while (lo < hi) {
            val mid = (lo + hi) / 2
            if (lines[mid].tMs <= ms) lo = mid + 1 else hi = mid
        }
        return if (lo > 0) lo - 1 else null
    }

    /** Where the song is when line [i] starts (a tap on a line seeks there). */
    fun startMs(i: Int): Long = maxOf(0L, lines[i].tMs + offsetMs)

    /** The first line after [after] (or from the start) with words in it. */
    fun nextWords(after: Int?): Int? {
        val from = (after ?: -1) + 1
        return (from until lines.size).firstOrNull { !blank(lines[it].text) }
    }

    companion object {
        /** null when there is nothing to show: not found, instrumental, or empty. */
        fun of(doc: LyricsDoc): OfflineLyrics? {
            if (!doc.found || doc.instrumental == true) return null
            val offset = doc.offsetMs ?: 0
            val lines = doc.lines
            if (doc.synced && !lines.isNullOrEmpty()) return OfflineLyrics(lines, offset, null)
            val t = doc.text ?: lines?.joinToString("\n") { it.text }
            if (t == null || t.isBlank()) return null
            return OfflineLyrics(emptyList(), offset, t)
        }

        fun blank(s: String): Boolean = s.isBlank()
    }
}
