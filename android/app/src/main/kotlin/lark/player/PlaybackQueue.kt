package lark.player

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.api.ApiTrack
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.asFields

/** One kind's queue: music or 频道 episodes. The engine keeps one of each. */
data class PlaybackQueue(val items: List<Item>, val index: Int, val positionMs: Long, val source: QueueSource) {
    val current: Item? get() = items.getOrNull(index)

    /** The items after the current one. */
    val upcoming: List<Item> get() = if (index + 1 < items.size) items.subList(index + 1, items.size) else emptyList()

    internal fun toJson(): JsonObject = JsonObject(linkedMapOf(
        "items" to JsonArray(items.map { it.toJsonObject() }), "index" to JsonPrimitive(index),
        "positionMs" to JsonPrimitive(positionMs), "source" to JsonPrimitive(source.name)))

    companion object {
        val empty = PlaybackQueue(emptyList(), 0, 0, QueueSource.list)

        internal fun from(e: JsonElement): PlaybackQueue {
            val f = e.asFields()
            return PlaybackQueue(f.array("items").map { Item.from(it) }, f.long("index").toInt(), f.long("positionMs"),
                QueueSource.parse(f.string("source")))
        }
    }
}

/** A music item from the API's track and its raw JSON (kept as `meta`, echoed to the web unchanged). */
fun ApiTrack.toItem(): Item =
    Item(Item.Kind.track, track.id.toString(), track.title, track.artist, track.album, track.durationMs, json)
