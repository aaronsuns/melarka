package lark.cache

import lark.testing.lyricsDoc
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Port of OfflineLyricsTests' `CacheLyricsTests`: lyrics kept beside each cached track, like its cover. */
class CacheLyricsTest : CacheTestCase() {
    @Test fun aDownloadAlsoKeepsTheLyrics() = cacheTest {
        api.lyricsAnswers[1] = lyricsDoc(lines = listOf(0L to "一", 2000L to "二"), offset = 300)
        val t = trackModel(1)
        cache.download(t.track, t.json, favorite = true)
        settle(downloads = 1)
        assertNotNull(cache.cachedLyrics(1))
        assertEquals(api.lyricsAnswers[1], cache.cachedLyrics(1))
        assertEquals(listOf(1L), api.lyricsCalls)
    }

    @Test fun noLyricsAreAskedOnceAndAgainAfterAWeek() = cacheTest {
        api.lyricsAnswers[1] = lyricsDoc(found = false)
        put(1, 10, true, t0)
        cache.cacheLyrics(1)
        cache.cacheLyrics(1)
        assertNull(cache.cachedLyrics(1))
        assertEquals(listOf(1L), api.lyricsCalls)                           // remembered: not asked on every sync
        assertEquals(emptyList<Long>(), cache.missingLyrics(listOf(1)))
        clock.advance(7 * 24 * 3600 + 1.0)
        assertEquals(listOf(1L), cache.missingLyrics(listOf(1)))
    }

    @Test fun aNetworkErrorIsAskedAgainLater() = cacheTest {
        put(1, 10, true, t0)
        cache.cacheLyrics(1)                                                // the fake throws: no answer for 1
        assertEquals(listOf(1L), cache.missingLyrics(listOf(1)))
        api.lyricsAnswers[1] = lyricsDoc()
        cache.cacheLyrics(1)
        assertNotNull(cache.cachedLyrics(1))
        assertEquals(listOf(1L, 1L), api.lyricsCalls)
    }

    @Test fun anUncachedTrackHasNoLyricsAndFetchesNone() = cacheTest {
        api.lyricsAnswers[9] = lyricsDoc()
        cache.cacheLyrics(9)
        assertNull(cache.cachedLyrics(9))
        assertTrue(api.lyricsCalls.isEmpty())
    }

    /** A fresh answer (an offset changed) replaces the kept lyrics of a cached track; an uncached one keeps none. */
    @Test fun updateLyricsReplacesTheKeptCopy() = cacheTest {
        api.lyricsAnswers[1] = lyricsDoc(offset = 300)
        put(1, 10, true, t0)
        cache.cacheLyrics(1)
        cache.updateLyrics(1, lyricsDoc(offset = 4500))
        assertEquals(4500L, cache.cachedLyrics(1)?.offsetMs)
        cache.updateLyrics(1, lyricsDoc(found = false))                    // lyrics gone: kept as "none"
        assertNull(cache.cachedLyrics(1))
        assertEquals(emptyList<Long>(), cache.missingLyrics(listOf(1)))
        cache.updateLyrics(2, lyricsDoc())
        assertNull(cache.cachedLyrics(2))
    }

    @Test fun theLyricsGoWithTheirTrack() = cacheTest {
        api.lyricsAnswers[1] = lyricsDoc()
        put(1, 10, false, t0)
        cache.cacheLyrics(1)
        assertNotNull(cache.cachedLyrics(1))
        cache.clear()
        assertNull(cache.cachedLyrics(1))
        put(1, 10, false, t0)
        assertNull(cache.cachedLyrics(1))                                   // the old file went with the clear
    }

    @Test fun plainAndInstrumentalLyricsSurviveTheRoundTrip() = cacheTest {
        put(1, 10, true, t0); put(2, 10, true, t0)
        val plain = lark.api.LyricsDoc(true, false, null, null, null, "第一行\n第二行")
        val instrumental = lyricsDoc(instrumental = true)
        cache.updateLyrics(1, plain); cache.updateLyrics(2, instrumental)
        cache = makeCache()
        assertEquals(plain, cache.cachedLyrics(1))
        assertEquals(instrumental, cache.cachedLyrics(2))
    }
}
