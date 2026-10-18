package lark.ui

import kotlinx.coroutines.ExperimentalCoroutinesApi
import lark.api.ApiTrack
import lark.api.LyricsDoc
import lark.app.ServicesTestCase
import lark.bridge.Item
import lark.bridge.NativeEvent
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.WebMessage
import lark.player.toItem
import lark.testing.EngineTestCase
import lark.testing.FakeCache
import lark.testing.FakeNetwork
import lark.testing.cachedFavorite
import lark.testing.episode
import lark.testing.lyricsDoc
import lark.testing.q
import lark.testing.track
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * iOS `OfflinePlayerModelTests`: the native offline player's model. The cached favorites as a list, and the engine
 * driven through the same messages the web sends, its state read back from the same events. No network anywhere.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class OfflinePlayerModelTest : EngineTestCase() {
    lateinit var model: OfflinePlayerModel
    var retries = 0

    /** The model as `AppServices.openOfflinePlayer` wires it: the engine's events reach it, then it is seeded. */
    private fun makeModel() {
        network.isOnline = false
        model = OfflinePlayerModel(EngineControl(engine), favorites = { cache.cachedFavorites() }, retry = { retries++ })
        engine.onEvent = { e ->
            sent.add(e)
            model.receive(e)
        }
        engine.emitAll()
    }

    private fun useFavorites(favs: List<ApiTrack>) {
        cache.favorites = favs
        for (t in favs) cache.local[t.track.id] = tmp("${t.track.id}.m4a")
    }

    @Test fun theListIsTheCachedFavoritesInTitleOrderWithNoNetwork() = engineTest {
        useFavorites(listOf(cachedFavorite(3, "cello"), cachedFavorite(1, "Banana"), cachedFavorite(2, "apple")))
        makeModel()
        assertEquals(listOf("2", "1", "3"), model.favorites.map { it.id })
        assertEquals(listOf("apple", "Banana", "cello"), model.favorites.map { it.title })
        assertEquals(0, api.favoritesCalls)
        assertTrue(api.randomFavoritesCalls.isEmpty())
        assertTrue(backend.loads.isEmpty())                     // opening it plays nothing by itself
        assertNull(model.current)
    }

    /** Numbers in titles sort as numbers, as iOS `localizedStandardCompare` does. */
    @Test fun titlesWithNumbersSortAsNumbers() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "Track 10"), cachedFavorite(2, "Track 9"), cachedFavorite(3, "track 1")))
        makeModel()
        assertEquals(listOf("track 1", "Track 9", "Track 10"), model.favorites.map { it.title })
    }

    @Test fun reloadPicksUpFavoritesCachedSince() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "A")))
        makeModel()
        useFavorites(listOf(cachedFavorite(1, "A"), cachedFavorite(2, "B")))
        model.reload()
        assertEquals(listOf("1", "2"), model.favorites.map { it.id })
    }

    @Test fun tappingASongPlaysItWithTheListAsTheQueue() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "A"), cachedFavorite(2, "B"), cachedFavorite(3, "C")))
        makeModel()
        model.play(model.favorites[1])
        idle()
        assertEquals(listOf("1", "2", "3"), engine.music.items.map { it.id })
        assertEquals(1, engine.music.index)
        assertEquals(QueueSource.favorites, engine.music.source)
        assertEquals(1, backend.loads.size)
        assertEquals(true, backend.loads.last().autoplay)
        assertEquals("2", model.current?.id)
        assertEquals(listOf("1", "2", "3"), model.queue.map { it.id })
        assertEquals(listOf("3"), model.upNext.map { it.item.id })
        assertEquals(listOf(2), model.upNext.map { it.index })
    }

    /** The engine's (and the web's) rule for a new list with shuffle on: what follows the chosen song is shuffled. */
    @Test fun tappingWithShuffleOnStartsTheChosenSongAndShufflesWhatFollows() = engineTest {
        useFavorites((1..6).map { cachedFavorite(it, "T$it") })
        engine.setModes(shuffle = true, repeatMode = RepeatMode.off)
        makeModel()
        model.play(model.favorites[2])
        idle()
        assertEquals("3", engine.current?.id)
        assertEquals(2, engine.music.index)
        assertEquals(setOf("4", "5", "6"), engine.music.upcoming.map { it.id }.toSet())
        assertEquals(listOf("4", "5", "6"), engine.modes.original)
        assertEquals(engine.music.upcoming.map { it.id }, model.upNext.map { it.item.id })   // the engine's order, not the list's
    }

    @Test fun tappingAnUpNextEntryJumpsWithoutRebuildingTheQueue() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "A"), cachedFavorite(2, "B"), cachedFavorite(3, "C")))
        makeModel()
        model.play(model.favorites[0])
        idle()
        val before = engine.music.items
        model.jump(2)
        idle()
        assertEquals(before, engine.music.items)
        assertEquals(2, engine.music.index)
        assertEquals("3", model.current?.id)
        assertEquals(2, backend.loads.size)
        assertTrue(model.upNext.isEmpty())
    }

    @Test fun modesReflectTheEngine() = engineTest {
        engine.setModes(shuffle = true, repeatMode = RepeatMode.one)    // set elsewhere (the web, the lock screen) before opening
        makeModel()
        assertTrue(model.shuffle)
        assertEquals(RepeatMode.one, model.repeatMode)

        engine.setModes(shuffle = false, repeatMode = RepeatMode.all)   // changed by the car meanwhile
        assertFalse(model.shuffle)
        assertEquals(RepeatMode.all, model.repeatMode)

        model.toggleShuffle()
        assertTrue(engine.modes.shuffle)
        assertTrue(model.shuffle)
        assertEquals(RepeatMode.all, engine.modes.repeatMode)          // the other mode is left alone

        model.cycleRepeat()                                             // all → one → off → all, as the web's button
        assertEquals(RepeatMode.one, engine.modes.repeatMode)
        assertEquals(RepeatMode.one, model.repeatMode)
        model.cycleRepeat()
        assertEquals(RepeatMode.off, engine.modes.repeatMode)
        model.cycleRepeat()
        assertEquals(RepeatMode.all, engine.modes.repeatMode)
        assertTrue(engine.modes.shuffle)
    }

    @Test fun transportDrivesTheEngine() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "A"), cachedFavorite(2, "B"), cachedFavorite(3, "C")))
        makeModel()
        model.play(model.favorites[0])
        idle()
        backend.start()
        assertTrue(model.playing)

        model.togglePlay()
        assertFalse(engine.playing)
        assertFalse(model.playing)
        model.togglePlay()
        idle()
        assertTrue(engine.playing)
        assertTrue(model.playing)

        model.next()
        idle()
        assertEquals(1, engine.music.index)
        assertEquals("2", model.current?.id)
        model.previous()                                                // near the start: the one before
        idle()
        assertEquals(0, engine.music.index)
        assertEquals("1", model.current?.id)

        model.seek(42_000)
        assertTrue(backend.calls.contains("seek:42000"))
        assertEquals(42_000L, model.positionMs)
    }

    @Test fun progressFollowsTheEngine() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "A", durationMs = 180_000)))
        makeModel()
        model.play(model.favorites[0])
        idle()
        backend.durationMs = 181_000
        backend.start()
        backend.playTo(5_000)
        assertEquals(5_000L, model.positionMs)
        assertEquals(181_000L, model.durationMs)
    }

    @Test fun episodeStatesLeaveTheMusicAlone() = engineTest {
        useFavorites(listOf(cachedFavorite(1, "A")))
        makeModel()
        model.play(model.favorites[0])
        idle()
        backend.start()
        model.receive(NativeEvent.State(Item.Kind.episode, "abcdefghijk", 0, playing = false, positionMs = 9, durationMs = 10,
            buffering = false, error = null, rate = 1.0))
        model.receive(NativeEvent.Queue(Item.Kind.episode, listOf(episode("abcdefghijk")), 0, QueueSource.list))
        assertEquals("1", model.current?.id)
        assertTrue(model.playing)
        assertEquals(listOf("1"), model.queue.map { it.id })
    }

    @Test fun artworkIsTheCachedCoverOrNothing() = engineTest {
        val cover = tmp("covers/1")
        model = OfflinePlayerModel(EngineControl(engine), favorites = { emptyList() }, retry = {},
            artwork = { if (it == 1L) cover else null })
        assertEquals(cover, model.artworkFile(track(1)))
        assertNull(model.artworkFile(track(2)))
        assertNull(model.artworkFile(episode("abcdefghijk")))
    }

    @Test fun retryAsksForTheServer() = engineTest {
        makeModel()
        model.retry()
        assertEquals(1, retries)
        assertTrue(model.connecting)
        model.retryFailed()
        assertFalse(model.connecting)
        assertTrue(model.stillOffline)
        model.retry()
        assertTrue(model.connecting)
        assertFalse(model.stillOffline)
    }

    @Test fun timeLabels() {
        assertEquals("0:00", OfflinePlayerModel.time(0))
        assertEquals("1:05", OfflinePlayerModel.time(65_400))
        assertEquals("1:02:05", OfflinePlayerModel.time(3_725_000))
        assertEquals("0:00", OfflinePlayerModel.time(-5))
    }

    // iOS OfflineLyricsTests: `OfflinePlayerLyricsTests`.

    /** The offline player reads the current song's cached lyrics (once per song). */
    @Test fun theCurrentSongsLyrics() = engineTest {
        cache.local[1] = tmp("1.m4a"); cache.local[2] = tmp("2.m4a")
        val reads = mutableListOf<Long>()
        val docs = mapOf(1L to lyricsDoc(lines = listOf(0L to "一")))
        val m = OfflinePlayerModel(EngineControl(engine), favorites = { emptyList() }, retry = {},
            lyrics = { reads += it; docs[it] })
        engine.onEvent = { m.receive(it) }
        engine.handle(q(listOf(1, 2), 0, 0, true))
        idle()
        assertEquals("一", m.lyrics?.lines?.first()?.text)
        m.lyrics; m.lyrics
        assertEquals(listOf(1L), reads)                                 // read once while the song plays
        engine.handle(WebMessage.Next(Item.Kind.track))
        idle()
        assertNull(m.lyrics)                                            // 2 has none cached
    }
}

/** iOS OfflineLyricsTests: `OfflineLyricsModelTests`, lyrics as the offline player shows them. */
class OfflineLyricsTest {
    @Test fun timedLinesFollowThePositionWithTheOffset() {
        val l = OfflineLyrics.of(lyricsDoc(lines = listOf(1000L to "一", 3000L to " ", 5000L to "三"), offset = 500))!!
        assertTrue(l.synced)
        assertNull(l.index(atMs = 1400))                                // before the first line (1000 + 500)
        assertEquals(0, l.index(atMs = 1500))
        assertEquals(1, l.index(atMs = 4000))
        assertEquals(0, l.nextWords(after = null))
        assertEquals(2, l.nextWords(after = 0))                         // the blank line is passed over
        assertNull(l.nextWords(after = 2))
        assertEquals(5500L, l.startMs(2))
    }

    @Test fun plainLyricsAreText() {
        val doc = LyricsDoc(found = true, synced = false, instrumental = null, lines = null, offsetMs = null, text = "第一行\n第二行")
        val l = OfflineLyrics.of(doc)!!
        assertFalse(l.synced)
        assertEquals("第一行\n第二行", l.text)
    }

    @Test fun nothingToShow() {
        assertNull(OfflineLyrics.of(lyricsDoc(found = false)))
        assertNull(OfflineLyrics.of(lyricsDoc(instrumental = true)))
        assertNull(OfflineLyrics.of(LyricsDoc(found = true, synced = false, instrumental = null, lines = emptyList(), offsetMs = null, text = " ")))
    }
}

/** iOS `OfflinePlayerAppTests`: the offline player as the app opens it from the can't-connect screen. */
@OptIn(ExperimentalCoroutinesApi::class)
class OfflinePlayerAppTest : ServicesTestCase() {
    private val page = "https://page.lark.test/"

    private fun offlineServices(): lark.app.AppServices {
        val cache = FakeCache()
        cache.favorites = listOf(cachedFavorite(1, "A"), cachedFavorite(2, "B"))
        cache.local[1] = File("/tmp/1.m4a"); cache.local[2] = File("/tmp/2.m4a")
        val network = FakeNetwork().also { it.isOnline = false }
        val s = makeServices(user = 3, cache = cache, network = network, server = page)
        s.eventSink = {}
        s.pageDidFail(android.webkit.WebViewClient.ERROR_HOST_LOOKUP, "net::ERR_INTERNET_DISCONNECTED")
        return s
    }

    private val lark.app.AppServices.model: OfflinePlayerModel? get() = offlinePlayer as OfflinePlayerModel?

    @Test fun playOfflineFavoritesOpensTheOfflinePlayerAndPlays() = servicesTest {
        val s = offlineServices()
        assertNull(s.offlinePlayer)
        s.playOfflineFavorites()
        val model = s.model!!
        assertTrue(s.engine!!.playing)
        assertEquals(listOf("1", "2"), model.favorites.map { it.id })
        assertEquals(s.engine?.current?.id, model.current?.id)
        assertNotNull(model.current)
    }

    @Test fun openingWhilePlayingKeepsTheSongAndShowsIt() = servicesTest {
        val s = offlineServices()
        val engine = s.engine!!
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(cachedFavorite(2, "B").toItem()), 0, 0, true, QueueSource.list))
        backend.start()
        val loads = backend.loads.size
        s.playOfflineFavorites()
        assertEquals(loads, backend.loads.size)
        assertEquals("2", s.model?.current?.id)
        assertEquals(true, s.model?.playing)
    }

    @Test fun theOfflinePlayerFollowsTheEngine() = servicesTest {
        val s = offlineServices()
        s.playOfflineFavorites()
        val model = s.model!!
        s.engine?.setModes(shuffle = true, repeatMode = RepeatMode.one)     // e.g. from the lock screen
        assertTrue(model.shuffle)
        assertEquals(RepeatMode.one, model.repeatMode)
    }

    @Test fun retryConnectionReturnsToTheWebAndPlaybackGoesOn() = servicesTest {
        val s = offlineServices()
        val loads = mutableListOf<String>()
        s.loadPage = { loads += it }
        s.playOfflineFavorites()
        backend.start()
        val model = s.model!!
        val callsBefore = backend.calls.toList()
        val loadsBefore = backend.loads.size

        model.retry()                                                   // still down
        assertEquals(listOf(page), loads)
        assertTrue(model.connecting)
        assertNotNull(s.offlinePlayer)
        s.pageDidFail(android.webkit.WebViewClient.ERROR_CONNECT, "net::ERR_CONNECTION_REFUSED")
        assertTrue(s.pageFailed)
        assertFalse(model.connecting)
        assertTrue(model.stillOffline)
        assertSame(model, s.offlinePlayer)

        model.retry()                                                   // the server answers: back to the web UI
        assertEquals(2, loads.size)
        s.pageDidLoad()
        assertNull(s.offlinePlayer)
        assertFalse(s.pageFailed)
        assertTrue(s.engine!!.playing)                                  // the engine never stopped
        assertEquals(callsBefore, backend.calls)
        assertEquals(loadsBefore, backend.loads.size)
    }

    @Test fun closeGoesBackToTheErrorScreen() = servicesTest {
        val s = offlineServices()
        s.playOfflineFavorites()
        s.closeOfflinePlayer()
        assertNull(s.offlinePlayer)
        assertTrue(s.pageFailed)
        assertTrue(s.engine!!.playing)
    }

    /** The covers and lyrics the services hand the model are the signed-in user's cached ones. */
    @Test fun theModelReadsTheCachedLyrics() = servicesTest {
        val s = offlineServices()
        (s.cache as FakeCache).lyrics[1] = lyricsDoc(lines = listOf(0L to "一"))
        s.engine!!.handle(WebMessage.SetQueue(Item.Kind.track, listOf(cachedFavorite(1, "A").toItem()), 0, 0, true, QueueSource.list))
        s.openOfflinePlayer()
        assertEquals("一", s.model?.lyrics?.lines?.first()?.text)
    }
}
