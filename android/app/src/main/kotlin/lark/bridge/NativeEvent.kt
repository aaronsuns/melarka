package lark.bridge

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * An event native dispatches to the page as the `lark-native` CustomEvent on `window`; `detail` is `toJson()`:
 * - `{type:"state", kind, itemId, index, playing, positionMs, durationMs, buffering, error, rate, shuffle, repeat,
 *   sleepRemainingMs}`: `itemId`, `error` and `sleepRemainingMs` are explicit nulls, never absent keys (the web
 *   tells an app that runs the timer, or has modes, by the key)
 * - `{type:"queue", kind, items, index, source}`, `{type:"flushed", id}`, `{type:"authRequired"}`, `{type:"notice", text}`
 */
sealed interface NativeEvent {
    /** The `state` event: where one kind's player is. An episode's state always says shuffle false and repeat off. */
    data class State(
        val kind: Item.Kind,
        val itemId: String?,
        val index: Int,
        val playing: Boolean,
        val positionMs: Long,
        val durationMs: Long,
        val buffering: Boolean,
        val error: String?,
        val rate: Double,
        val shuffle: Boolean = false,
        val repeat: RepeatMode = RepeatMode.off,
        /** The sleep timer's time left; null with no timer. */
        val sleepRemainingMs: Long? = null,
    ) : NativeEvent

    data class Queue(val kind: Item.Kind, val items: List<Item>, val index: Int, val source: QueueSource) : NativeEvent
    data class Flushed(val id: String) : NativeEvent
    data object AuthRequired : NativeEvent
    data class Notice(val text: String) : NativeEvent

    /** The event as JSON, key order as iOS writes it. */
    fun toJson(): String = StringBuilder().appendJson(toElement()).toString()
}

/** Swift writes the double 1 as `1`, not `1.0`. */
private fun number(d: Double): JsonPrimitive = if (d == Math.rint(d) && Math.abs(d) < 1e15) JsonPrimitive(d.toLong()) else JsonPrimitive(d)

private fun str(s: String?): JsonElement = if (s == null) JsonNull else JsonPrimitive(s)

private fun NativeEvent.toElement(): JsonObject = when (this) {
    is NativeEvent.State -> JsonObject(linkedMapOf(
        "type" to str("state"), "kind" to str(kind.name), "itemId" to str(itemId), "index" to JsonPrimitive(index),
        "playing" to JsonPrimitive(playing), "positionMs" to JsonPrimitive(positionMs), "durationMs" to JsonPrimitive(durationMs),
        "buffering" to JsonPrimitive(buffering), "error" to str(error),
        "rate" to number(if (rate.isFinite()) rate else 1.0),
        "shuffle" to JsonPrimitive(shuffle), "repeat" to str(repeat.name),
        "sleepRemainingMs" to (sleepRemainingMs?.let { JsonPrimitive(it) } ?: JsonNull)))
    is NativeEvent.Queue -> JsonObject(linkedMapOf(
        "type" to str("queue"), "kind" to str(kind.name), "items" to JsonArray(items.map { it.toJsonObject() }),
        "index" to JsonPrimitive(index), "source" to str(source.name)))
    is NativeEvent.Flushed -> JsonObject(linkedMapOf("type" to str("flushed"), "id" to str(id)))
    NativeEvent.AuthRequired -> JsonObject(linkedMapOf("type" to str("authRequired")))
    is NativeEvent.Notice -> JsonObject(linkedMapOf("type" to str("notice"), "text" to str(text)))
}

/**
 * `window.dispatchEvent(new CustomEvent("lark-native",{detail:<json>}))`, safe to evaluate as a script:
 * U+2028, U+2029 and `</` are escaped, a non-finite number is written as a string.
 */
fun dispatchScript(event: NativeEvent): String {
    val json = event.toJson()
        .replace(" ", "\\u2028")
        .replace(" ", "\\u2029")
        .replace("</", "<\\/")
    return "window.dispatchEvent(new CustomEvent(\"lark-native\",{detail:$json}))"
}
