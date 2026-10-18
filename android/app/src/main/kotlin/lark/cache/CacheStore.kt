package lark.cache

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.api.ApiTrack
import lark.api.LarkApiProtocol
import lark.api.LarkError
import lark.api.LyricsDoc
import lark.api.Track
import lark.bridge.Item
import lark.core.Cancellable
import lark.core.Clock
import lark.core.NetworkStatus
import lark.core.Scheduler
import lark.player.toItem
import java.io.File
import java.io.IOException
import java.nio.file.Files

/**
 * One user's offline cache: `filesDir/lark/<serverKey>/<userId>/` (port of iOS `CacheStore`). Used from the main
 * thread; downloads run on [scope] (the application's main-thread scope), their transfer on IO inside `api.download`.
 *
 * - `files/<id>.<ext>`: the audio, always the `high` tier (AAC 256k, the web's `OFFLINE_QUALITY`).
 * - `staging/`: downloads in progress (`LarkApi.download` writes `<id>.part` there), moved into `files/` by [store],
 *   a rename on the same volume, so a file in `files/` is always complete. Emptied at every start.
 * - `index.json`: `trackId → {file, bytes, lastPlayed, favorite, track, meta}`, written atomically (temporary file and
 *   rename). A corrupt one is an empty cache. Writes are coalesced: at most one per [indexDelaySeconds] (a 2,000-entry
 *   index is about 1 MB), and [flush] writes at once (backgrounding, [close], the end of a background run). A crash
 *   loses at most that much recency; a file stored but not yet indexed is an orphan, deleted at the next start.
 *
 * LRU with favorites last: over the cap, the least recently played non-favorite goes first, then the least recently
 * played favorite. Files that are gone are dropped from the index when noticed ([localFile], [cachedFavorites], start),
 * and the directories are made again on the next store. Nothing outside [root] is ever deleted.
 *
 * Disk space: a download starts only while the volume keeps [RESERVE_BYTES] free beside it ([FreeSpace]), and an
 * out-of-space error stops the running sync ([outOfSpace]).
 */
class CacheStore(
    val root: File,
    val api: LarkApiProtocol,
    private val network: NetworkStatus,
    private val clock: Clock,
    private val scope: CoroutineScope,
    private val scheduler: Scheduler,
    private val freeSpace: FreeSpace = FreeSpace { null },
    capBytes: Long = DEFAULT_CAP,
    private val indexDelaySeconds: Double = INDEX_WRITE_DELAY_S,
    /** The rename that publishes a file (tests make it fail). */
    private val move: (File, File) -> Unit = ::moveAtomic,
) : CacheProviding {
    class Download internal constructor(val job: Job, /** False: another caller's download of the same track, which only its owner may cancel. */ val created: Boolean)

    private val entries = LinkedHashMap<Long, Entry>()
    /** The server's favorites as last seen ([setFavorites]); null until then, when the track's own flag counts. */
    private var knownFavorites: MutableSet<Long>? = null
    private val inFlight = HashMap<Long, Job>()
    /** Downloads holding a slot right now. */
    var activeDownloads = 0
        private set
    /** Bumped by [clear] and [close]: a download started before it is dropped when it finishes. */
    private var epoch = 0
    private var closed = false
    private var dirty = false
    private var pendingWrite: Cancellable? = null
    /** Index files written (tests). */
    var indexWrites = 0
        private set
    /** An out-of-space error since the last [resetOutOfSpace]: the sync starts nothing more. */
    var outOfSpace = false
        private set
    private val slots = Semaphore(MAX_CONCURRENT_DOWNLOADS)

    /** Settable from Settings; setting it evicts down to it at once. */
    var capBytes: Long = capBytes
        set(value) { field = value; evict() }
    val usedBytes: Long get() = entries.values.sumOf { it.bytes }
    val favoriteBytes: Long get() = entries.values.sumOf { if (it.favorite) it.bytes else 0L }
    val isDownloading: Boolean get() = inFlight.isNotEmpty()

    private val indexFile = File(root, "index.json")
    private val filesDir = File(root, "files")
    private val stagingDir = File(root, "staging")
    /** `covers/<id>`: a cached track's cover (the offline player's artwork); an empty file marks a track with none. */
    private val coversDir = File(root, "covers")
    /** `lyrics/<id>`: a cached track's lyrics as the server sent them; an empty file marks a track with none. */
    private val lyricsDir = File(root, "lyrics")

    init { load() }

    // MARK: CacheProviding

    override fun localFile(trackId: Long): File? {
        val e = entries[trackId] ?: return null
        val f = File(root, e.file)
        if (!f.exists()) {
            entries.remove(trackId)
            removeExtras(trackId)
            markDirty()
            return null
        }
        return f
    }

    override fun touch(trackId: Long) {
        val e = entries[trackId] ?: return
        entries[trackId] = e.copy(lastPlayed = seconds())
        markDirty()
    }

    override fun cachedFavorites(): List<ApiTrack> {
        val gone = mutableListOf<Long>()
        val out = mutableListOf<ApiTrack>()
        for ((id, e) in entries.entries.sortedBy { it.key }) {
            if (!e.favorite) continue
            if (File(root, e.file).exists()) out.add(ApiTrack(e.track, e.meta)) else gone.add(id)
        }
        if (gone.isNotEmpty()) {
            gone.forEach { entries.remove(it); removeExtras(it) }
            markDirty()
        }
        return out
    }

    /** The lookahead. Any network will do (cellular too: it is 2 tracks); none while offline or signed out. */
    override fun prefetch(items: List<Item>) {
        if (closed || !network.isOnline || api.token == null) return
        for (item in items) {
            val id = item.trackId ?: continue
            val track = Track(id, item.title, item.artist, item.album, item.durationMs,
                (item.meta as? JsonObject)?.get("favorite") == JsonPrimitive(true), CacheIndex.gainOf(item.meta))
            download(track, item.meta)
        }
    }

    // MARK: Store, flags, eviction

    /**
     * Moves [file] into the cache as this track's audio (replacing an older copy), then evicts down to the cap;
     * the track just stored is the last to go.
     */
    @Throws(IOException::class)
    fun store(trackId: Long, file: File, track: Track, meta: JsonElement, favorite: Boolean) {
        filesDir.mkdirs()
        val ext = file.extension.ifEmpty { "audio" }
        val name = "files/$trackId.$ext"
        val dest = File(root, name)
        entries[trackId]?.takeIf { it.file != name }?.let { delete(File(root, it.file)) }
        try {
            move(file, dest)
        } catch (ex: IOException) {
            // Out of space may pass (the sync stops and a later one retries it); anything else is not coming back.
            if (!isOutOfSpace(ex)) delete(file)
            throw ex
        }
        val (t, m) = flagged(track, meta, favorite)
        entries[trackId] = Entry(name, dest.length(), seconds(), favorite, t, m)
        evict(keeping = trackId, save = false)
        markDirty()
    }

    /** The server's favorites: flags only. An unfavorited file stays, as an ordinary LRU entry. */
    fun setFavorites(ids: Set<Long>) {
        knownFavorites = ids.toMutableSet()
        var changed = false
        for (id in entries.keys.toList()) {
            val on = id in ids
            if (entries[id]?.favorite != on) { setFlag(id, on); changed = true }
        }
        if (changed) markDirty()
    }

    /** One favorite toggled in the web: flagged at once, before any sync (an offline shuffle respects it). */
    fun markFavorite(id: Long, on: Boolean) {
        if (on) knownFavorites?.add(id) else knownFavorites?.remove(id)
        val e = entries[id] ?: return
        if (e.favorite == on) return
        setFlag(id, on)
        markDirty()
    }

    /** Newer metadata for tracks already cached (a favorites list): the index keeps what the web would get. */
    fun refresh(tracks: List<ApiTrack>) {
        var changed = false
        for (t in tracks) {
            val e = entries[t.track.id] ?: continue
            val (nt, nm) = flagged(t.track, t.json, e.favorite)
            if (e.track != nt || e.meta != nm) { entries[t.track.id] = e.copy(track = nt, meta = nm); changed = true }
        }
        if (changed) markDirty()
    }

    /** Deletes every file and the index; downloads in flight are dropped. */
    fun clear() {
        dropDownloads()
        entries.clear()
        pendingWrite?.cancel(); pendingWrite = null; dirty = false
        for (f in listOf(filesDir, stagingDir, coversDir, lyricsDir, indexFile)) delete(f)
        if (!closed) onChange?.invoke()
    }

    /** Sign-out or another user: this cache stays on disk for its user's next sign-in, and does nothing more. */
    fun close() {
        dropDownloads()
        flush()
        closed = true
    }

    /** Sign-out for good (or another server): the cache closes and the user's whole folder is deleted. */
    fun wipe() {
        close()
        entries.clear()
        CacheLayout.wipeUser(root)
    }

    /** Writes the index now if anything changed since the last write. */
    fun flush() {
        pendingWrite?.cancel(); pendingWrite = null
        if (!dirty) return
        dirty = false
        writeIndex()
    }

    /** Room on the volume for `bytes` more, keeping [RESERVE_BYTES] free. Unknown free space only counts the cap. */
    fun hasRoom(bytes: Long): Boolean {
        val free = freeSpace.bytes() ?: return true
        return free - bytes >= RESERVE_BYTES
    }

    fun resetOutOfSpace() { outOfSpace = false }

    private fun dropDownloads() {
        epoch += 1
        inFlight.values.toList().forEach { it.cancel() }
        inFlight.clear()
    }

    private fun setFlag(id: Long, on: Boolean) {
        val e = entries[id] ?: return
        val (t, m) = flagged(e.track, e.meta, on)
        entries[id] = e.copy(favorite = on, track = t, meta = m)
    }

    /** The track's and the raw JSON's `favorite` follow the flag, so a cached favorite reaches the web as one. */
    private fun flagged(t: Track, m: JsonElement, on: Boolean): Pair<Track, JsonElement> {
        val nt = if (t.favorite != on) t.copy(favorite = on) else t
        val nm = if (m is JsonObject && m["favorite"] != JsonPrimitive(on)) JsonObject(LinkedHashMap(m).apply { put("favorite", JsonPrimitive(on)) }) else m
        return nt to nm
    }

    private fun evict(keeping: Long? = null, save: Boolean = true) {
        var used = usedBytes
        if (used <= capBytes) return
        val order = entries.entries.filter { it.key != keeping }.sortedWith(
            compareBy<Map.Entry<Long, Entry>> { it.value.favorite }.thenBy { it.value.lastPlayed }.thenBy { it.key })
        for ((id, e) in order) {
            if (used <= capBytes) break
            delete(File(root, e.file))
            removeExtras(id)
            entries.remove(id)
            used -= e.bytes
        }
        if (save) markDirty()
    }

    // MARK: Downloads

    /**
     * Fetches one track into the cache unless it is there or already coming. At most 2 run at once; the rest
     * wait for a slot, and [shouldStart] is asked when the slot is theirs (the sync's network and cap checks).
     * Returns the download's job and whether this call created it; null when there is nothing to do.
     */
    fun download(track: Track, meta: JsonElement, favorite: Boolean? = null, shouldStart: () -> Boolean = { true }): Download? {
        val id = track.id
        inFlight[id]?.let { return Download(it, false) }
        if (closed || localFile(id) != null) return null
        val e = epoch
        // Started only once it is in the map: a job that ends at once still leaves it.
        val job = scope.launch(start = CoroutineStart.LAZY) {
            slots.withPermit { fetch(track, meta, favorite, e, shouldStart) }
        }
        inFlight[id] = job
        job.invokeOnCompletion { if (epoch == e && inFlight[id] === job) inFlight.remove(id) }
        job.start()
        return Download(job, true)
    }

    private suspend fun fetch(track: Track, meta: JsonElement, favorite: Boolean?, e: Int, shouldStart: () -> Boolean) {
        if (!currentCoroutineContext().isActive || epoch != e || !network.isOnline || !shouldStart() ||
            !hasRoom(estimatedBytes(track)) || localFile(track.id) != null) return
        activeDownloads += 1
        try {
            try {
                val file = api.download(track.id, QUALITY, stagingDir)
                // A finished download is kept even if its caller gave up meanwhile; not after a clear or a sign-out.
                if (epoch != e) { delete(file); return }
                val fav = knownFavorites?.contains(track.id) ?: favorite ?: track.favorite
                store(track.id, file, track, meta, fav)
            } catch (ex: CancellationException) {
                throw ex
            } catch (ex: Exception) {
                if (isOutOfSpace(ex)) outOfSpace = true
                return
            }
            cacheCover(track.id)
            cacheLyrics(track.id)
        } finally { activeDownloads -= 1 }
    }

    // MARK: Covers

    private fun coverFile(id: Long) = File(coversDir, id.toString())
    private fun lyricsFile(id: Long) = File(lyricsDir, id.toString())

    /** The cover is on disk, or the server said it has none within [NO_COVER_TTL_S] (the marker's date is ours: the clock). */
    private fun coverKnown(id: Long) = known(coverFile(id))

    /** A non-empty file, or an empty "none" marker younger than [NO_COVER_TTL_S]. */
    private fun known(f: File): Boolean {
        if (!f.exists()) return false
        if (f.length() > 0) return true
        return (clock.now().toEpochMilli() - f.lastModified()) / 1000.0 < NO_COVER_TTL_S
    }

    /** A cached track's cover on disk; null when the track is not cached, its cover was not fetched yet, or it has none. */
    fun artworkFile(trackId: Long): File? {
        if (entries[trackId] == null) return null
        return coverFile(trackId).takeIf { it.length() > 0 }
    }

    /**
     * Fetches a cached track's cover once (after its download; the favorites sync for tracks cached before covers
     * were kept). Best effort: a network error leaves it for the next time; a track with no cover (404) gets an
     * empty marker, so it is not asked again for [NO_COVER_TTL_S]. Covers are small and do not count against the cap.
     */
    suspend fun cacheCover(trackId: Long) {
        if (closed) return
        val e = entries[trackId] ?: return
        if (coverKnown(trackId)) return
        val ep = epoch
        var data = try {
            api.artwork(ApiTrack(e.track, e.meta).toItem())
        } catch (ex: CancellationException) {
            throw ex
        } catch (ex: LarkError.Http) {
            if (ex.status == 404) ByteArray(0) else return
        } catch (ex: Exception) {
            return
        }
        if (epoch != ep || closed || entries[trackId] == null) return
        if (data.size > MAX_COVER_BYTES) data = ByteArray(0)
        writeMarked(coverFile(trackId), data)
    }

    /** Cached tracks among [ids] whose cover was never fetched, or whose "no cover" has expired. */
    fun missingCovers(ids: List<Long>): List<Long> = ids.filter { entries[it] != null && !coverKnown(it) }

    /** A track leaves the cache: its cover and lyrics go with it. */
    private fun removeExtras(id: Long) {
        delete(coverFile(id))
        delete(lyricsFile(id))
    }

    /** `data` empty: the "none" marker, dated now. */
    private fun writeMarked(f: File, data: ByteArray) {
        try {
            writeAtomic(f, data, move)
            if (data.isEmpty()) f.setLastModified(clock.now().toEpochMilli())
        } catch (ex: IOException) {
            if (isOutOfSpace(ex)) outOfSpace = true
        }
    }

    // MARK: Lyrics

    /** The lyrics are on disk, or the server said it has none within [NO_COVER_TTL_S] (lyrics found later show up then). */
    private fun lyricsKnown(id: Long) = known(lyricsFile(id))

    override fun cachedLyrics(trackId: Long): LyricsDoc? {
        if (entries[trackId] == null) return null
        val text = try { lyricsFile(trackId).takeIf { it.exists() }?.readText() } catch (_: IOException) { null }
        if (text.isNullOrEmpty()) return null
        return CacheIndex.lyricsFromText(text)
    }

    /**
     * Fetches a cached track's lyrics once, like its cover (after the download; the favorites sync for tracks
     * cached before lyrics were kept). A network error leaves it for next time; none found gets an empty marker.
     */
    suspend fun cacheLyrics(trackId: Long) {
        if (closed || entries[trackId] == null || lyricsKnown(trackId)) return
        val ep = epoch
        var data = ByteArray(0)
        try {
            val doc = api.lyrics(trackId)
            if (doc.found) data = CacheIndex.lyricsJson(doc).toByteArray()
        } catch (ex: CancellationException) {
            throw ex
        } catch (ex: LarkError.Http) {
            if (ex.status != 404) return
        } catch (ex: Exception) {
            return
        }
        if (epoch != ep) return
        writeLyrics(trackId, data)
    }

    override fun updateLyrics(trackId: Long, doc: LyricsDoc) {
        writeLyrics(trackId, if (doc.found) CacheIndex.lyricsJson(doc).toByteArray() else ByteArray(0))
    }

    private fun writeLyrics(id: Long, data: ByteArray) {
        if (closed || entries[id] == null) return
        writeMarked(lyricsFile(id), data)
    }

    /** Cached tracks among [ids] whose lyrics were never fetched, or whose "none" has expired. */
    fun missingLyrics(ids: List<Long>): List<Long> = ids.filter { entries[it] != null && !lyricsKnown(it) }

    // MARK: Disk

    private fun load() {
        try {
            val text = try { indexFile.takeIf { it.exists() }?.readText() } catch (_: IOException) { null } ?: return
            val map = CacheIndex.decode(text) ?: return   // unreadable: starting empty
            var dropped = false
            for ((id, e) in map) {
                if (!e.file.startsWith("files/") || e.file.contains("..") || !File(root, e.file).exists()) { dropped = true; continue }
                entries[id] = e
            }
            if (dropped) markDirty()
            evict()
        } finally {
            delete(stagingDir)       // partial downloads of an earlier run
            removeOrphans()
        }
    }

    /** Files no index entry accounts for (the index was lost or unreadable) would count against nothing. */
    private fun removeOrphans() {
        filesDir.list()?.let { names ->
            val known = entries.values.map { it.file }.toSet()
            for (n in names) if ("files/$n" !in known) delete(File(filesDir, n))
        }
        for (dir in listOf(coversDir, lyricsDir)) {
            for (n in dir.list() ?: continue) if (n.toLongOrNull()?.let { entries[it] == null } != false) delete(File(dir, n))
        }
    }

    /** Runs after anything in the cache changed (a store, an eviction, a clear, a favorite): the screens follow it. */
    var onChange: (() -> Unit)? = null

    /** Something changed: the index is written once the delay has passed (or at the next [flush]). */
    private fun markDirty() {
        if (closed) return
        onChange?.invoke()
        dirty = true
        if (pendingWrite != null) return
        pendingWrite = scheduler.after(indexDelaySeconds) { pendingWrite = null; flush() }
    }

    private fun writeIndex() {
        indexWrites += 1
        try {
            writeAtomic(indexFile, CacheIndex.encode(entries).toByteArray(), move)
        } catch (ex: IOException) {
            if (isOutOfSpace(ex)) outOfSpace = true
        }
    }

    private fun seconds(): Double = clock.now().let { it.epochSecond + it.nano / 1e9 }

    /** Deletes [f] and below, but only inside [root] (and never [root] itself): nothing else is the cache's to delete. */
    private fun delete(f: File) {
        val base = root.canonicalFile.toPath()
        val target = try {
            // The parent's real path, so a link at `f` itself is removed rather than followed.
            f.absoluteFile.parentFile?.canonicalFile?.toPath()?.resolve(f.name) ?: return
        } catch (_: IOException) { return }
        if (target == base || !target.startsWith(base)) return
        if (!Files.exists(target, java.nio.file.LinkOption.NOFOLLOW_LINKS)) return
        CacheLayout.deleteTree(target.toFile())
    }

    companion object {
        const val QUALITY = "high"
        const val MAX_CONCURRENT_DOWNLOADS = 2
        const val DEFAULT_CAP: Long = 2L shl 30
        /** The caps Settings offers, in GB (default 2). */
        val CAP_CHOICES_GB = listOf(1, 2, 5, 10)
        /** The index is written at most this often (seconds); [flush] writes at once. */
        const val INDEX_WRITE_DELAY_S = 5.0
        /** Free space a download must leave on the volume. */
        const val RESERVE_BYTES: Long = 500L shl 20
        /** AAC 256k: bytes per second of audio, to estimate a download before it is made. */
        const val BYTES_PER_SECOND = 32_000L
        /** A cover bigger than this is not kept (the server's are 300 px). */
        const val MAX_COVER_BYTES = 2 shl 20
        /** How long "no cover" (or "no lyrics") is believed: after this a sync asks again. */
        const val NO_COVER_TTL_S = 7.0 * 24 * 3600

        /** A track's size before it is fetched: its duration at AAC 256k, at least a minute's worth. */
        fun estimatedBytes(t: Track): Long = maxOf(60L, t.durationMs / 1000) * BYTES_PER_SECOND

        /** ENOSPC in any of its forms, also as an underlying error. */
        fun isOutOfSpace(error: Throwable): Boolean {
            var e: Throwable? = error
            var n = 0
            while (e != null && n++ < 10) {
                val m = e.message ?: ""
                if (m.contains("ENOSPC") || m.contains("No space left on device")) return true
                e = e.cause
            }
            return false
        }
    }
}
