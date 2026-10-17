package lark.player

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Item
import lark.bridge.Malformed
import lark.bridge.asFields

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
