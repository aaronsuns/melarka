package lark.cache

import lark.api.ApiTrack
import lark.api.LyricsDoc
import lark.bridge.Item
import java.io.File

/**
 * The cache the engine holds for the app's life: the signed-in user's [CacheStore], nothing while signed out.
 */
class UserCache : CacheProviding {
    var store: CacheStore? = null
        private set

    /** Replaces the store; the old one is closed (its files stay for its user). */
    fun set(s: CacheStore?) {
        if (s === store) return
        store?.close()
        store = s
    }

    override fun localFile(trackId: Long): File? = store?.localFile(trackId)
    override fun touch(trackId: Long) { store?.touch(trackId) }
    override fun cachedFavorites(): List<ApiTrack> = store?.cachedFavorites() ?: emptyList()
    override fun prefetch(items: List<Item>) { store?.prefetch(items) }
    override fun cachedLyrics(trackId: Long): LyricsDoc? = store?.cachedLyrics(trackId)
    override fun updateLyrics(trackId: Long, doc: LyricsDoc) { store?.updateLyrics(trackId, doc) }
}
