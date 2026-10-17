package lark.player

import lark.bridge.Fields
import lark.bridge.Malformed
import lark.bridge.RepeatMode
import lark.bridge.asFields
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Shuffle and repeat for the music queue, mirroring the web's `web/src/player/modes.ts` (same rules, same
 * fixtures in the tests). Episodes ignore them. Saved with the queues ([QueueSnapshot.modes]), per device.
 *
 * - Shuffle on: everything after the current track is shuffled; `original` remembers the upcoming ids in
 *   their order, so shuffle off can restore it. Tracks added while shuffled stay where the user put them.
 * - Repeat all: at the end the whole queue plays again from its first track ([newPass]), with no refill;
 *   reshuffled each pass when shuffle is on.
 */
data class PlayModes(
    val shuffle: Boolean = false,
    val repeatMode: RepeatMode = RepeatMode.off,   // on disk and on the wire the key is "repeat"
    val original: List<String>? = null,            // upcoming item ids when shuffle was turned on
) {
    internal fun toJson(): JsonObject {
        val m = linkedMapOf<String, JsonElement>("shuffle" to JsonPrimitive(shuffle), "repeat" to JsonPrimitive(repeatMode.name))
        original?.let { o -> m["original"] = JsonArray(o.map { JsonPrimitive(it) }) }
        return JsonObject(m)
    }

    companion object {
        /** Lenient: a missing or unknown field reads as its default (a file from another version never fails). */
        internal fun from(e: JsonElement): PlayModes {
            val f = e.asFields()
            return PlayModes(
                shuffle = lenient(false) { f.boolOrNull("shuffle") },
                repeatMode = lenient(RepeatMode.off) { f.stringOrNull("repeat")?.let(RepeatMode::parse) },
                original = lenient(null) { f.stringList("original") })
        }

        private fun <T> lenient(default: T, read: () -> T?): T = try { read() ?: default } catch (_: Malformed) { default }

        private fun Fields.stringList(key: String): List<String>? =
            element(key)?.let { el -> (el as? JsonArray ?: throw Malformed("$key: not an array")).map { p ->
                (p as? JsonPrimitive)?.takeIf { it.isString }?.content ?: throw Malformed("$key: not a string") } }

        /** Fisher-Yates with an injected random source (iOS: `NextChooser.shuffled`). */
        fun <T> shuffled(xs: List<T>, random: () -> Double): List<T> {
            val out = xs.toMutableList()
            var i = out.size - 1
            while (i > 0) {
                val j = minOf(i, (random() * (i + 1)).toInt())
                val t = out[i]; out[i] = out[j]; out[j] = t
                i -= 1
            }
            return out
        }

        /** The current item stays put; everything after it is shuffled. Returns the new queue and the upcoming ids in their order before (`original`). */
        fun shuffleUpcoming(q: PlaybackQueue, random: () -> Double): Pair<PlaybackQueue, List<String>> {
            val cut = minOf(q.items.size, q.index + 1)
            val head = q.items.subList(0, cut)
            val rest = q.items.subList(cut, q.items.size)
            return q.copy(items = head + shuffled(rest, random)) to rest.map { it.id }
        }

        /**
         * Restores the pre-shuffle order of the remaining items. Items added while shuffled keep where the user put
         * them: those ahead of the first pre-shuffle item (play next) stay in front; the rest (add to queue,
         * refills) follow, in their current order. Ids may repeat: matched as a multiset.
         */
        fun unshuffle(q: PlaybackQueue, original: List<String>): PlaybackQueue {
            val cut = minOf(q.items.size, q.index + 1)
            val head = q.items.subList(0, cut)
            val rest = q.items.subList(cut, q.items.size)
            val wanted = original.toSet()
            // A play-next item whose id is also among the shuffled ones counts as that one (they can't be told
            // apart), so it goes back to its original slot.
            val firstOriginal = rest.indexOfFirst { it.id in wanted }
            if (firstOriginal < 0) return q
            val front = rest.subList(0, firstOriginal)
            val tail = rest.subList(firstOriginal, rest.size)
            // How many of each id are still upcoming (played and removed ones are gone).
            val left = HashMap<String, Int>()
            for (it in tail) left[it.id] = (left[it.id] ?: 0) + 1
            val byId = HashMap<String, lark.bridge.Item>()
            for (it in tail) byId[it.id] = it
            val restored = mutableListOf<lark.bridge.Item>()
            for (id in original) {
                val n = left[id] ?: continue
                val item = byId[id]
                if (n <= 0 || item == null) continue
                left[id] = n - 1
                restored.add(item)
            }
            // Whatever wasn't matched was added while shuffled: it follows, in its order.
            val used = HashMap<String, Int>()
            for (it in restored) used[it.id] = (used[it.id] ?: 0) + 1
            val added = tail.filter { it ->
                val n = used[it.id] ?: 0
                if (n == 0) true else { used[it.id] = n - 1; false }
            }
            return q.copy(items = head + front + restored + added)
        }

        /**
         * Repeat all at the end: the whole queue again from its first item; reshuffled when shuffle is on (never
         * starting with the item that just ended, when there is another).
         */
        fun newPass(q: PlaybackQueue, shuffle: Boolean, random: () -> Double): PlaybackQueue {
            val out = q.copy(index = 0, positionMs = 0)
            if (!shuffle) return out
            val items = shuffled(q.items, random).toMutableList()
            val ended = q.current?.id
            if (items.size > 1 && items[0].id == ended) {
                val others = items.indices.filter { items[it].id != ended }
                if (others.isNotEmpty()) {
                    val j = others[minOf(others.size - 1, (random() * others.size).toInt())]
                    val t = items[0]; items[0] = items[j]; items[j] = t
                }
            }
            return out.copy(items = items)
        }

        /**
         * Shuffle on, a queue edited by the user (the web's queue sheet, play next, add to queue): `original` is
         * kept in step, as the web's provider does, so shuffle off leaves the user's placement alone.
         * - an entry inserted or taken out: that id is forgotten once (it stays where the user put it);
         * - one entry moved: in the pre-shuffle order it goes just before the first item that now follows it and
         *   is still in that order (or last); moved into the past, it leaves the order.
         * Anything else (a different queue) leaves `original` as it is: unshuffle copes with ids it doesn't find.
         */
        fun edited(original: List<String>, from: PlaybackQueue, to: PlaybackQueue): List<String> {
            val a = from.items.map { it.id }
            val b = to.items.map { it.id }
            val o = original.toMutableList()
            fun forget(id: String) { val k = o.indexOf(id); if (k >= 0) o.removeAt(k) }
            if (b.size == a.size + 1 || b.size + 1 == a.size) {
                // One inserted (b longer) or one taken out (a longer): the single entry the other lacks.
                val (long, short) = if (b.size > a.size) b to a else a to b
                val i = short.indices.firstOrNull { long[it] != short[it] } ?: short.size
                if (long.subList(0, i) + long.subList(i + 1, long.size) == short) forget(long[i])
                return o
            }
            if (a.size != b.size || a == b || a.sorted() != b.sorted()) return o
            val i = a.indices.first { a[it] != b[it] }
            val j = a.indices.last { a[it] != b[it] }
            // Where the moved entry landed: a[i] moved down to j, or a[j] moved up to i.
            val dest = when {
                b[j] == a[i] && a.subList(i + 1, j + 1) == b.subList(i, j) -> j
                b[i] == a[j] && a.subList(i, j) == b.subList(i + 1, j + 1) -> i
                else -> return o
            }
            val id = b[dest]
            val k = o.indexOf(id)
            if (k < 0) return o   // added while shuffled: it stays where it is put
            o.removeAt(k)
            if (dest > to.index) {
                val after = b.subList(dest + 1, b.size).firstOrNull { it in o }
                o.add(after?.let { o.indexOf(it) } ?: o.size, id)
            }
            return o
        }
    }
}
