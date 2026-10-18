package lark.player

import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import lark.bridge.Item
import lark.bridge.NativePrefs
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.FakeNowPlayingSink
import lark.testing.lyricsDoc
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Port of the engine cases of ios/Tests/NowPlayingTests.swift ("MARK: Engine"): what the engine shows on the lock
 * screen and the car, and the car-lyrics line as the title. The controller and remote-command cases (artwork, the
 * media session's fields) are in `lark.service.NowPlayingTest`.
 */
class EngineNowPlayingTest : EngineTestCase() {
    private val sink = FakeNowPlayingSink().also { nowPlaying = it }
    private val qingtian = listOf(0L to "前奏", 1000L to "第一句", 5000L to "第二句")

    private fun nowPlayingTrack(id: Int, title: String, artist: String, album: String = "叶惠美") =
        Item(Item.Kind.track, id.toString(), title, artist, album, 269_000, buildJsonObject { put("id", id) })

    private fun play(item: Item) = engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(item), 0, 0, true, QueueSource.list))

    private fun playLyricsTrack(carLyrics: Boolean, keepAnswer: Boolean = false) {
        engine.prefs = NativePrefs("high", carLyrics)
        if (!keepAnswer) api.lyricsAnswers[7] = lyricsDoc(qingtian, offset = 500)
        play(nowPlayingTrack(7, "晴天", "周杰伦"))
        idle()
        backend.start()
    }

    private fun tick(ms: Long) {
        backend.positionMs = ms
        backend.delegate?.backendTick(ms, backend.generation)
    }

    @Test fun engineUpdatesTitleOnlyWhenLineChanges() = engineTest {
        playLyricsTrack(carLyrics = true)
        assertEquals(listOf(7L), api.lyricsCalls)
        assertEquals("晴天", sink.title)
        val before = sink.writes
        tick(1400)                                 // still the marker: no write
        assertEquals(before, sink.writes)
        tick(1600); tick(1700); tick(1800)
        assertEquals(before + 1, sink.writes)
        assertEquals("第一句", sink.title)
        assertEquals("晴天 · 周杰伦", sink.artist)
        assertEquals(1600L, sink.last?.positionMs)
        tick(5600)
        assertEquals(before + 2, sink.writes)
        assertEquals("第二句", sink.title)
    }

    @Test fun carLyricsOffShowsTheSongAndNeverAsks() = engineTest {
        playLyricsTrack(carLyrics = false)
        assertEquals(emptyList<Long>(), api.lyricsCalls)
        val before = sink.writes
        tick(1600); tick(5600)
        assertEquals(before, sink.writes)
        assertEquals("晴天", sink.title)
        assertEquals("周杰伦", sink.artist)
    }

    @Test fun turningCarLyricsOffPutsTheSongBack() = engineTest {
        playLyricsTrack(carLyrics = true)
        tick(1600)
        assertEquals("第一句", sink.title)
        engine.handle(WebMessage.SetPrefs(NativePrefs("high", false)))
        assertEquals("晴天", sink.title)
        engine.handle(WebMessage.SetPrefs(NativePrefs("high", true)))
        idle()
        assertEquals("第一句", sink.title)
    }

    @Test fun lyricsFailureLeavesTheTitle() = engineTest {
        api.lyricsAnswers.clear()
        engine.prefs = NativePrefs("high", true)
        play(nowPlayingTrack(9, "雨", "B"))
        idle()
        backend.start()
        tick(3000)
        assertEquals("雨", sink.title)
        assertEquals(listOf(9L), api.lyricsCalls)
    }

    /** Lyrics that could not be fetched (offline) are asked again after a backoff, while the track plays. */
    @Test fun failedLyricsAreAskedAgainAfterABackoff() = engineTest {
        engine.prefs = NativePrefs("high", true)
        play(nowPlayingTrack(7, "晴天", "周杰伦"))
        idle()
        backend.start()
        assertEquals(listOf(7L), api.lyricsCalls)                    // no answer: it threw
        tick(1600)
        idle()
        assertEquals(listOf(7L), api.lyricsCalls)                    // not at once
        advanceClock(PlayerEngine.LYRICS_RETRY_S)
        tick(1700)
        idle()
        assertEquals(listOf(7L, 7L), api.lyricsCalls)                 // failed again: the next wait is twice as long
        advanceClock(PlayerEngine.LYRICS_RETRY_S)
        tick(1800)
        idle()
        assertEquals(listOf(7L, 7L), api.lyricsCalls)
        api.lyricsAnswers[7] = lyricsDoc(qingtian, offset = 500)
        advanceClock(PlayerEngine.LYRICS_RETRY_S)
        tick(1900)
        idle()
        assertEquals(listOf(7L, 7L, 7L), api.lyricsCalls)
        assertEquals("第一句", sink.title)
        tick(2000)
        advanceClock(3600.0)
        tick(2100)
        idle()
        assertEquals(listOf(7L, 7L, 7L), api.lyricsCalls)              // found: never asked again
    }

    /** Lyrics aligned in the web while the track plays: the car line follows the new offset at once. */
    @Test fun lyricsChangedMovesTheCarLine() = engineTest {
        playLyricsTrack(carLyrics = true)
        tick(1600)
        assertEquals("第一句", sink.title)
        api.lyricsAnswers[7] = lyricsDoc(qingtian, offset = 4000)
        engine.handle(WebMessage.LyricsChanged(7))
        idle()
        assertEquals(listOf(7L, 7L), api.lyricsCalls)
        assertEquals("晴天", sink.title)                     // 1600 − 4000: before the first line
        tick(5100)
        assertEquals("第一句", sink.title)
        assertEquals(listOf(7L, 7L), cache.updatedLyrics.map { it.first })   // the cached copy follows too
        assertEquals(4000L, cache.updatedLyrics.last().second.offsetMs)
    }

    @Test fun lyricsChangedForAnotherTrackIsIgnored() = engineTest {
        playLyricsTrack(carLyrics = true)
        engine.handle(WebMessage.LyricsChanged(8))
        idle()
        assertEquals(listOf(7L), api.lyricsCalls)
    }

    /**
     * A cached track shows its kept lyrics at once and, online, asks for fresh ones: an offset set on another
     * device since the download reaches the car.
     */
    @Test fun cachedLyricsAreRefreshedWhenOnline() = engineTest {
        cache.lyrics[7] = lyricsDoc(qingtian, offset = 500)
        api.lyricsAnswers[7] = lyricsDoc(qingtian, offset = 4000)
        playLyricsTrack(carLyrics = true, keepAnswer = true)
        assertEquals(listOf(7L), api.lyricsCalls)
        tick(1600)
        assertEquals("晴天", sink.title)
        assertEquals(4000L, cache.updatedLyrics.last().second.offsetMs)
    }

    @Test fun cachedLyricsOfflineAreNotAsked() = engineTest {
        network.isOnline = false
        cache.lyrics[7] = lyricsDoc(qingtian, offset = 500)
        playLyricsTrack(carLyrics = true, keepAnswer = true)
        advanceClock(3600.0)
        tick(1600)
        idle()
        assertEquals(emptyList<Long>(), api.lyricsCalls)
        assertEquals("第一句", sink.title)
    }

    /** "Not found" is an answer: it is not asked again for this track. */
    @Test fun lyricsNotFoundIsNotRetried() = engineTest {
        api.lyricsAnswers[7] = lyricsDoc(found = false)
        playLyricsTrack(carLyrics = true, keepAnswer = true)
        advanceClock(3600.0)
        tick(1600)
        idle()
        assertEquals(listOf(7L), api.lyricsCalls)
    }

    @Test fun playPauseAndSeekWriteTheSink() = engineTest {
        playLyricsTrack(carLyrics = true)
        var before = sink.writes
        backend.positionMs = 2000
        engine.handle(WebMessage.Pause(Item.Kind.track))
        assertEquals(before + 1, sink.writes)
        assertEquals(false, sink.last?.playing)
        assertEquals(2000L, sink.last?.positionMs)
        before = sink.writes
        engine.handle(WebMessage.Play(Item.Kind.track))
        assertEquals(before + 1, sink.writes)
        assertEquals(true, sink.last?.playing)
        before = sink.writes
        engine.handle(WebMessage.Seek(Item.Kind.track, 1800))     // the same line: still written once, for the elapsed time
        assertEquals(before + 1, sink.writes)
        assertEquals(1800L, sink.last?.positionMs)
        before = sink.writes
        engine.handle(WebMessage.Seek(Item.Kind.track, 5600))     // another line: one write, not two
        assertEquals(before + 1, sink.writes)
        assertEquals("第二句", sink.title)
    }

    @Test fun stopClearsNowPlaying() = engineTest {
        playLyricsTrack(carLyrics = true)
        assertNotNull(sink.last)
        engine.handle(WebMessage.Stop(Item.Kind.track))
        assertNull(sink.last)
        assertEquals(1, sink.cleared)
    }

    @Test fun aNewTrackDropsTheOldLyrics() = engineTest {
        playLyricsTrack(carLyrics = true)
        tick(1600)
        assertEquals("第一句", sink.title)
        play(nowPlayingTrack(8, "七里香", "周杰伦"))
        assertEquals("七里香", sink.title)
        idle()
        backend.start()
        tick(1600)
        assertEquals("七里香", sink.title)
    }
}
