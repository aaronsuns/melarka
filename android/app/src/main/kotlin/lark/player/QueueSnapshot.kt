package lark.player

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Item
import lark.bridge.Malformed
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

/**
 * Everything the engine persists for one user: both queues, which one is active, when they last changed
 * (`savedAt`, unix seconds) and the server's name for this device (`ownDevice`, from our own `PUT /queue`).
 * `version` is the schema: a file with another version reads as no queue, never as a crash.
 * `modes` (shuffle and repeat) came later: a file without it reads as both off, still version 1.
 */
data class QueueSnapshot(
    val version: Int = CURRENT_VERSION,
    val music: PlaybackQueue,
    val episodes: PlaybackQueue,
    val active: Item.Kind,
    val savedAt: Long,
    val ownDevice: String? = null,
    val modes: PlayModes? = null,
) {
    internal fun toJson(): JsonObject {
        val m = linkedMapOf<String, JsonElement>(
            "version" to JsonPrimitive(version), "music" to music.toJson(), "episodes" to episodes.toJson(),
            "active" to JsonPrimitive(active.name), "savedAt" to JsonPrimitive(savedAt))
        ownDevice?.let { m["ownDevice"] = JsonPrimitive(it) }
        modes?.let { m["modes"] = it.toJson() }
        return JsonObject(m)
    }

    companion object {
        const val CURRENT_VERSION = 1

        /** The schema number of a snapshot JSON, or null when it has none. */
        internal fun versionOf(e: JsonElement): Int? = e.asFields().longOrNull("version")?.toInt()

        internal fun from(e: JsonElement): QueueSnapshot {
            val f = e.asFields()
            val modes = f.element("modes")?.let { try { PlayModes.from(it) } catch (_: Malformed) { null } }   // a damaged `modes` reads as none
            return QueueSnapshot(
                version = f.longOrNull("version")?.toInt() ?: CURRENT_VERSION,
                music = PlaybackQueue.from(f.element("music") ?: throw Malformed("missing music")),
                episodes = PlaybackQueue.from(f.element("episodes") ?: throw Malformed("missing episodes")),
                active = Item.Kind.parse(f.string("active")), savedAt = f.long("savedAt"),
                ownDevice = f.stringOrNull("ownDevice"), modes = modes)
        }
    }
}

