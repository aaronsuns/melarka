package lark.cache

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.isActive
import kotlinx.coroutines.job
import lark.api.LarkApiProtocol
import lark.api.Track
import lark.core.NetworkStatus

/**
 * Brings the cache's favorites up to date: the server's list, the flags, every missing favorite downloaded.
 * Wi-Fi only (online and not expensive: metered or Data Saver), checked before the list and again before each
 * download, so moving onto cellular mid-sync starts nothing more. Favorites fill to [FILL] (90%) of the cap, so a
 * library bigger than the cap does not churn favorites in and out on every sync, and the lookahead's tracks and
 * estimate errors fit beside them. A download also needs the volume's free-space reserve, and the first
 * out-of-space error ends the sync. Cancelling the calling coroutine (the background work stopping) cancels the
 * sync's own downloads only: a lookahead download of the same track is left alone. Used from the main thread.
 */
class FavoritesSync(private val api: LarkApiProtocol, private val cache: CacheStore, private val network: NetworkStatus) {
    private val onWiFi: Boolean get() = network.answered && network.isOnline && !network.isExpensive

    /** Returns when every download it started has finished (or been skipped). */
    suspend fun run() {
        if (!onWiFi || api.token == null || !currentCoroutineContext().isActive) return
        val favorites = try {
            api.favorites()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return    // a failed list is not "no favorites": the flags stay as they are
        }
        val me = currentCoroutineContext().job
        if (!me.isActive) return
        cache.setFavorites(favorites.map { it.track.id }.toSet())
        cache.refresh(favorites)
        cache.resetOutOfSpace()        // space may have been freed since the last sync
        val downloads = favorites.mapNotNull { f ->
            cache.download(f.track, f.json, favorite = true, shouldStart = { me.isActive && mayStart(f.track) })
        }
        val own = downloads.filter { it.created }.map { it.job }
        try {
            downloads.forEach { it.job.join() }
        } catch (e: CancellationException) {
            own.forEach { it.cancel() }
            throw e
        }
        val ids = favorites.map { it.track.id }
        // Covers of favorites cached before covers were kept (a download fetches its own), one at a time.
        for (id in cache.missingCovers(ids)) {
            if (!onWiFi || !me.isActive || cache.outOfSpace) break
            cache.cacheCover(id)
        }
        // Lyrics of favorites cached before lyrics were kept, the same way.
        for (id in cache.missingLyrics(ids)) {
            if (!onWiFi || !me.isActive || cache.outOfSpace) break
            cache.cacheLyrics(id)
        }
    }

    /**
     * Asked when a download gets its slot: still on Wi-Fi, no out-of-space error yet, and room for it beside
     * the favorites already cached and those downloading now, under 90% of the cap and on the volume.
     */
    private fun mayStart(t: Track): Boolean {
        if (!onWiFi || cache.outOfSpace) return false
        val need = (cache.activeDownloads + 1) * CacheStore.estimatedBytes(t)
        return cache.favoriteBytes + need <= (cache.capBytes * FILL).toLong() && cache.hasRoom(need)
    }

    companion object {
        /** The share of the cap favorites may fill. */
        const val FILL = 0.9
    }
}
