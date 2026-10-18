package lark.car

import android.content.Context
import lark.app.R
import lark.player.IntentBlocker
import lark.player.PlayerEngine

/**
 * What the shortcuts and car autoplay drive (iOS `IntentPlayer`): the engine in the app ([EngineIntentPlayer]), a fake
 * in tests (`AppServices.intentPlayer`).
 */
interface IntentPlayer {
    /**
     * Why nothing can start (signed out; offline with nothing on the phone), asked before playback is prepared; null
     * when something may play. `resume`: the saved queue counts too.
     */
    fun intentBlocker(resume: Boolean): IntentResult?
    /** Prepares playback (the media service comes up), synchronously, before anything is awaited. */
    fun prepareForPlayback()
    suspend fun shuffleFavorites()
    suspend fun resumeOrShuffleFavorites()
    /** Playing, or about to (loading, buffering). */
    val playing: Boolean
    /** Why the last attempt did not start. */
    val failureText: String?
}

/** The engine as the intents see it (iOS `extension PlayerEngine: IntentPlayer`). */
class EngineIntentPlayer(val engine: PlayerEngine) : IntentPlayer {
    override fun intentBlocker(resume: Boolean): IntentResult? = when (engine.intentBlocker(resume)) {
        IntentBlocker.notSignedIn -> IntentResult.notSignedIn
        IntentBlocker.offlineNothingCached -> IntentResult.offlineNothingCached
        null -> null
    }
    override fun prepareForPlayback() = engine.prepareForPlayback()
    override suspend fun shuffleFavorites() = engine.shuffleFavorites()
    override suspend fun resumeOrShuffleFavorites() = engine.resumeOrShuffleFavorites()
    override val playing: Boolean get() = engine.playing
    override val failureText: String? get() = engine.failureText
}

/**
 * What an intent did (iOS `IntentOutcome` and `LarkIntentError` in one): [played], [alreadyPlaying], or why nothing
 * could play. Its [text] is what the shortcut shows (a toast); car autoplay only logs it.
 */
sealed interface IntentResult {
    /** Started. */
    data object played : IntentResult
    /** Already playing (the car's own play resumed the queue first): the shortcut leaves it alone. */
    data object alreadyPlaying : IntentResult
    /** No server yet: Melarka has never been set up on this phone. */
    data object notSetUp : IntentResult
    /** No token: signed out (a logout, or a 401). */
    data object notSignedIn : IntentResult
    /** No network, and nothing on the phone to play. */
    data object offlineNothingCached : IntentResult
    /** It was tried and did not start (the server could not be reached, …): the engine's reason (null: "Can't play"). */
    data class nothingToPlay(val reason: String?) : IntentResult

    /** Nothing plays because of this result (not [played] or [alreadyPlaying]). */
    val failed: Boolean get() = this != played && this != alreadyPlaying

    /** The text for this result, in the app's language. [shuffle]: [played] says "Shuffling favorites". */
    fun text(context: Context, shuffle: Boolean = true): String? = when (this) {
        played -> if (shuffle) context.getString(R.string.intent_shuffling_favorites) else null
        alreadyPlaying -> context.getString(R.string.intent_already_playing)
        notSetUp -> context.getString(R.string.intent_not_set_up)
        notSignedIn -> context.getString(R.string.intent_not_signed_in)
        offlineNothingCached -> context.getString(R.string.intent_offline_nothing_cached)
        is nothingToPlay -> reason ?: context.getString(R.string.engine_cannot_play)
    }
}

/**
 * The intents' bodies (port of iOS `IntentRun`). Nothing that cannot play prepares playback (it would take audio focus
 * from the car's other audio for nothing); otherwise playback is prepared before the first suspension (as the car's
 * play command does), because a shortcut's activity is only visible until it returns, and a background start is only
 * allowed then. A start that fails returns its reason.
 */
object IntentRun {
    /**
     * Already playing: nothing at all (no service start, no new queue). When the car connects, its own play resumes the
     * queue and car autoplay's Shuffle Favorites arrives a moment later; it must not cut that song.
     */
    suspend fun shuffleFavorites(player: IntentPlayer?): IntentResult {
        if (player != null && player.playing) return IntentResult.alreadyPlaying
        ready(player, resume = false)?.let { return it }
        player!!.prepareForPlayback()
        player.shuffleFavorites()
        return check(player)
    }

    /** Resume (继续播放): the saved queue where it stopped (from the cache first); with no queue, shuffled favorites. */
    suspend fun resume(player: IntentPlayer?): IntentResult {
        ready(player, resume = true)?.let { return it }
        player!!.prepareForPlayback()
        player.resumeOrShuffleFavorites()
        return check(player)
    }

    private fun ready(player: IntentPlayer?, resume: Boolean): IntentResult? {
        if (player == null) return IntentResult.notSetUp
        return player.intentBlocker(resume)
    }

    private fun check(player: IntentPlayer): IntentResult =
        if (player.playing) IntentResult.played else IntentResult.nothingToPlay(player.failureText)
}
