package lark.player

import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Item
import lark.bridge.NativePrefs
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.WebMessage
import lark.bridge.setting
import lark.testing.EngineTestCase
import lark.testing.episode
import lark.testing.gainTrack
import lark.testing.q
import lark.testing.track
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Port of ios/Tests/PlayerEngineTests.swift. */
class PlayerEngineTest : EngineTestCase() {
    private fun episodes(vararg items: Item, positionMs: Long? = 0) =
        WebMessage.SetQueue(Item.Kind.episode, items.toList(), 0, positionMs, true, QueueSource.list)

    @Test fun setQueuePlaysCachedFromDiskElseStreams() = engineTest {
        cache.local[7] = tmp("7.m4a")
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(track(7), track(8)), 0, 0, true, QueueSource.list))
        assertEquals(MediaSource.File(tmp("7.m4a")), backend.loads.last().src)
        assertEquals(true, backend.loads.last().autoplay)
        backend.finish()
        assertEquals(remote(8), backend.loads.last().src)
        assertEquals(1, engine.music.index)
    }

    @Test fun qualityFromPrefsIsUsedForStreams() = engineTest {
        engine.handle(WebMessage.SetPrefs(NativePrefs("lossless", true)))
        engine.handle(q(listOf(8), 0, 0, true))
        assertEquals(MediaSource.Remote(android.net.Uri.parse("https://lark.test/api/v1/tracks/8/stream?quality=lossless"), "T1"), backend.loads.last().src)
    }

    @Test fun editOfSameCurrentItemDoesNotReload() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        engine.handle(q(listOf(7, 9, 8), 0, null, true))   // enqueueNext(9)
        assertEquals(1, backend.loads.size); assertEquals(listOf("7", "9", "8"), ids)
        assertEquals(listOf("7", "9", "8"), queues(Item.Kind.track).last().items.map { it.id })
    }

    // Dragging the current track elsewhere in the queue sheet: the web sends the new order with the
    // current entry's new index and no position. It keeps playing, unloaded and unreset.
    @Test fun editMovingCurrentKeepsLoadedItem() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4), 1, 0, true))
        val loads = backend.loads.size
        engine.handle(q(listOf(1, 3, 4, 2), 3, null, true))
        assertEquals(loads, backend.loads.size)        // not reloaded
        assertEquals(3, engine.music.index)
        assertEquals("2", engine.current?.id)
        assertEquals(listOf("1", "3", "4", "2"), queues(Item.Kind.track).last().items.map { it.id })
        assertEquals(3, queues(Item.Kind.track).last().index)
    }

    // Removing an entry before the current one only renumbers it.
    @Test fun editRemovingAnEarlierEntryKeepsLoadedItem() = engineTest {
        engine.handle(q(listOf(1, 2, 3), 1, 0, true))
        val loads = backend.loads.size
        engine.handle(q(listOf(2, 3), 0, null, true))
        assertEquals(loads, backend.loads.size)
        assertEquals(0, engine.music.index)
        assertEquals("2", engine.current?.id)
    }

    @Test fun removingTheCurrentItemLoadsTheOneThatSlidIntoPlace() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        engine.handle(q(listOf(8), 0, null, true))
        assertEquals(2, backend.loads.size)
        assertEquals(0L, backend.loads.last().startMs)
    }

    @Test fun episodeAndMusicQueuesAreIndependent() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.start(); backend.playTo(12_000)
        engine.handle(episodes(episode("abcdefghijk"), positionMs = 60_000))
        assertEquals(Item.Kind.episode, engine.active); assertEquals(2, engine.music.items.size)
        assertEquals(12_000L, engine.music.positionMs)     // the outgoing position is kept in its queue
        assertEquals(60_000L, backend.loads.last().startMs)
        assertEquals(MediaSource.Remote(android.net.Uri.parse("https://lark.test/api/v1/episodes/abcdefghijk/stream?kind=audio"), "T1"),
            backend.loads.last().src)
        backend.start(); backend.playTo(75_000)
        engine.handle(WebMessage.Play(Item.Kind.track))          // back to music: the episode's progress is saved, music resumes where it was
        idle()
        assertEquals(lark.testing.FakeLarkApi.ProgressCall("abcdefghijk", 75, false), api.progressCalls.last())
        assertEquals(Item.Kind.track, engine.active)
        assertEquals(12_000L, backend.loads.last().startMs)
        assertEquals(75_000L, engine.episodes.positionMs)
        assertEquals(1, engine.episodes.items.size)
    }

    /**
     * The web mirrors each kind from that kind's own state events: a switch by setQueue must say the outgoing
     * kind stopped playing, or its play button stays stale (and a web preview could play over native).
     */
    @Test fun switchingKindBySetQueueSendsStateForBothKinds() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        backend.start()
        assertEquals(true, states.last { it.kind == Item.Kind.track }.playing)
        sent.clear()
        engine.handle(episodes(episode("abcdefghijk")))
        assertEquals(false, states.last { it.kind == Item.Kind.track }.playing)
        assertEquals("7", states.last { it.kind == Item.Kind.track }.itemId)
        sent.clear()
        backend.start()
        engine.handle(q(listOf(9), 0, 0, true))
        assertEquals(false, states.last { it.kind == Item.Kind.episode }.playing)
        assertEquals("abcdefghijk", states.last { it.kind == Item.Kind.episode }.itemId)
    }

    @Test fun musicEditWhileAnEpisodePlaysDoesNotTouchPlayback() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, true))
        engine.handle(episodes(episode("abcdefghijk")))
        val loads = backend.loads.size
        engine.handle(q(listOf(7, 9, 8), 0, null, false))
        assertEquals(loads, backend.loads.size)
        assertEquals(Item.Kind.episode, engine.active)
        assertEquals(listOf("7", "9", "8"), ids)
    }

    @Test fun pauseForWebPausesWhicheverIsActive() = engineTest {
        engine.handle(episodes(episode("abcdefghijk")))
        backend.start()
        assertTrue(engine.playing)
        engine.handle(WebMessage.PauseForWeb)
        assertEquals("pause", backend.calls.last())
        assertFalse(engine.playing)
        assertEquals(Item.Kind.episode, states.last().kind)
        assertEquals(false, states.last().playing)
    }

    @Test fun pauseOfTheOtherKindIsIgnored() = engineTest {
        engine.handle(q(listOf(7), 0, 0, true))
        backend.start()
        engine.handle(WebMessage.Pause(Item.Kind.episode))
        assertTrue(engine.playing)
        engine.handle(WebMessage.Pause(null))
        assertFalse(engine.playing)
    }

    @Test fun stopEpisodeClearsItsQueueAndFallsBackToMusicPaused() = engineTest {
        engine.handle(q(listOf(7, 8), 1, 5_000, true))
        backend.start()
        engine.handle(episodes(episode("abcdefghijk")))
        backend.start(); backend.playTo(30_000)
        engine.handle(WebMessage.Stop(Item.Kind.episode))
        idle()
        assertTrue(engine.episodes.items.isEmpty())
        assertEquals(Item.Kind.track, engine.active)
        assertFalse(engine.playing)
        assertEquals("stop", backend.calls.last())
        assertEquals(lark.testing.FakeLarkApi.ProgressCall("abcdefghijk", 30, false), api.progressCalls.last())
        assertEquals(0, queues(Item.Kind.episode).last().items.size)
        assertEquals(1, engine.music.index)
        engine.handle(WebMessage.Play(Item.Kind.track))
        assertEquals(remote(8), backend.loads.last().src)
    }

    @Test fun stateEventsCarryKindItemAndPosition() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 4_000, true))
        backend.start()
        val s = states.last()
        assertEquals(Item.Kind.track, s.kind); assertEquals("7", s.itemId); assertEquals(0, s.index)
        assertEquals(true, s.playing); assertEquals(4_000L, s.positionMs); assertEquals(200_000L, s.durationMs)
        assertNull(s.error)
        backend.playTo(6_000)
        assertEquals(6_000L, states.last().positionMs)
    }

    @Test fun helloRepliesWithBothQueues() = engineTest {
        engine.handle(q(listOf(7, 8), 1, 0, false))
        sent.clear()
        engine.handle(WebMessage.Hello("nothing"))
        idle()
        assertEquals(1, queues(Item.Kind.track).size); assertEquals(1, queues(Item.Kind.episode).size)
        assertEquals(listOf("7", "8"), queues(Item.Kind.track).first().items.map { it.id }); assertEquals(1, queues(Item.Kind.track).first().index)
        assertFalse(states.isEmpty())
        assertEquals(setOf(Item.Kind.track, Item.Kind.episode), states.map { it.kind }.toSet())
    }

    @Test fun nextPrevSeekSkipOnActive() = engineTest {
        engine.handle(q(listOf(7, 8, 9), 0, 0, true))
        backend.start()
        engine.handle(WebMessage.Next(Item.Kind.track))
        assertEquals(1, engine.music.index)
        backend.start(); backend.playTo(5_000)
        engine.handle(WebMessage.Prev(Item.Kind.track))                    // past 3 s: back to the start of this one
        assertEquals("seek:0", backend.calls.last()); assertEquals(1, engine.music.index)
        engine.handle(WebMessage.Prev(Item.Kind.track))
        assertEquals(0, engine.music.index)
        assertEquals(remote(7), backend.loads.last().src)
        engine.handle(WebMessage.Seek(Item.Kind.track, 50_000))
        assertEquals("seek:50000", backend.calls.last())
        engine.handle(WebMessage.Skip(Item.Kind.track, -15_000))
        assertEquals("seek:35000", backend.calls.last())
        engine.handle(WebMessage.Skip(Item.Kind.track, -100_000))
        assertEquals("seek:0", backend.calls.last())
    }

    @Test fun rateAppliesToEpisodesOnly() = engineTest {
        engine.handle(WebMessage.SetRate(1.5))
        engine.handle(q(listOf(7), 0, 0, true))
        assertEquals(1f, backend.lastRate)
        engine.handle(episodes(episode("abcdefghijk")))
        assertEquals(1.5f, backend.lastRate)
        assertEquals(1.5, states.last().rate, 0.0)
    }

    @Test fun finishedEpisodeStartsTheNextAtItsSavedPosition() = engineTest {
        engine.handle(episodes(episode("abcdefghijk"), episode("bbbbbbbbbbb", positionS = 120)))
        backend.start(); backend.finish()
        assertEquals(1, engine.episodes.index)
        assertEquals(120_000L, backend.loads.last().startMs)
    }

    @Test fun playWithNothingLoadedAfterRestoreLoadsAtSavedPosition() = engineTest {
        engine.handle(q(listOf(7, 8), 1, 9_000, false))
        assertEquals(false, backend.loads.last().autoplay)
        engine.handle(WebMessage.Play(Item.Kind.track))
        assertEquals("play", backend.calls.last())
        assertTrue(engine.playing)
    }

    // MARK: - Shuffle and repeat (the web's modes, music only)

    private fun items(ids: IntRange) = ids.map(::track)

    @Test fun repeatOneLoopsOnNaturalEndAndCountsTwoPlays() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.track, items(1..3), 0, 0, true, QueueSource.list))
        engine.handle(WebMessage.SetModes(false, RepeatMode.one))
        backend.playTo(31_000); backend.finish()
        assertEquals("1", engine.current?.id)
        backend.playTo(31_000); backend.finish()
        assertEquals(2, engine.events.pending.count { it.trackId == 1L })
    }

    @Test fun repeatOneUserNextStillMoves() = engineTest {
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        engine.handle(WebMessage.SetModes(false, RepeatMode.one))
        engine.next()
        assertEquals("2", engine.current?.id)
        engine.prev()
        assertEquals("1", engine.current?.id)
        backend.finish()                                       // only the natural end loops
        assertEquals("1", engine.current?.id)
    }

    @Test fun repeatAllWrapsWithoutRefill() = engineTest {
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2), 0, 0, true, QueueSource.radio))
        backend.start(); backend.finish()
        backend.start(); backend.finish()
        assertEquals("1", engine.current?.id)
        assertEquals(0, engine.music.index)
        assertEquals(listOf("1", "2"), ids)
        assertTrue(engine.playing)
        idle()
        assertTrue(api.radioCalls.isEmpty()); assertTrue(api.randomTracksCalls.isEmpty()); assertTrue(api.randomFavoritesCalls.isEmpty())
        // the user's next at the last item wraps too
        engine.next()
        engine.next()
        assertEquals("1", engine.current?.id)
        // repeat off again: the radio refills as before
        engine.handle(WebMessage.SetModes(false, RepeatMode.off))
        idle()
        assertEquals(1, api.radioCalls.size)
    }

    @Test fun repeatAllShuffleStartsNewShuffledPass() = engineTest {
        engine.handle(WebMessage.SetModes(true, RepeatMode.all))
        engine.handle(q(listOf(1, 2, 3, 4), 0, 0, true, QueueSource.favorites))
        val first = ids
        assertEquals("1", first.first())
        repeat(4) { backend.start(); backend.finish() }
        assertEquals(0, engine.music.index)
        assertEquals(listOf("1", "2", "3", "4"), ids.sorted())
        assertNotEquals(first, ids)                          // a new shuffled pass
        assertNotEquals(first.last(), engine.current?.id)    // never the item that just ended
        idle()
        assertTrue(api.randomFavoritesCalls.isEmpty())
    }

    @Test fun shuffleOnKeepsCurrentLoaded() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4, 5), 1, 0, true))
        backend.start(); backend.playTo(5_000)
        val loads = backend.loads.size
        val generation = backend.generation
        sent.clear()
        engine.handle(WebMessage.SetModes(true, RepeatMode.off))
        assertEquals(loads, backend.loads.size)
        assertEquals(generation, backend.generation)
        assertEquals("2", engine.current?.id); assertEquals(1, engine.music.index)
        assertTrue(engine.playing)
        assertEquals(listOf("1", "2"), ids.take(2))
        assertEquals(listOf("3", "4", "5"), ids.drop(2).sorted())
        assertNotEquals(listOf("1", "2", "3", "4", "5"), ids)
        assertEquals(listOf("3", "4", "5"), engine.modes.original)
        assertEquals(ids, queues(Item.Kind.track).last().items.map { it.id })
        assertEquals(MediaSource.Remote(api.streamUrl(engine.music.items[2], "high"), "T1"), backend.preloads.last())
    }

    @Test fun shuffleOffRestoresOrder() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4, 5), 0, 0, true))
        engine.handle(WebMessage.SetModes(true, RepeatMode.off))
        backend.start(); backend.finish()                     // one shuffled item played
        val now = engine.current?.id
        val loads = backend.loads.size
        engine.handle(WebMessage.SetModes(false, RepeatMode.off))
        assertEquals(loads, backend.loads.size)
        assertEquals(now, engine.current?.id)
        assertEquals(listOf("2", "3", "4", "5").filter { it != now }, ids.drop(2))
        assertNull(engine.modes.original)
    }

    @Test fun aListPlayedWhileShuffledIsShuffledAfterTheChosenItemAJumpIsNot() = engineTest {
        engine.handle(WebMessage.SetModes(true, RepeatMode.off))
        engine.handle(q(listOf(1, 2, 3, 4, 5), 1, 0, true))
        assertEquals(listOf("1", "2"), ids.take(2))
        assertNotEquals(listOf("1", "2", "3", "4", "5"), ids)
        assertEquals(listOf("3", "4", "5"), engine.modes.original)
        val order = ids
        engine.handle(WebMessage.SetQueue(Item.Kind.track, engine.music.items, 3, 0, true, QueueSource.list))
        assertEquals(order, ids)
        assertEquals(3, engine.music.index)
    }

    @Test fun aMoveWhileShuffledKeepsItsNewNeighbourOnShuffleOff() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4, 5), 0, 0, true))
        engine.handle(WebMessage.SetModes(true, RepeatMode.off))      // random 0: [1, 3, 4, 5, 2]
        assertEquals(listOf("1", "3", "4", "5", "2"), ids)
        engine.handle(q(listOf(1, 5, 3, 4, 2), 0, null, true))   // the queue sheet: 5 dragged up
        engine.handle(WebMessage.SetModes(false, RepeatMode.off))
        assertEquals(listOf("1", "2", "5", "3", "4"), ids)                 // 5 still right before 3
    }

    @Test fun episodesIgnoreModes() = engineTest {
        engine.handle(WebMessage.SetModes(true, RepeatMode.one))
        engine.handle(episodes(episode("aaaaaaaaaaa"), episode("bbbbbbbbbbb"), episode("ccccccccccc")))
        assertEquals(listOf("aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"), engine.episodes.items.map { it.id })
        backend.start(); backend.finish()
        assertEquals("bbbbbbbbbbb", engine.current?.id)
        val ep = states.last { it.kind == Item.Kind.episode }
        assertEquals(false, ep.shuffle); assertEquals(RepeatMode.off, ep.repeat)
    }

    @Test fun modesPersistAndRestore() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4), 0, 0, false))
        engine.handle(WebMessage.SetModes(true, RepeatMode.all))
        val order = ids
        makeEngine()
        assertEquals(true, engine.modes.shuffle); assertEquals(RepeatMode.all, engine.modes.repeatMode)
        assertEquals(listOf("2", "3", "4"), engine.modes.original)
        assertEquals(order, ids)
        // A file written before the modes existed (v0.1.0): no `modes` key, both off.
        val s = store.load()!!
        store.save(s.copy(modes = null))
        val json = tmp("queues-1.json").readText()
        assertFalse(json.contains("\"modes\""))
        makeEngine()
        assertEquals(PlayModes(), engine.modes)
        assertEquals(order, ids)
    }

    @Test fun stateCarriesModes() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        assertEquals(false, states.last().shuffle); assertEquals(RepeatMode.off, states.last().repeat)
        engine.handle(WebMessage.SetModes(true, RepeatMode.one))
        val st = states.last { it.kind == Item.Kind.track }
        assertEquals(true, st.shuffle); assertEquals(RepeatMode.one, st.repeat)
        sent.clear()
        engine.emitAll()
        assertEquals(RepeatMode.one, states.first { it.kind == Item.Kind.track }.repeat)
        assertEquals(RepeatMode.off, states.first { it.kind == Item.Kind.episode }.repeat)
    }

    /**
     * Repeat all, offline: a cached favorite stands in for a track that can't play; it plays once and leaves,
     * and the interrupted track is tried again before the queue starts over (the web's transient substitute).
     */
    @Test fun repeatAllSubstituteIsTransient() = engineTest {
        cache.favorites = listOf(trackModel(9))
        cache.local[9] = tmp("9.m4a")
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2), 1, 0, true))
        network.isOnline = false
        backend.fail(network = true)
        assertEquals(listOf("1", "2", "9", "2"), ids); assertEquals("9", engine.current?.id)
        network.isOnline = true
        backend.start(); backend.finish()                     // the substitute ends: it leaves, 2 is retried
        assertEquals(listOf("1", "2"), ids); assertEquals("2", engine.current?.id)
        backend.start(); backend.finish()
        assertEquals(listOf("1", "2"), ids); assertEquals("1", engine.current?.id)
    }

    @Test fun repeatOffKeepsTheSubstitute() = engineTest {
        cache.favorites = listOf(trackModel(9))
        cache.local[9] = tmp("9.m4a")
        engine.handle(q(listOf(1, 2), 1, 0, true))
        network.isOnline = false
        backend.fail(network = true)
        network.isOnline = true
        backend.start(); backend.finish()
        assertEquals(listOf("1", "2", "9", "2"), ids); assertEquals("2", engine.current?.id)
    }

    // MARK: Repeat all never loops on what cannot play (review: a failing queue, no token, nothing cached)

    @Test fun repeatAllWithEveryTrackFailingStopsInsteadOfLooping() = engineTest {
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.fail(network = false); backend.fail(network = false); backend.fail(network = false)
        assertEquals(3, backend.loads.size)                // each once, then the brake: no wrap onto failed items
        assertFalse(engine.playing)
        assertEquals(texts.cannotPlay, states.last().error)
    }

    @Test fun repeatAllOneFailingTrackFallsBackToACachedFavorite() = engineTest {
        cache.favorites = listOf(trackModel(9))
        cache.local[9] = tmp("9.m4a")
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1), 0, 0, true))
        backend.fail(network = false)
        assertEquals(2, backend.loads.size)
        assertEquals(MediaSource.File(tmp("9.m4a")), backend.loads.last().src)   // never-stop's favorite, not item 1 again
    }

    @Test fun repeatAllWithNoTokenAndNothingCachedStopsWithoutRecursing() = engineTest {
        api.token = null
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2), 1, 0, true))   // the last item: the wrap is the next choice
        assertTrue(backend.loads.isEmpty())
        assertFalse(engine.playing)
        assertEquals(texts.cannotPlay, states.last().error)
    }

    @Test fun repeatAllWrapSkipsFailedAndStartsLocalWhenItMust() = engineTest {
        cache.local[3] = tmp("3.m4a")
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2, 3), 1, 0, true))
        network.isOnline = false
        backend.start(); backend.finish()                     // offline: 3 is on the phone
        assertEquals("3", engine.current?.id)
        backend.start(); backend.finish()                     // the wrap: only 3 can play offline
        assertEquals(2, engine.music.index)
        assertEquals(MediaSource.File(tmp("3.m4a")), backend.loads.last().src)
    }

    // MARK: Repeat one: the looped stream is cached, so it is fetched once

    @Test fun repeatOnePrefetchesTheCurrentStream() = engineTest {
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.start()
        cache.prefetched.clear()
        engine.handle(WebMessage.SetModes(false, RepeatMode.one))
        assertEquals(1L, cache.prefetched.last().first())
        cache.local[1] = tmp("1.m4a"); cache.prefetched.clear()
        backend.finish(); backend.start()
        assertFalse(cache.prefetched.any { 1L in it })   // on the phone now: not asked again
    }

    // MARK: - Loudness gain

    @Test fun trackLoadsWithItsGain() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(gainTrack(1, -6.0), gainTrack(2, null)), 0, 0, true, QueueSource.list))
        assertEquals(0.501f, backend.gains.last(), 0.001f)
        assertEquals("the preloaded next item carries its own gain (unmeasured: 1)", 1f, backend.preloadGains.last())
        backend.start(); backend.finish()
        assertEquals(1f, backend.gains.last())
    }

    @Test fun thePreloadedItemCarriesItsOwnGain() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(gainTrack(1, null), gainTrack(2, -12.0)), 0, 0, true, QueueSource.list))
        assertEquals(1f, backend.gains.last())
        assertEquals(0.251f, backend.preloadGains.last(), 0.001f)
    }

    @Test fun loudnessOffLoadsAtUnity() = engineTest {
        engine.handle(WebMessage.SetPrefs(NativePrefs("high", true, loudness = false)))
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(gainTrack(1, -6.0), gainTrack(2, -6.0)), 0, 0, true, QueueSource.list))
        assertEquals(1f, backend.gains.last())
        assertEquals(1f, backend.preloadGains.last())
    }

    /** Switching it while a track plays changes that track's level at once, and the preloaded next one's. */
    @Test fun loudnessSwitchAppliesToThePlayingTrack() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(gainTrack(1, -6.0), gainTrack(2, -6.0)), 0, 0, true, QueueSource.list))
        backend.start()
        val loads = backend.loads.size
        val preloads = backend.preloads.size
        engine.handle(WebMessage.SetPrefs(NativePrefs("high", true, loudness = false)))
        assertEquals("not reloaded", loads, backend.loads.size)
        assertEquals("the preloaded item is changed in place, not preloaded again", preloads, backend.preloads.size)
        assertEquals(1f, backend.currentGains.last())
        assertEquals(1f, backend.nextGains.last())
        engine.handle(WebMessage.SetPrefs(NativePrefs("high", true, loudness = true)))
        assertEquals(0.501f, backend.currentGains.last(), 0.001f)
        assertEquals(0.501f, backend.nextGains.last(), 0.001f)
        assertEquals(preloads, backend.preloads.size)
        // The same prefs again change nothing.
        val calls = backend.currentGains.size
        engine.handle(WebMessage.SetPrefs(NativePrefs("high", true, loudness = true)))
        assertEquals(calls, backend.currentGains.size)
    }

    @Test fun episodesLoadAtUnity() = engineTest {
        val e = episode("a")
        val ep = e.copy(meta = e.meta.setting("gain_db", JsonPrimitive(-6)))
        engine.handle(episodes(ep))
        assertEquals(1f, backend.gains.last())
    }

    @Test fun networkRetryKeepsTheGain() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(gainTrack(1, -6.0)), 0, 0, true, QueueSource.list))
        backend.start(); backend.playTo(5_000)
        backend.fail(network = true)
        scheduler.advance(2.0)
        assertEquals(2, backend.loads.size)
        assertEquals(0.501f, backend.gains.last(), 0.001f)
    }
}
