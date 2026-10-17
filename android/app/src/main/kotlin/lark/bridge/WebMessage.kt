package lark.bridge

import kotlinx.serialization.json.JsonObject

/** The sleep timer: pause after this many minutes, or at the end of the item playing, or not at all. */
sealed interface SleepRequest {
    data class Minutes(val minutes: Int) : SleepRequest
    data object EndOfTrack : SleepRequest
    data object Cancel : SleepRequest
}

/** `loudness`: play music at its measured loudness gain; on unless the web says otherwise. */
data class NativePrefs(val quality: String, val carLyrics: Boolean, val loudness: Boolean = true)

/**
 * A message the web posts through `window.larkNative.post(msg)`: a JSON object with a `type`. The field names are
 * the contract with the web's `bridge.ts` (see ios-client-design.md "Web → native messages"). Unknown fields are
 * ignored; `auth` never carries the token.
 */
sealed interface WebMessage {
    data class Hello(val onOpen: String) : WebMessage
    /** `positionMs` null: if `items[index]` is the loaded item, keep playing it where it is (an edit). */
    data class SetQueue(val kind: Item.Kind, val items: List<Item>, val index: Int, val positionMs: Long?, val play: Boolean,
                        val source: QueueSource) : WebMessage
    data class Play(val kind: Item.Kind) : WebMessage
    data class Pause(val kind: Item.Kind?) : WebMessage
    data class Next(val kind: Item.Kind) : WebMessage
    data class Prev(val kind: Item.Kind) : WebMessage
    data class Seek(val kind: Item.Kind, val ms: Long) : WebMessage
    data class Skip(val kind: Item.Kind, val ms: Long) : WebMessage
    data class SetRate(val rate: Double) : WebMessage
    data class Stop(val kind: Item.Kind) : WebMessage
    data class SetPrefs(val prefs: NativePrefs) : WebMessage
    data class Auth(val signedIn: Boolean, val userId: Long?) : WebMessage
    data object PauseForWeb : WebMessage
    data class FavoriteChanged(val trackId: Long, val on: Boolean) : WebMessage
    data class LyricsChanged(val trackId: Long) : WebMessage
    data class FlushEvents(val id: String) : WebMessage
    data object OpenSettings : WebMessage
    data class SetModes(val shuffle: Boolean, val repeat: RepeatMode) : WebMessage
    data class SleepTimer(val request: SleepRequest) : WebMessage

    companion object {
        /** The longest sleep timer taken (a day): anything longer is a malformed message. */
        const val MAX_SLEEP_MINUTES = 24 * 60

        /** Decodes a message body from the page; null (drop it) for anything malformed, unknown or of a missing field. */
        fun decode(json: String): WebMessage? = try {
            val root = lenientJson.parseToJsonElement(json)
            if (root !is JsonObject) null else decode(root.asFields(), root)
        } catch (_: Malformed) {
            null
        } catch (_: kotlinx.serialization.SerializationException) {
            null
        } catch (_: IllegalArgumentException) {
            null
        }

        private fun decode(c: Fields, root: JsonObject): WebMessage {
            fun kind() = Item.Kind.parse(c.string("kind"))
            return when (c.string("type")) {
                "hello" -> Hello(c.string("onOpen"))
                "setQueue" -> SetQueue(
                    kind = kind(), items = c.array("items").map { Item.from(it) },
                    index = c.lenientLong("index").toInt(), positionMs = c.lenientLongOrNull("positionMs"),
                    play = c.bool("play"), source = QueueSource.parse(c.string("source")))
                "play" -> Play(kind())
                "pause" -> Pause(c.stringOrNull("kind")?.let { Item.Kind.parse(it) })
                "next" -> Next(kind())
                "prev" -> Prev(kind())
                "seek" -> Seek(kind(), c.lenientLong("ms"))
                "skip" -> Skip(kind(), c.lenientLong("ms"))
                "setRate" -> {
                    val p = root["rate"] as? kotlinx.serialization.json.JsonPrimitive
                    val rate = if (p == null || p.isString) null else p.content.toDoubleOrNull()
                    if (rate == null || !rate.isFinite() || rate <= 0) throw Malformed("bad rate")
                    SetRate(rate)
                }
                "stop" -> Stop(kind())
                "setPrefs" -> SetPrefs(NativePrefs(c.string("quality"), c.bool("carLyrics"), c.boolOrNull("loudness") ?: true))
                "auth" -> Auth(c.bool("signedIn"), c.longOrNull("userId"))   // a stray `token` field is dropped on purpose
                "pauseForWeb" -> PauseForWeb
                "favoriteChanged" -> FavoriteChanged(c.long("trackId"), c.bool("on"))
                "lyricsChanged" -> LyricsChanged(c.long("trackId"))
                "flushEvents" -> FlushEvents(c.string("id"))
                "openSettings" -> OpenSettings
                "setModes" -> SetModes(c.bool("shuffle"), RepeatMode.parse(c.string("repeat")))
                "sleepTimer" -> SleepTimer(
                    if (c.boolOrNull("cancel") == true) SleepRequest.Cancel
                    else if (c.boolOrNull("endOfTrack") == true) SleepRequest.EndOfTrack
                    else {
                        val m = c.longOrNull("minutes")
                        if (m == null || m !in 1..MAX_SLEEP_MINUTES) throw Malformed("bad sleep timer")
                        SleepRequest.Minutes(m.toInt())
                    })
                else -> throw Malformed("unknown message type")
            }
        }
    }
}
