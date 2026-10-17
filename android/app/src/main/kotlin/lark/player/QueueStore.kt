package lark.player

import lark.api.PlayEvent
import lark.bridge.asFields
import lark.bridge.lenientJson
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardCopyOption

/**
 * Per-user files in one directory: `queues-<userId>.json` and `events-<userId>.json`. With no user nothing is
 * read or written. Writes are atomic (a temp file, then a rename); an unreadable file reads as nothing.
 */
class QueueStore(val directory: File) {
    @Volatile var user: Long? = null

    private fun file(prefix: String): File? = user?.let { File(directory, "$prefix-$it.json") }

    fun load(): QueueSnapshot? {
        val json = read("queues") ?: return null
        return try {
            if (QueueSnapshot.versionOf(json) != QueueSnapshot.CURRENT_VERSION) null   // another schema (or none): no queue
            else QueueSnapshot.from(json)
        } catch (_: Exception) { null }
    }

    fun save(s: QueueSnapshot) = write("queues", s.toJson())

    fun loadEvents(): List<PlayEvent> {
        val json = read("events") as? JsonArray ?: return emptyList()
        return try { json.map(::event) } catch (_: Exception) { emptyList() }
    }

    fun saveEvents(e: List<PlayEvent>) {
        if (e.isEmpty()) file("events")?.delete() else write("events", JsonArray(e.map(::eventJson)))
    }

    /** Deletes the current user's queues and pending events. */
    fun wipe() {
        for (prefix in listOf("queues", "events")) file(prefix)?.delete()
    }

    private fun read(prefix: String): JsonElement? {
        val f = file(prefix) ?: return null
        return try { lenientJson.parseToJsonElement(f.readText(Charsets.UTF_8)) } catch (_: Exception) { null }
    }

    private fun write(prefix: String, v: JsonElement) {
        val f = file(prefix) ?: return
        val tmp = File(directory, ".${f.name}.tmp")
        try {
            directory.mkdirs()
            tmp.writeText(v.toString(), Charsets.UTF_8)
            Files.move(tmp.toPath(), f.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        } catch (_: IOException) {
            tmp.delete()   // could not write: the previous file, if any, stays
        }
    }

    private fun eventJson(e: PlayEvent): JsonObject = JsonObject(linkedMapOf(
        "client_event_id" to JsonPrimitive(e.clientEventId), "track_id" to JsonPrimitive(e.trackId), "started_at" to JsonPrimitive(e.startedAt),
        "played_seconds" to JsonPrimitive(e.playedSeconds), "skipped" to JsonPrimitive(e.skipped), "quality" to JsonPrimitive(e.quality)))

    private fun event(e: JsonElement): PlayEvent {
        val f = e.asFields()
        return PlayEvent(f.string("client_event_id"), f.long("track_id"), f.long("started_at"), f.long("played_seconds"), f.bool("skipped"), f.string("quality"))
    }
}
