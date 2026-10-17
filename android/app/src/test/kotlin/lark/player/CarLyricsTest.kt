package lark.player

import lark.api.LyricsDoc
import lark.testing.lyricsDoc
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Port of ios/Tests/CarLyricsTests.swift: the same rules as the web's `lyricsCache.ts` (`activeLine`, `isBlankLine`) and the car-lyrics `take`. */
class CarLyricsTest {
    private fun doc(lines: List<Pair<Long, String>> = listOf(0L to "a", 1000L to "b"), offset: Long? = null, synced: Boolean = true,
                    instrumental: Boolean? = null, found: Boolean = true) = lyricsDoc(lines, offset, synced, instrumental, found)

    @Test fun lineAtAppliesOffset() {
        val c = CarLyrics.of(doc(listOf(0L to "前奏", 1000L to "第一句", 5000L to "第二句"), offset = 500))!!
        assertNull(c.line(1400))          // 1400 − 500 = 900 → "前奏", a marker → null (title shows)
        assertEquals("第一句", c.line(1600))
        assertEquals("第二句", c.line(5600))
    }

    @Test fun beforeTheFirstLineAndNegativeOffset() {
        val c = CarLyrics.of(doc(listOf(2000L to "一", 4000L to "二"), offset = -1000))!!
        assertNull(c.line(900))           // 900 + 1000 = 1900: before the first line
        assertEquals("一", c.line(1000))  // exactly at t_ms + offset
        assertEquals("二", c.line(3000))
        assertEquals("二", c.line(999_999))
    }

    @Test fun lineIsTrimmed() {
        val c = CarLyrics.of(doc(listOf(0L to "  晴天  ")))!!
        assertEquals("晴天", c.line(10))
    }

    @Test fun blankAndMarkerLines() {
        for (s in listOf("", "  ", "♪♪", "(Instrumental)", "【间奏】", "（純音樂）", "尾奏", "MUSIC", " [Interlude] ", "...", "纯音乐")) {
            assertTrue(s, CarLyrics.isBlank(s))
        }
        for (s in listOf("Music is life", "间奏之后", "1", "a")) assertFalse(s, CarLyrics.isBlank(s))
    }

    /** Android: Unicode spaces (U+3000 ideographic, U+00A0 no-break) count as space, as on iOS. */
    @Test fun unicodeSpacesAroundAMarker() {
        assertTrue(CarLyrics.isBlank("　（间奏）　"))
        assertTrue(CarLyrics.isBlank("( Instrumental )"))
        assertEquals("晴天", CarLyrics.of(doc(listOf(0L to "　晴天 ")))!!.line(0))
    }

    @Test fun aBlankLineShowsTheTitle() {
        val c = CarLyrics.of(doc(listOf(0L to "一", 1000L to "♪", 2000L to "二")))!!
        assertEquals("一", c.line(500))
        assertNull(c.line(1500))
        assertEquals("二", c.line(2500))
    }

    @Test fun unsyncedOrInstrumentalGivesNull() {
        assertNull(CarLyrics.of(doc(synced = false)))
        assertNull(CarLyrics.of(doc(instrumental = true)))
        assertNull(CarLyrics.of(doc(found = false)))
        assertNull(CarLyrics.of(doc(lines = emptyList())))
        assertNull(CarLyrics.of(LyricsDoc(true, true, null, null, null, null)))
        assertNotNull(CarLyrics.of(doc(instrumental = false)))
    }
}
