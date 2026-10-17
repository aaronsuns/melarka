package lark.player

import java.time.Duration
import java.time.Instant
import kotlin.math.roundToLong

/**
 * The sleep timer's state, run by the engine on its scheduler (so it keeps time with the screen off): a wall-clock
 * deadline (its fade starts [PlayerEngine.SLEEP_FADE_S] before), or the end of the item playing.
 */
internal sealed interface Sleep {
    data class At(val deadline: Instant) : Sleep
    data object EndOfTrack : Sleep
}

internal object SleepTimer {
    /** What the state says is left before the deadline, in ms (never below 0). */
    fun remainingMs(deadline: Instant, now: Instant): Long =
        maxOf(0L, (Duration.between(now, deadline).toNanos() / 1_000_000.0).roundToLong())

    /** "End of this track": the volume follows what is left of the item in its last [PlayerEngine.SLEEP_FADE_S]. */
    fun endOfTrackLevel(durationMs: Long, positionMs: Long): Float =
        ((durationMs - positionMs) / (PlayerEngine.SLEEP_FADE_S * 1000)).coerceIn(0.0, 1.0).toFloat()

    /** The fade's steps: each one's delay from the fade's start and the volume it sets (the last one is 0). */
    fun fadeSteps(): List<Pair<Double, Float>> {
        val n = PlayerEngine.SLEEP_FADE_STEPS
        val step = PlayerEngine.SLEEP_FADE_S / n
        return (1..n).map { i -> i * step to (n - i).toFloat() / n }
    }
}
