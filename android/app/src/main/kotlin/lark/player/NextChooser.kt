package lark.player

import lark.bridge.Item

/**
 * Which music item plays next, mirroring the web's `web/src/player/nextTrack.ts` (`chooseNext`, `pickFavorite`).
 *
 * - Failed items (within their 30-minute penalty) are passed over.
 * - With `mustBeLocal` (offline, or after 3 failures in a row) only items whose bytes are on the phone count;
 *   with none in the queue, a cached favorite is put after the current item. Otherwise the next item plays.
 * - A chosen item further down the queue is moved up to just after the current one (the engine does that),
 *   so the items it jumped over still play later.
 */
object NextChooser {
    sealed interface Choice {
        /** Play queue entry `index`, moved to just after the current one. */
        data class Queue(val index: Int) : Choice
        /** Put this cached favorite after the current one and play it. */
        data class Insert(val item: Item) : Choice
        /** Repeat all: the queue starts over (from its first item that may play). */
        data class Wrap(val mustBeLocal: Boolean) : Choice
        data object None : Choice
    }

    /** A cached favorite this far back in the queue (or the current one) is passed over while another is available. */
    const val HISTORY = 30

    /**
     * `wrap` (repeat all): with nothing playable after the current item, start the queue over instead of a
     * favorite or stopping, but only when some item in it may play: not failed, and on the phone when
     * `mustBeLocal` (offline, the failure brake). Otherwise, and without `wrap`, the result is as before
     * (a cached favorite, or none), so a queue that can't play never loops.
     */
    fun choose(q: PlaybackQueue, mustBeLocal: Boolean, failed: (Item) -> Boolean, isLocal: (Item) -> Boolean,
               favorites: List<Item>, random: () -> Double, wrap: Boolean = false): Choice {
        val cands = q.items.indices.filter { it > q.index && !failed(q.items[it]) }
        if (wrap && cands.isEmpty() && q.items.any { !failed(it) && (!mustBeLocal || isLocal(it)) }) {
            return Choice.Wrap(mustBeLocal)
        }
        if (!mustBeLocal) cands.firstOrNull()?.let { return Choice.Queue(it) }
        cands.firstOrNull { isLocal(q.items[it]) }?.let { return Choice.Queue(it) }
        pickFavorite(q, failed, favorites, random)?.let { return Choice.Insert(it) }
        return Choice.None
    }

    fun pickFavorite(q: PlaybackQueue, failed: (Item) -> Boolean, favorites: List<Item>, random: () -> Double): Item? {
        val cur = q.current?.id
        val lo = maxOf(0, q.index - HISTORY)
        val hi = minOf(q.items.size, q.index + 1)
        val recent = if (lo < hi) q.items.subList(lo, hi).mapTo(HashSet()) { it.id } else emptySet()
        val all = favorites.filter { it.id != cur && !failed(it) }
        val fresh = all.filter { it.id !in recent }
        val pool = fresh.ifEmpty { all }
        if (pool.isEmpty()) return null
        return pool[minOf(pool.size - 1, (random() * pool.size).toInt())]
    }

    /** Fisher–Yates with an injected random source. */
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
}
