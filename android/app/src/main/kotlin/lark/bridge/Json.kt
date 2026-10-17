package lark.bridge

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/** A message or item that does not fit the protocol: the caller drops it. */
internal class Malformed(message: String) : Exception(message)

internal val lenientJson = Json { ignoreUnknownKeys = true; isLenient = true; explicitNulls = true }

/** A JSON object read with the iOS decoder's rules: strict types, absent and `null` both mean "not present". */
internal class Fields(private val o: JsonObject) {
    private fun raw(key: String): JsonPrimitive? = when (val v = o[key]) {
        null, JsonNull -> null
        is JsonPrimitive -> v
        else -> throw Malformed("$key: not a scalar")
    }

    fun has(key: String): Boolean = o[key].let { it != null && it != JsonNull }
    fun element(key: String): JsonElement? = o[key]?.takeIf { it != JsonNull }

    fun stringOrNull(key: String): String? = raw(key)?.let { if (it.isString) it.content else throw Malformed("$key: not a string") }
    fun string(key: String): String = stringOrNull(key) ?: throw Malformed("missing $key")

    fun boolOrNull(key: String): Boolean? = raw(key)?.let {
        if (it.isString) throw Malformed("$key: not a bool")
        when (it.content) { "true" -> true; "false" -> false; else -> throw Malformed("$key: not a bool") }
    }
    fun bool(key: String): Boolean = boolOrNull(key) ?: throw Malformed("missing $key")

    /** An integer; a whole number written with a fraction part (`3.0`) is accepted, `3.5` is not. */
    fun longOrNull(key: String): Long? = raw(key)?.let { p ->
        if (p.isString) throw Malformed("$key: not a number")
        p.content.toLongOrNull() ?: p.content.toDoubleOrNull()?.takeIf { it.isFinite() && it == Math.rint(it) && Math.abs(it) < 9e18 }?.toLong()
            ?: throw Malformed("$key: not an integer")
    }
    fun long(key: String): Long = longOrNull(key) ?: throw Malformed("missing $key")

    /** Milliseconds from JS may be fractional (`currentTime * 1000`): rounded half away from zero instead of failing. */
    fun lenientLongOrNull(key: String): Long? = raw(key)?.let { p ->
        if (p.isString) throw Malformed("$key: not a number")
        p.content.toLongOrNull() ?: run {
            val d = p.content.toDoubleOrNull() ?: throw Malformed("$key: not a number")
            if (!d.isFinite() || Math.abs(d) >= 1e15) throw Malformed("$key: number out of range")
            (if (d < 0) -Math.floor(-d + 0.5) else Math.floor(d + 0.5)).toLong()
        }
    }
    fun lenientLong(key: String): Long = lenientLongOrNull(key) ?: throw Malformed("missing $key")

    fun array(key: String): JsonArray = element(key) as? JsonArray ?: throw Malformed("$key: not an array")
}

internal fun JsonElement.asFields(): Fields = Fields(this as? JsonObject ?: throw Malformed("not an object"))

/**
 * Encodes the way iOS's JSONEncoder does, so the page sees the same bytes: `/` is written `\/`, control characters
 * as `\u00XX`, everything else (CJK, U+2028) literally; a number that is not finite is written as a string.
 */
internal fun StringBuilder.appendJson(e: JsonElement): StringBuilder {
    when (e) {
        is JsonNull -> append("null")
        is JsonObject -> {
            append('{')
            var first = true
            for ((k, v) in e) { if (!first) append(','); first = false; appendJsonString(k).append(':').appendJson(v) }
            append('}')
        }
        is JsonArray -> {
            append('[')
            e.forEachIndexed { i, v -> if (i > 0) append(','); appendJson(v) }
            append(']')
        }
        is JsonPrimitive -> when {
            e.isString -> appendJsonString(e.content)
            e.content == "NaN" || e.content == "Infinity" || e.content == "-Infinity" -> appendJsonString(e.content)
            else -> append(e.content)
        }
    }
    return this
}

internal fun StringBuilder.appendJsonString(s: String): StringBuilder {
    append('"')
    for (c in s) when {
        c == '"' -> append("\\\"")
        c == '\\' -> append("\\\\")
        c == '/' -> append("\\/")
        c == '\n' -> append("\\n")
        c == '\r' -> append("\\r")
        c == '\t' -> append("\\t")
        c == '\b' -> append("\\b")
        c == '\u000C' -> append("\\f")
        c < ' ' -> append("\\u00").append("%02x".format(c.code))
        else -> append(c)
    }
    return append('"')
}

/** The member `key` of an object; null for a missing key or a non-object (iOS `JSONValue` subscript). */
operator fun JsonElement.get(key: String): JsonElement? = (this as? JsonObject)?.get(key)

/** An object with `key` set to `value` (in place when it is there, else appended); a non-object becomes `{key: value}`. */
fun JsonElement.setting(key: String, value: JsonElement): JsonObject {
    val o = LinkedHashMap<String, JsonElement>((this as? JsonObject) ?: emptyMap())
    o[key] = value
    return JsonObject(o)
}
