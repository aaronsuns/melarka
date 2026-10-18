package lark.service

import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.test.core.app.ApplicationProvider
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import lark.api.LarkError
import lark.bridge.Item
import lark.bridge.NativePrefs
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.FakeInfoSink
import lark.testing.SessionPlayerRig
import lark.testing.episode
import lark.testing.lyricsDoc
import lark.testing.q
import lark.testing.trackModel
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of the controller and remote-command cases of ios/Tests/NowPlayingTests.swift (the engine cases are
 * `lark.player.EngineNowPlayingTest`): [NowPlayingController] on a counting sink, driven by the engine, and what the
 * media session shows from it ([SessionPlayer]).
 */
@UnstableApi
class NowPlayingTest : EngineTestCase() {
    private val sink = FakeInfoSink()
    private lateinit var np: NowPlayingController
    private val artworkCalls = mutableListOf<String>()
    private var artworkImage: ByteArray? = null
    private var artworkError: Exception? = null
    private var remote: RemoteCommands? = null
    private var session: SessionPlayerRig? = null

    override fun beforeEngine() {
        np = NowPlayingController(sink, { item ->
            artworkCalls.add(item.id)
            artworkError?.let { throw it }
            artworkImage
        }, scope, clock)
        nowPlaying = np
    }

    @After fun tearDown() {
        remote?.detach(); remote = null
        session?.release(); session = null
    }

    private val info get() = sink.info
    private fun image() = byteArrayOf(1, 2, 3, 4)

    private fun nowPlayingTrack(id: Int, title: String, artist: String, album: String = "叶惠美") =
        Item(Item.Kind.track, id.toString(), title, artist, album, 269_000, buildJsonObject { put("id", id) })

    private fun show(item: Item, positionMs: Long = 0, durationMs: Long? = null, playing: Boolean = true, line: String? = null) =
        np.show(item, positionMs, durationMs, 1.0, playing, line)

    // MARK: Controller

    @Test fun carLyricsPutsLineInTitleAndSongInArtist() = engineTest {
        np.show(nowPlayingTrack(7, "晴天", "周杰伦"), 1600, 269_000, 1.0, true, "第一句")
        val i = info!!
        assertEquals("第一句", i.title)
        assertEquals("晴天 · 周杰伦", i.artist)
        assertEquals("叶惠美", i.album)
        assertEquals(1600L, i.elapsedMs)
        assertEquals(269_000L, i.durationMs)
        assertEquals(1.0, i.playbackRate, 0.0)
        assertEquals(Item.Kind.track, i.kind)                    // audio: MediaMetadata.MEDIA_TYPE_MUSIC in the session
    }

    @Test fun noLineShowsTheSong() = engineTest {
        np.show(nowPlayingTrack(7, "晴天", "周杰伦"), 0, null, 1.0, false, null)
        assertEquals("晴天", sink.title)
        assertEquals("周杰伦", sink.artist)
        assertEquals(269_000L, info?.durationMs)                // the item's own duration
        assertEquals(0.0, info!!.playbackRate, 0.0)             // paused
        assertEquals(1.0, info!!.defaultRate, 0.0)
    }

    @Test fun lineWithNoArtistIsTheSongAlone() = engineTest {
        np.show(nowPlayingTrack(7, "晴天", ""), 0, null, 1.0, true, "第一句")
        assertEquals("晴天", sink.artist)
    }

    @Test fun artworkIsFetchedOncePerItemAndCached() = engineTest {
        artworkImage = image()
        val item = nowPlayingTrack(7, "晴天", "周杰伦")
        show(item)
        idle()
        assertNotNull(info?.artwork)
        show(item, positionMs = 1000, line = "一")
        assertNotNull(info?.artwork)                           // kept on the next write
        show(nowPlayingTrack(8, "x", "y"))
        assertNull(info?.artwork)                              // never another item's
        idle()
        assertNotNull(info?.artwork)
        show(item)
        assertNotNull(info?.artwork)                           // from the memory cache
        idle()
        assertEquals(listOf("7", "8"), artworkCalls)
    }

    /** The same artwork object on every write: some head units send the cover again when it changes. */
    @Test fun artworkObjectIsReused() = engineTest {
        artworkImage = image()
        val item = nowPlayingTrack(7, "晴天", "周杰伦")
        show(item)
        idle()
        val first = info?.artwork
        assertNotNull(first)
        show(item, positionMs = 1000, line = "一")
        assertSame(first, info?.artwork)
    }

    /** A fetch that failed (offline) is asked again after a backoff; only "no cover" is remembered. */
    @Test fun failedArtworkIsAskedAgainAfterABackoff() = engineTest {
        artworkError = LarkError.Http(503, null)
        val item = nowPlayingTrack(7, "晴天", "周杰伦")
        show(item)
        idle()
        assertEquals(1, artworkCalls.size)
        show(item, line = "一")
        idle()
        assertEquals(1, artworkCalls.size)                     // not at once
        advanceClock(NowPlayingController.ARTWORK_RETRY_S)
        artworkError = null
        artworkImage = image()
        show(item, line = "二")
        idle()
        assertNotNull(info?.artwork)
        assertEquals(2, artworkCalls.size)
    }

    @Test fun failedArtworkBacksOffLonger() = engineTest {
        artworkError = LarkError.Http(503, null)
        val item = nowPlayingTrack(7, "晴天", "周杰伦")
        show(item)
        idle()
        assertEquals(1, artworkCalls.size)
        advanceClock(NowPlayingController.ARTWORK_RETRY_S)
        show(item, line = "一")
        idle()
        assertEquals(2, artworkCalls.size)
        advanceClock(NowPlayingController.ARTWORK_RETRY_S)     // the second wait is twice as long
        show(item, line = "二")
        idle()
        assertEquals(2, artworkCalls.size)
        advanceClock(NowPlayingController.ARTWORK_RETRY_S)
        show(item, line = "三")
        idle()
        assertEquals(3, artworkCalls.size)
    }

    @Test fun noCoverIsRemembered() = engineTest {
        val item = nowPlayingTrack(7, "晴天", "周杰伦")
        show(item)
        idle()
        assertEquals(1, artworkCalls.size)
        advanceClock(3600.0)
        show(item, line = "一")
        idle()
        assertEquals(1, artworkCalls.size)
    }

    @Test fun artworkCacheKeepsTwentyItems() = engineTest {
        artworkImage = image()
        for (i in 1..21) {
            show(nowPlayingTrack(i, "t", "a"))
            idle()
            assertNotNull(info?.artwork)
        }
        show(nowPlayingTrack(21, "t", "a"))
        show(nowPlayingTrack(2, "t", "a"))
        show(nowPlayingTrack(1, "t", "a"))
        idle()
        assertNotNull(info?.artwork)
        assertEquals(22, artworkCalls.size)        // 1 was dropped (the oldest) and fetched again; 2 and 21 were kept
        assertEquals("1", artworkCalls.last())
    }

    /** An arriving cover rewrites the last write, its elapsed time moved on to now (it played meanwhile). */
    @Test fun arrivingArtworkMovesTheElapsedTimeOn() = engineTest {
        artworkImage = image()
        val item = nowPlayingTrack(7, "晴天", "周杰伦")
        np.show(item, 1000, null, 1.0, true, null)
        advanceClock(2.0)
        idle()
        assertEquals(3000L, info?.elapsedMs)
        assertNotNull(info?.artwork)
    }

    // MARK: Engine and remote commands

    @Test fun episodeShowsChannelAndSkipCommands() = engineTest {
        val ep = Item(Item.Kind.episode, "abcdefghijk", "第 12 期", "某频道", "频道", 3_600_000,
            buildJsonObject { put("video_id", "abcdefghijk") })
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(ep), 0, 0, true, QueueSource.list))
        idle()
        assertEquals(emptyList<Long>(), api.lyricsCalls)                 // never lyrics for an episode
        assertEquals("第 12 期", sink.title)
        assertEquals("某频道", sink.artist)

        val r = RemoteCommands(scope, engine).also { remote = it }
        val s = SessionPlayerRig(ApplicationProvider.getApplicationContext(), r, sink.hub).also { session = it }
        val p = s.player
        r.update(Item.Kind.episode)
        assertTrue(r.skipEnabled)
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_FORWARD))
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_BACK))
        assertEquals(30_000L, p.seekForwardIncrement)
        assertEquals(15_000L, p.seekBackIncrement)
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT))
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_TO_PREVIOUS))
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM))
        r.update(Item.Kind.track)
        assertFalse(r.skipEnabled)
        assertFalse(p.isCommandAvailable(Player.COMMAND_SEEK_FORWARD))
        assertFalse(p.isCommandAvailable(Player.COMMAND_SEEK_BACK))
        assertTrue(p.isCommandAvailable(Player.COMMAND_SEEK_TO_NEXT))
        assertTrue(p.isCommandAvailable(Player.COMMAND_PLAY_PAUSE))
    }

    @Test fun remoteCommandsFollowTheActiveKind() = engineTest {
        val r = RemoteCommands(scope, engine).also { remote = it }
        assertFalse(r.skipEnabled)
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("abcdefghijk")), 0, 0, true, QueueSource.list))
        assertTrue(r.skipEnabled)
        engine.handle(WebMessage.Stop(Item.Kind.episode))
        assertFalse(r.skipEnabled)
    }

    // MARK: Remote command handlers

    @Test fun remoteHandlers() = engineTest {
        val r = RemoteCommands(scope, engine).also { remote = it }
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, r.pauseNow())
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, r.nextNow())
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, r.seekNow(3.0))

        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.start()
        assertEquals(RemoteCommands.Status.success, r.pauseNow())
        assertFalse(engine.playing)
        val activations = mutableListOf<Int>()
        engine.willStartPlayback = { activations.add(backend.calls.size) }
        val calls = backend.calls.size
        assertEquals(RemoteCommands.Status.success, r.toggleNow())
        assertTrue(engine.playing)
        assertEquals(listOf(calls), activations)                   // once, before backend.play
        assertEquals("play", backend.calls.last())
        engine.willStartPlayback = null
        assertEquals(RemoteCommands.Status.success, r.seekNow(42.5))
        assertEquals("seek:42500", backend.calls.last())
        assertEquals(RemoteCommands.Status.success, r.nextNow())
        assertEquals("2", engine.current?.id)
        backend.positionMs = 0
        assertEquals(RemoteCommands.Status.success, r.previousNow())
        assertEquals("1", engine.current?.id)
        assertEquals(RemoteCommands.Status.success, r.skipNow(30.0))
        assertEquals(RemoteCommands.Status.success, r.skipNow(-15.0))
        assertEquals("seek:15000", backend.calls.last())
    }

    /** The car's play with nothing loaded (a cold start): the saved queue plays, or cached favorites shuffle. */
    @Test fun remotePlayWithNothingLoadedShufflesFavorites() = engineTest {
        cache.favorites = listOf(trackModel(11), trackModel(12))
        cache.local[11L] = tmp("11.m4a"); cache.local[12L] = tmp("12.m4a")
        val r = RemoteCommands(scope, engine).also { remote = it }
        val activations = mutableListOf<Int>()
        engine.willStartPlayback = { activations.add(backend.loads.size) }
        assertEquals(RemoteCommands.Status.success, r.playNow())
        // Playback is being started before the handler returns, before the cold start's coroutine runs.
        assertEquals(listOf(0), activations)
        idle()
        assertTrue(engine.playing)
        assertEquals(QueueSource.favorites, engine.music.source)
        assertEquals(1, backend.loads.size)
        assertTrue("a cached favorite plays from the file", backend.loads[0].src is lark.player.MediaSource.File)
    }

    @Test fun remotePlayResumesTheRestoredQueue() = engineTest {
        engine.handle(q(listOf(5, 6), 1, 7000, false))
        makeEngine()                      // a relaunch: restored, nothing loaded
        assertTrue(backend.loads.isEmpty())
        val r = RemoteCommands(scope, engine).also { remote = it }
        assertEquals(RemoteCommands.Status.success, r.playNow())
        idle()
        assertEquals(1, backend.loads.size)
        assertEquals(7000L, backend.loads[0].startMs)
        assertEquals("6", engine.current?.id)
        assertEquals("T6", sink.title)
    }

    // MARK: The media session (Android)

    private fun tick(ms: Long) {
        backend.positionMs = ms
        backend.delegate?.backendTick(ms, backend.generation)
    }

    /** The session's metadata changes only when what it shows changes: the car-lyrics line, not every tick. */
    @Test fun sessionMetadataIsWrittenOnlyWhenTheShownValuesChange() = engineTest {
        val r = RemoteCommands(scope, engine).also { remote = it }
        val p = SessionPlayerRig(ApplicationProvider.getApplicationContext(), r, sink.hub).also { session = it }.player
        var changes = 0
        p.addListener(object : Player.Listener {
            override fun onMediaMetadataChanged(mediaMetadata: MediaMetadata) { changes++ }
        })
        engine.prefs = NativePrefs("high", true)
        api.lyricsAnswers[7] = lyricsDoc(listOf(0L to "前奏", 1000L to "第一句", 5000L to "第二句"), offset = 500)
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(nowPlayingTrack(7, "晴天", "周杰伦")), 0, 0, true, QueueSource.list))
        idle()
        backend.start()
        assertEquals("晴天", p.mediaMetadata.title.toString())
        assertEquals("周杰伦", p.mediaMetadata.artist.toString())
        assertEquals("叶惠美", p.mediaMetadata.albumTitle.toString())
        assertEquals(MediaMetadata.MEDIA_TYPE_MUSIC, p.mediaMetadata.mediaType)
        val before = changes
        tick(1400); tick(1450)
        assertEquals(before, changes)
        tick(1600); tick(1700); tick(1800)
        assertEquals(before + 1, changes)
        assertEquals("第一句", p.mediaMetadata.title.toString())
        assertEquals("晴天 · 周杰伦", p.mediaMetadata.artist.toString())
        tick(5600)
        assertEquals(before + 2, changes)
        assertEquals("第二句", p.mediaMetadata.title.toString())
        assertEquals(1, p.mediaItemCount)                          // one item, the song: the line is only the shown title
        assertEquals("晴天", p.currentMediaItem?.mediaMetadata?.title.toString())
    }

    /** The cover reaches the session as image data, never as a URL (no authenticated URI, no token anywhere). */
    @Test fun sessionArtworkIsImageDataNotAUri() = engineTest {
        artworkImage = image()
        val r = RemoteCommands(scope, engine).also { remote = it }
        val p = SessionPlayerRig(ApplicationProvider.getApplicationContext(), r, sink.hub).also { session = it }.player
        engine.handle(q(listOf(7), 0, 0, true))
        idle()
        assertArrayEquals(image(), p.mediaMetadata.artworkData)
        assertEquals(MediaMetadata.PICTURE_TYPE_FRONT_COVER, p.mediaMetadata.artworkDataType)
        assertNull(p.mediaMetadata.artworkUri)
        assertNull(p.currentMediaItem?.localConfiguration)          // no URI of any kind on the session's item
        assertFalse(p.mediaMetadata.toString().contains("T1"))
    }

    @Test fun sessionShowsThePositionAndStateOfTheEngine() = engineTest {
        val r = RemoteCommands(scope, engine).also { remote = it }
        val p = SessionPlayerRig(ApplicationProvider.getApplicationContext(), r, sink.hub).also { session = it }.player
        assertEquals(0, p.mediaItemCount)
        assertEquals(Player.STATE_IDLE, p.playbackState)
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        tick(4000)
        assertEquals(1, p.mediaItemCount)
        assertTrue(p.playWhenReady)
        assertEquals(4000L, p.currentPosition)
        assertEquals(200_000L, p.duration)
        engine.pause()
        assertFalse(p.playWhenReady)
        engine.handle(WebMessage.Stop(Item.Kind.track))
        assertEquals(0, p.mediaItemCount)
        assertEquals(Player.STATE_IDLE, p.playbackState)
    }

    // MARK: Artwork from the server

    @Test fun artworkLoaderReturnsTheServersImage() = engineTest {
        api.artworkAnswer = image()
        assertArrayEquals(image(), ArtworkLoader { api }.load(nowPlayingTrack(7, "晴天", "周杰伦")))
        assertEquals(listOf("7"), api.artworkCalls)
    }

    @Test fun artworkLoaderTakesNotFoundAndEmptyAsNoCover() = engineTest {
        api.artworkError = LarkError.Http(404, null)
        assertNull(ArtworkLoader { api }.load(nowPlayingTrack(7, "晴天", "周杰伦")))
        api.artworkError = null
        api.artworkAnswer = ByteArray(0)
        assertNull(ArtworkLoader { api }.load(nowPlayingTrack(7, "晴天", "周杰伦")))
    }

    @Test fun artworkLoaderThrowsWhatShouldBeAskedAgain() = engineTest {
        api.artworkError = LarkError.Http(503, null)
        assertTrue(runCatching { ArtworkLoader { api }.load(nowPlayingTrack(7, "晴天", "周杰伦")) }.exceptionOrNull() is LarkError.Http)
        assertTrue(runCatching { ArtworkLoader { null }.load(nowPlayingTrack(7, "晴天", "周杰伦")) }.exceptionOrNull() is LarkError.SignedOut)
    }

    @Test fun stopClearsNowPlaying() = engineTest {
        engine.handle(q(listOf(1), 0, 0, true))
        assertNotNull(info)
        engine.handle(WebMessage.Stop(Item.Kind.track))
        assertNull(info)
    }
}
