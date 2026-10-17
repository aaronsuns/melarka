package lark.player

import lark.bridge.Item

/**
 * The lock screen, the notification and the car display as the engine sees them (the media session's
 * now-playing controller implements it). The engine calls [show] only when what it shows changed (the item,
 * playing or not, the rate, the duration, the car-lyrics line) or after a seek; [clear] when nothing is current.
 */
interface NowPlayingSink {
    /** `lyricLine`: the car-lyrics line to show as the title (the song moves to the artist line), or null. */
    fun show(item: Item, positionMs: Long, durationMs: Long?, rate: Double, playing: Boolean, lyricLine: String?)
    fun clear()
}
