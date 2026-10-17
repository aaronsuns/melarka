package lark.player

import lark.api.LyricsDoc
import lark.api.LyricsLine

/**
 * The current lyric line for the car display and the lock screen: it becomes the now-playing title, and the
 * song moves to the artist line. The same rules as the web's car lyrics (`PlayerProvider`'s `take` and
 * `lyricsCache.ts` `activeLine` / `isBlankLine`): only found, synced, non-instrumental lyrics with lines; a
 * line shows at `t_ms + offset_ms`; a line with nothing to read shows the song instead.
 */
class CarLyrics private constructor(val lines: List<LyricsLine>, val offsetMs: Long) {
    /** The last line whose time has come, trimmed; null before the first line or on a blank one (the song title shows). */
    fun line(atMs: Long): String? {
        val ms = atMs - offsetMs
        var lo = 0
        var hi = lines.size            // hi: the first index with t_ms > ms
        while (lo < hi) {
            val mid = (lo + hi) / 2
            if (lines[mid].tMs <= ms) lo = mid + 1 else hi = mid
        }
        if (lo <= 0) return null
        val text = lines[lo - 1].text
        return if (isBlank(text)) null else text.trim()
    }

    companion object {
        /** null unless found && synced && !instrumental && lines non-empty (iOS: the failable init). */
        fun of(doc: LyricsDoc): CarLyrics? {
            if (!doc.found || !doc.synced || doc.instrumental == true) return null
            val lines = doc.lines
            if (lines.isNullOrEmpty()) return null
            return CarLyrics(lines, doc.offsetMs ?: 0)
        }

        /**
         * A literal port of the web's `MARKER` (case-insensitive): an "(Instrumental)"-style line. `\p{Z}` next to
         * `\s` keeps Unicode spaces (U+3000, no-break space) counted on the JVM as on iOS.
         */
        private val MARKER = Regex(
            """^[\s\p{Z}(（\[【]*(instrumental|music|interlude|间奏|間奏|纯音乐|純音樂|前奏|尾奏)[\s\p{Z})）\]】]*$""",
            RegexOption.IGNORE_CASE)
        private val READABLE = Regex("""[\p{L}\p{N}]""")

        /** Nothing to read on a car display: empty, only symbols or punctuation, or a marker. */
        fun isBlank(s: String): Boolean {
            val t = s.trim()
            return t.isEmpty() || !READABLE.containsMatchIn(t) || MARKER.containsMatchIn(t)
        }
    }
}
