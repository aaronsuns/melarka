package lark.player

import android.content.Context
import lark.app.R

/**
 * The notices and errors the engine sends to the page (`notice`, the state's `error`), in the phone's language:
 * [from] reads them from the string resources (four locales, the iOS wording). [english] is the same text as
 * `values/strings.xml`, for tests and anything built without a context.
 */
class EngineTexts(
    val offlineNotice: String,
    val noFavoritesNotice: String,
    val cannotPlay: String,
    val stallNotice: String,
    val offlineStopped: String,
    val episodeFallbackNotice: String,
    val nothingCachedNotice: String,
    val shuffleFailedNotice: String,
) {
    companion object {
        val english = EngineTexts(
            offlineNotice = "Offline: playing cached favorites",
            noFavoritesNotice = "No favorites yet: shuffling all songs",
            cannotPlay = "Can't play",
            stallNotice = "Network too slow: playing cached songs first",
            offlineStopped = "Offline: no cached songs to play",
            episodeFallbackNotice = "Episode can't play: playing music instead",
            nothingCachedNotice = "Offline, and no favorites are cached",
            shuffleFailedNotice = "Couldn't get favorites; try again later",
        )

        fun from(context: Context) = EngineTexts(
            offlineNotice = context.getString(R.string.engine_offline_notice),
            noFavoritesNotice = context.getString(R.string.engine_no_favorites_notice),
            cannotPlay = context.getString(R.string.engine_cannot_play),
            stallNotice = context.getString(R.string.engine_stall_notice),
            offlineStopped = context.getString(R.string.engine_offline_stopped),
            episodeFallbackNotice = context.getString(R.string.engine_episode_fallback_notice),
            nothingCachedNotice = context.getString(R.string.engine_nothing_cached_notice),
            shuffleFailedNotice = context.getString(R.string.engine_shuffle_failed_notice),
        )
    }
}
