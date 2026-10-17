package lark.player

import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.FakeLarkApi.ProgressCall
import lark.testing.episode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Port of ios/Tests/EpisodeProgressTests.swift. */
class EpisodeProgressTest : EngineTestCase() {
    private fun playEpisodes(ids: List<String> = listOf("abcdefghijk", "bbbbbbbbbbb"), pos: Long = 0) {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, ids.map { episode(it) }, 0, pos, true, QueueSource.list))
        backend.start()
    }

    @Test fun progressEveryFifteenSecondsWhilePlaying() = engineTest {
        playEpisodes()
        advanceClock(10.0); backend.playTo(10_000); idle()
        assertTrue(api.progressCalls.isEmpty())
        backend.playTo(14_500); advanceClock(5.0); backend.playTo(15_000); idle()
        assertEquals(listOf(ProgressCall("abcdefghijk", 15, false)), api.progressCalls)
        advanceClock(15.0); engine.tick(); idle()
        assertEquals(2, api.progressCalls.size)
    }

    @Test fun progressOnPause() = engineTest {
        playEpisodes()
        backend.playTo(7_600)
        engine.handle(WebMessage.Pause(Item.Kind.episode))
        idle()
        assertEquals(ProgressCall("abcdefghijk", 7, false), api.progressCalls.last())
    }

    @Test fun theEndMarksItPlayedThenPlaysTheNext() = engineTest {
        playEpisodes()
        backend.playTo(20_000)
        backend.finish()
        idle()
        assertEquals(ProgressCall("abcdefghijk", 0, true), api.progressCalls.last())
        assertEquals(1, engine.episodes.index)
        assertEquals(MediaSource.Remote(android.net.Uri.parse("https://lark.test/api/v1/episodes/bbbbbbbbbbb/stream?kind=audio"), "T1"),
            backend.loads.last().src)
        assertEquals(true, backend.loads.last().autoplay)
    }

    @Test fun theLastEpisodeEndingStops() = engineTest {
        playEpisodes(listOf("abcdefghijk"))
        backend.finish()
        idle()
        assertFalse(engine.playing)
        assertEquals(true, api.progressCalls.last().played)
        assertEquals(0L, engine.episodes.positionMs)
    }

    @Test fun episodesCreateNoPlayEvents() = engineTest {
        playEpisodes()
        backend.playTo(60_000)
        engine.next()
        backend.start(); backend.playTo(60_000); backend.finish()
        engine.handle(WebMessage.FlushEvents("x"))
        idle()
        assertTrue(api.postedEvents.isEmpty())
        assertTrue(events.pending.isEmpty())
    }

    @Test fun skippingToTheNextEpisodeSavesTheOutgoingPosition() = engineTest {
        playEpisodes()
        backend.playTo(33_000)
        engine.next()
        idle()
        assertEquals(ProgressCall("abcdefghijk", 33, false), api.progressCalls.first())
        assertEquals(1, engine.episodes.index)
    }

    @Test fun aNetworkErrorRetriesTheEpisodeAtItsPosition() = engineTest {
        playEpisodes(pos = 60_000)
        backend.playTo(61_000)
        backend.fail(network = true)
        scheduler.advance(2.0)
        assertEquals(2, backend.loads.size); assertEquals(61_000L, backend.loads.last().startMs)
    }
}
