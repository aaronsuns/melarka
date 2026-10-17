package lark.bridge

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

enum class QueueSource { list, radio, restored, shuffle, favorites;
    companion object { fun parse(s: String): QueueSource = entries.firstOrNull { it.name == s } ?: throw Malformed("bad source") }
}

enum class RepeatMode { off, all, one;
    companion object { fun parse(s: String): RepeatMode = entries.firstOrNull { it.name == s } ?: throw Malformed("bad repeat") }
}

/**
 * One queue entry, a music track or a 频道 episode. `meta` is the web's own Track or Episode object, kept exactly
 * as received and echoed back unchanged (`7` stays `7`, never `7.0`).
 */
data class Item(
    val kind: Kind,
    val id: String,            // track: "123" or "yt:<video id>"; episode: the YouTube video id
    val title: String = "",
    val artist: String = "",   // track artist / episode channel
    val album: String = "",    // track album / "频道"
    val durationMs: Long = 0,
    val meta: JsonElement = JsonNull,
) {
    enum class Kind { track, episode;
        companion object { fun parse(s: String): Kind = entries.firstOrNull { it.name == s } ?: throw Malformed("bad kind") }
    }

    /** The track id, for a music item; null for a `yt:` track and for episodes. */
    val trackId: Long? get() = if (kind == Kind.track) id.toLongOrNull() else null

    /** A YouTube track (id `yt:<video id>`): streamed from its preview, never cached, no lyrics, play events or server queue. */
    val youtubeVideoId: String?
        get() {
            if (kind != Kind.track || !id.startsWith("yt:")) return null
            val v = id.substring(3)
            return if (VIDEO_ID.matches(v)) v else null
        }

    internal fun toJsonObject(): JsonObject = JsonObject(linkedMapOf(
        "kind" to JsonPrimitive(kind.name), "id" to JsonPrimitive(id), "title" to JsonPrimitive(title),
        "artist" to JsonPrimitive(artist), "album" to JsonPrimitive(album), "durationMs" to JsonPrimitive(durationMs), "meta" to meta))

    companion object {
        private val VIDEO_ID = Regex("^[A-Za-z0-9_-]{11}$")

        /** Only `kind` and `id` are required. */
        internal fun from(e: JsonElement): Item {
            val f = e.asFields()
            return Item(
                kind = Kind.parse(f.string("kind")), id = f.string("id"),
                title = f.stringOrNull("title") ?: "", artist = f.stringOrNull("artist") ?: "", album = f.stringOrNull("album") ?: "",
                durationMs = f.lenientLongOrNull("durationMs") ?: 0, meta = f.element("meta") ?: JsonNull)
        }
    }
}
