package lark.api

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Malformed
import lark.bridge.asFields
import lark.bridge.lenientJson

// Shapes of the Melarka server's /api/v1 JSON (internal/library, internal/personal, internal/lyrics).
// Decoding ignores unknown fields; the raw object is kept next to each Track as `Item.meta`.

data class Track(
    val id: Long, val title: String, val artist: String, val album: String, val durationMs: Long, val favorite: Boolean,
    /** The loudness gain the server measured (dB, never above 0); null until measured. Players read it from `Item.meta`. */
    val gainDb: Double? = null,
)

/** A track and the server's object it was read from, verbatim (unknown fields kept). */
data class ApiTrack(val track: Track, val json: JsonElement)

data class LyricsLine(val tMs: Long, val text: String)

data class LyricsDoc(
    val found: Boolean, val synced: Boolean, val instrumental: Boolean? = null, val lines: List<LyricsLine>? = null,
    val offsetMs: Long? = null,
    /** Plain (unsynced) lyrics: the server sends `text` when the lyrics have no timestamps. */
    val text: String? = null,
)

data class ServerQueue(val trackIds: List<Long>, val currentIndex: Int, val positionMs: Long, val version: Long, val updatedBy: String, val updatedAt: Long)

data class PlayEvent(val clientEventId: String, val trackId: Long, val startedAt: Long, val playedSeconds: Long, val skipped: Boolean, val quality: String)

/** `GET /api/v1/info`, unauthenticated. */
data class ServerInfo(val name: String, val version: String, val language: String, val offlineCache: Boolean, val channels: Boolean)

data class QueueAnswer(val queue: ServerQueue, val tracks: List<ApiTrack>)
data class RandomFavorites(val source: String, val tracks: List<ApiTrack>)

// MARK: decoding (internal; any mismatch is `LarkError.BadResponse`)

internal inline fun <T> decoding(block: () -> T): T =
    try { block() } catch (e: Exception) {
        if (e is kotlinx.coroutines.CancellationException) throw e
        throw LarkError.BadResponse
    }

internal fun parseJson(text: String): JsonElement = lenientJson.parseToJsonElement(text)

private fun JsonElement.obj(): JsonObject = this as? JsonObject ?: throw Malformed("not an object")

private fun JsonElement?.list(): List<JsonElement> = when (this) {
    null, JsonNull -> emptyList()
    is JsonArray -> this
    else -> throw Malformed("not an array")
}

internal fun trackFrom(e: JsonElement): ApiTrack {
    val f = e.asFields()
    val gain = (e.obj()["gain_db"] as? JsonPrimitive)?.takeIf { it !is JsonNull }?.let {
        if (it.isString) throw Malformed("gain_db") else it.content.toDoubleOrNull() ?: throw Malformed("gain_db")
    }
    return ApiTrack(Track(f.long("id"), f.string("title"), f.string("artist"), f.string("album"), f.long("duration_ms"), f.bool("favorite"), gain), e)
}

internal fun tracksFrom(e: JsonElement?): List<ApiTrack> = e.list().map(::trackFrom)

internal fun queueFrom(e: JsonElement): ServerQueue {
    val f = e.asFields()
    return ServerQueue(f.array("track_ids").map { id ->
        (id as? JsonPrimitive)?.takeIf { !it.isString }?.content?.toLongOrNull() ?: throw Malformed("track_ids") },
        f.long("current_index").toInt(), f.long("position_ms"), f.long("version"), f.string("updated_by"), f.long("updated_at"))
}

internal fun lyricsFrom(e: JsonElement): LyricsDoc {
    val f = e.asFields()
    val lines = f.element("lines")?.let { l -> (l as? JsonArray ?: throw Malformed("lines")).map { x -> x.asFields().let { LyricsLine(it.long("t_ms"), it.string("text")) } } }
    return LyricsDoc(f.bool("found"), f.bool("synced"), f.boolOrNull("instrumental"), lines, f.longOrNull("offset_ms"), f.stringOrNull("text"))
}

internal fun infoFrom(e: JsonElement): ServerInfo {
    val f = e.asFields()
    return ServerInfo(f.stringOrNull("name") ?: "", f.stringOrNull("version") ?: "", f.stringOrNull("language") ?: "en",
        f.boolOrNull("offline_cache") ?: true, f.boolOrNull("channels") ?: false)
}

internal fun PlayEvent.toJson(): JsonObject = JsonObject(linkedMapOf(
    "client_event_id" to JsonPrimitive(clientEventId), "track_id" to JsonPrimitive(trackId), "started_at" to JsonPrimitive(startedAt),
    "played_seconds" to JsonPrimitive(playedSeconds), "skipped" to JsonPrimitive(skipped), "quality" to JsonPrimitive(quality)))
