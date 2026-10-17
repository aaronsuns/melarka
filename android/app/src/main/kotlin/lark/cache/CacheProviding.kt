package lark.cache

import lark.api.ApiTrack
import lark.api.LyricsDoc
import lark.bridge.Item
import java.io.File

/**
 * The offline cache as the engine sees it (the per-user cache store); tests use `FakeCache`. Used from the main
 * thread. The iOS names are kept, except [localFile] (iOS `localURL`): Android hands the player a file.
 */
interface CacheProviding {
    /** The cached file for a track, or null when it has to stream. Only a file that is still on disk. */
    fun localFile(trackId: Long): File?

    /** The track was just played from the cache (LRU bookkeeping). */
    fun touch(trackId: Long)

    /**
     * Every cached favorite with its raw JSON (`Item.meta`), for the offline fallback and a no-network shuffle.
     * From the index alone: no network, no waiting.
     */
    fun cachedFavorites(): List<ApiTrack>

    /** Download these tracks ahead (the lookahead). Items, not ids, so the cache can keep their metadata. */
    fun prefetch(items: List<Item>)

    /**
     * A cached track's lyrics, kept beside its audio: offline (the offline player, the lock screen) they are all
     * there is. null when the track is not cached, its lyrics were not fetched yet, or it has none.
     */
    fun cachedLyrics(trackId: Long): LyricsDoc?

    /**
     * A fresh answer from the server for a track's lyrics (another offset, other lyrics, none): it replaces the
     * kept copy when the track is cached, and is dropped otherwise.
     */
    fun updateLyrics(trackId: Long, doc: LyricsDoc)
}

/** No cache. */
object NoCache : CacheProviding {
    override fun localFile(trackId: Long): File? = null
    override fun touch(trackId: Long) {}
    override fun cachedFavorites(): List<ApiTrack> = emptyList()
    override fun prefetch(items: List<Item>) {}
    override fun cachedLyrics(trackId: Long): LyricsDoc? = null
    override fun updateLyrics(trackId: Long, doc: LyricsDoc) {}
}
