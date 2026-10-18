package lark.cache

import android.content.Context
import android.os.storage.StorageManager
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.api.LyricsDoc
import lark.api.Track
import lark.api.lyricsFrom
import lark.api.parseJson
import lark.bridge.Malformed
import lark.bridge.asFields
import java.io.File
import java.io.IOException
import java.net.URI
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.MessageDigest

/** One cached track in `index.json`: `file` is relative to the user's folder (`files/<id>.<ext>`). */
internal data class Entry(
    val file: String,
    val bytes: Long,
    val lastPlayed: Double,          // seconds since 1970
    val favorite: Boolean,
    val track: Track,
    val meta: JsonElement,
)

/** The index as the iOS store writes it: `trackID → {file, bytes, lastPlayed, favorite, track, meta}`, keys sorted. */
internal object CacheIndex {
    fun encode(entries: Map<Long, Entry>): String {
        val map = linkedMapOf<String, JsonElement>()
        for ((id, e) in entries.entries.sortedBy { it.key.toString() }) map[id.toString()] = entryJson(e)
        return JsonObject(map).toString()
    }

    private fun entryJson(e: Entry) = JsonObject(linkedMapOf(
        "bytes" to JsonPrimitive(e.bytes), "favorite" to JsonPrimitive(e.favorite), "file" to JsonPrimitive(e.file),
        "lastPlayed" to JsonPrimitive(e.lastPlayed), "meta" to e.meta, "track" to trackJson(e.track)))

    private fun trackJson(t: Track) = JsonObject(linkedMapOf(
        "album" to JsonPrimitive(t.album), "artist" to JsonPrimitive(t.artist), "duration_ms" to JsonPrimitive(t.durationMs),
        "favorite" to JsonPrimitive(t.favorite), "id" to JsonPrimitive(t.id), "title" to JsonPrimitive(t.title)))

    /** Null when the text is not a readable index: the cache starts empty. */
    fun decode(text: String): Map<Long, Entry>? = try {
        val root = parseJson(text) as? JsonObject ?: throw Malformed("not an object")
        root.entries.associate { (key, v) ->
            val f = v.asFields()
            val t = f.element("track")?.asFields() ?: throw Malformed("track")
            val meta = f.element("meta") ?: JsonNull
            val track = Track(t.long("id"), t.string("title"), t.string("artist"), t.string("album"), t.long("duration_ms"), t.bool("favorite"), gainOf(meta))
            val lastPlayed = (f.element("lastPlayed") as? JsonPrimitive)?.takeIf { !it.isString }?.content?.toDoubleOrNull() ?: throw Malformed("lastPlayed")
            (key.toLongOrNull() ?: throw Malformed("key")) to Entry(f.string("file"), f.long("bytes"), lastPlayed, f.bool("favorite"), track, meta)
        }
    } catch (e: Exception) {
        if (e is kotlinx.coroutines.CancellationException) throw e
        null
    }

    /** The server's measured gain lives in the raw JSON only (iOS reads it from `Item.meta` too). */
    fun gainOf(meta: JsonElement): Double? =
        ((meta as? JsonObject)?.get("gain_db") as? JsonPrimitive)?.takeIf { !it.isString && it !is JsonNull }?.content?.toDoubleOrNull()

    fun lyricsJson(d: LyricsDoc): String {
        val m = linkedMapOf<String, JsonElement>("found" to JsonPrimitive(d.found), "synced" to JsonPrimitive(d.synced))
        d.instrumental?.let { m["instrumental"] = JsonPrimitive(it) }
        d.lines?.let { l -> m["lines"] = JsonArray(l.map { JsonObject(linkedMapOf("t_ms" to JsonPrimitive(it.tMs), "text" to JsonPrimitive(it.text))) }) }
        d.offsetMs?.let { m["offset_ms"] = JsonPrimitive(it) }
        d.text?.let { m["text"] = JsonPrimitive(it) }
        return JsonObject(m).toString()
    }

    fun lyricsFromText(text: String): LyricsDoc? = try { lyricsFrom(parseJson(text)) } catch (e: Exception) {
        if (e is kotlinx.coroutines.CancellationException) throw e
        null
    }
}

/** Where caches live: `filesDir/lark/<serverKey>/<userId>/{files,staging,covers,lyrics,index.json}`. */
object CacheLayout {
    /**
     * The folder name of a server: the first 8 bytes (16 hex digits) of the SHA-256 of its normalised base URL
     * (scheme, lowercased host, port, path without a trailing `/`). Track ids are another server's rows, so each
     * server has its own cache.
     */
    fun serverKey(base: String): String {
        val u = URI(base.trim())
        var path = u.rawPath ?: ""
        while (path.endsWith("/")) path = path.dropLast(1)
        val norm = "${u.scheme?.lowercase() ?: ""}://${u.host?.lowercase() ?: ""}${if (u.port >= 0) ":${u.port}" else ""}$path"
        return MessageDigest.getInstance("SHA-256").digest(norm.toByteArray()).take(8).joinToString("") { "%02x".format(it) }
    }

    /** `<cacheRoot>/<serverKey>/<userId>`. */
    fun userRoot(cacheRoot: File, base: String, userId: Long): File = File(File(cacheRoot, serverKey(base)), userId.toString())

    /** `filesDir/lark`. */
    fun defaultRoot(context: Context): File = File(context.filesDir, "lark")

    /**
     * Only the current server's folder is kept: other servers' folders and the pre-server `<userId>` ones go (the
     * direct children of [cacheRoot] only; a link in it is removed, never followed).
     */
    fun pruneOtherServers(cacheRoot: File, keeping: File) {
        val names = cacheRoot.list() ?: return
        for (n in names) {
            val f = File(cacheRoot, n)
            if (f.name != keeping.name) deleteTree(f)
        }
    }

    /** Deletes a user's whole cache folder (sign-out of that user for good, or another server): `root` and below. */
    fun wipeUser(userRoot: File) = deleteTree(userRoot)

    /** Deletes `f` and below without following symbolic links. */
    internal fun deleteTree(f: File) {
        try {
            if (Files.isSymbolicLink(f.toPath())) { Files.delete(f.toPath()); return }
            if (f.isDirectory) f.listFiles()?.forEach { deleteTree(it) }
            f.delete()
        } catch (_: IOException) {}
    }
}

/** Free bytes for the cache's volume, for opportunistic use; null when unknown (then only the cap counts). */
fun interface FreeSpace {
    fun bytes(): Long?
}

/** `StorageManager.getAllocatableBytes`: free space counting what the system would clear (other apps' caches). */
class StorageManagerFreeSpace(context: Context, private val dir: File) : FreeSpace {
    private val storage = context.applicationContext.getSystemService(StorageManager::class.java)

    override fun bytes(): Long? = try {
        // The folder may not exist yet: the volume is the one of its nearest existing parent.
        var d: File? = dir
        while (d != null && !d.exists()) d = d.parentFile
        d?.let { storage.getAllocatableBytes(storage.getUuidForPath(it)) }
    } catch (_: IOException) { null } catch (_: RuntimeException) { null }
}

/** Writes `bytes` to `dest` through a temporary file beside it and a rename, so `dest` is never half written. */
internal fun writeAtomic(dest: File, bytes: ByteArray) {
    dest.parentFile?.mkdirs()
    val tmp = File(dest.parentFile, dest.name + ".tmp")
    try {
        tmp.writeBytes(bytes)
        moveAtomic(tmp, dest)
    } finally { tmp.delete() }
}

internal fun moveAtomic(from: File, to: File) {
    try {
        Files.move(from.toPath(), to.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
    } catch (_: AtomicMoveNotSupportedException) {
        Files.move(from.toPath(), to.toPath(), StandardCopyOption.REPLACE_EXISTING)
    }
}
