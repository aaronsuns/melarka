package lark.testing

import lark.api.ApiTrack
import lark.api.LyricsDoc
import lark.bridge.Item
import lark.cache.CacheProviding
import java.io.File

/** iOS `FakeCache`: what is on the phone, the cached favorites, and what the engine touched and prefetched. */
class FakeCache : CacheProviding {
    val local = mutableMapOf<Long, File>()
    var favorites: List<ApiTrack> = emptyList()
    val touched = mutableListOf<Long>()
    val prefetched = mutableListOf<List<Long>>()
    val lyrics = mutableMapOf<Long, LyricsDoc>()
    val updatedLyrics = mutableListOf<Pair<Long, LyricsDoc>>()

    override fun localFile(trackId: Long): File? = local[trackId]
    override fun touch(trackId: Long) { touched.add(trackId) }
    override fun cachedFavorites(): List<ApiTrack> = favorites
    override fun prefetch(items: List<Item>) { prefetched.add(items.mapNotNull { it.trackId }) }
    override fun cachedLyrics(trackId: Long): LyricsDoc? = lyrics[trackId]
    override fun updateLyrics(trackId: Long, doc: LyricsDoc) { updatedLyrics.add(trackId to doc) }
}
