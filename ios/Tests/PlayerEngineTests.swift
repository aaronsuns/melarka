import XCTest
@testable import Lark

final class PlayerEngineTests: EngineTestCase {
    func testSetQueuePlaysCachedFromDiskElseStreams() {
        cache.local = [7: tmp("7.m4a")]
        engine.handle(.setQueue(.init(kind: .track, items: [track(7), track(8)], index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("7.m4a")))
        XCTAssertEqual(backend.loads.last?.2, true)
        backend.finish()
        XCTAssertEqual(backend.loads.last?.0, .remote(URL(string: "https://lark.test/api/v1/tracks/8/stream?quality=high")!, token: "T1"))
        XCTAssertEqual(engine.music.index, 1)
    }

    func testQualityFromPrefsIsUsedForStreams() {
        engine.handle(.setPrefs(NativePrefs(quality: "lossless", carLyrics: true)))
        engine.handle(.setQueue(q([8], index: 0, pos: 0, play: true)))
        XCTAssertEqual(backend.loads.last?.0, .remote(URL(string: "https://lark.test/api/v1/tracks/8/stream?quality=lossless")!, token: "T1"))
    }

    func testEditOfSameCurrentItemDoesNotReload() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        engine.handle(.setQueue(q([7, 9, 8], index: 0, pos: nil, play: true)))   // enqueueNext(9)
        XCTAssertEqual(backend.loads.count, 1); XCTAssertEqual(engine.music.items.map(\.id), ["7", "9", "8"])
        XCTAssertEqual(queues(.track).last?.items.map(\.id), ["7", "9", "8"])
    }

    // Dragging the current track elsewhere in the queue sheet: the web sends the new order with the
    // current entry's new index and no position. It keeps playing, unloaded and unreset.
    func testEditMovingCurrentKeepsLoadedItem() {
        engine.handle(.setQueue(q([1, 2, 3, 4], index: 1, pos: 0, play: true)))
        let loads = backend.loads.count
        engine.handle(.setQueue(q([1, 3, 4, 2], index: 3, pos: nil, play: true)))
        XCTAssertEqual(backend.loads.count, loads)        // not reloaded
        XCTAssertEqual(engine.music.index, 3)
        XCTAssertEqual(engine.current?.id, "2")
        XCTAssertEqual(queues(.track).last?.items.map(\.id), ["1", "3", "4", "2"])
        XCTAssertEqual(queues(.track).last?.index, 3)
    }

    // Removing an entry before the current one only renumbers it.
    func testEditRemovingAnEarlierEntryKeepsLoadedItem() {
        engine.handle(.setQueue(q([1, 2, 3], index: 1, pos: 0, play: true)))
        let loads = backend.loads.count
        engine.handle(.setQueue(q([2, 3], index: 0, pos: nil, play: true)))
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(engine.music.index, 0)
        XCTAssertEqual(engine.current?.id, "2")
    }

    func testRemovingTheCurrentItemLoadsTheOneThatSlidIntoPlace() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        engine.handle(.setQueue(q([8], index: 0, pos: nil, play: true)))
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(backend.loads.last?.1, 0)
    }

    func testEpisodeAndMusicQueuesAreIndependent() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 12_000)
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 60_000, play: true, source: .list)))
        XCTAssertEqual(engine.active, .episode); XCTAssertEqual(engine.music.items.count, 2)
        XCTAssertEqual(engine.music.positionMs, 12_000)     // the outgoing position is kept in its queue
        XCTAssertEqual(backend.loads.last?.1, 60_000)
        XCTAssertEqual(backend.loads.last?.0, .remote(URL(string: "https://lark.test/api/v1/episodes/abcdefghijk/stream?kind=audio")!, token: "T1"))
        backend.start(); backend.play(to: 75_000)
        engine.handle(.play(.track))          // back to music: the episode's progress is saved, music resumes where it was
        await engine.idle()
        XCTAssertEqual(api.progressCalls.last, .init(id: "abcdefghijk", positionS: 75, played: false))
        XCTAssertEqual(engine.active, .track)
        XCTAssertEqual(backend.loads.last?.1, 12_000)
        XCTAssertEqual(engine.episodes.positionMs, 75_000)
        XCTAssertEqual(engine.episodes.items.count, 1)
    }

    /// The web mirrors each kind from that kind's own state events: a switch by setQueue must say the outgoing
    /// kind stopped playing, or its play button stays stale (and a web preview could play over native).
    func testSwitchingKindBySetQueueSendsStateForBothKinds() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.start()
        XCTAssertEqual(states.last(where: { $0.kind == .track })?.playing, true)
        sent = []
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(states.last(where: { $0.kind == .track })?.playing, false)
        XCTAssertEqual(states.last(where: { $0.kind == .track })?.itemId, "7")
        sent = []
        backend.start()
        engine.handle(.setQueue(q([9], index: 0, pos: 0, play: true)))
        XCTAssertEqual(states.last(where: { $0.kind == .episode })?.playing, false)
        XCTAssertEqual(states.last(where: { $0.kind == .episode })?.itemId, "abcdefghijk")
    }

    func testMusicEditWhileAnEpisodePlaysDoesNotTouchPlayback() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        let loads = backend.loads.count
        engine.handle(.setQueue(q([7, 9, 8], index: 0, pos: nil, play: false)))
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(engine.active, .episode)
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "9", "8"])
    }

    func testPauseForWebPausesWhicheverIsActive() {
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        backend.start()
        XCTAssertTrue(engine.playing)
        engine.handle(.pauseForWeb)
        XCTAssertEqual(backend.calls.last, "pause")
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(states.last?.kind, .episode)
        XCTAssertEqual(states.last?.playing, false)
    }

    func testPauseOfTheOtherKindIsIgnored() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start()
        engine.handle(.pause(.episode))
        XCTAssertTrue(engine.playing)
        engine.handle(.pause(nil))
        XCTAssertFalse(engine.playing)
    }

    func testStopEpisodeClearsItsQueueAndFallsBackToMusicPaused() async {
        engine.handle(.setQueue(q([7, 8], index: 1, pos: 5_000, play: true)))
        backend.start()
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        backend.start(); backend.play(to: 30_000)
        engine.handle(.stop(.episode))
        await engine.idle()
        XCTAssertTrue(engine.episodes.items.isEmpty)
        XCTAssertEqual(engine.active, .track)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.calls.last, "stop")
        XCTAssertEqual(api.progressCalls.last, .init(id: "abcdefghijk", positionS: 30, played: false))
        XCTAssertEqual(queues(.episode).last?.items.count, 0)
        XCTAssertEqual(engine.music.index, 1)
        engine.handle(.play(.track))
        XCTAssertEqual(backend.loads.last?.0, .remote(URL(string: "https://lark.test/api/v1/tracks/8/stream?quality=high")!, token: "T1"))
    }

    func testStateEventsCarryKindItemAndPosition() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 4_000, play: true)))
        backend.start()
        let s = states.last
        XCTAssertEqual(s?.kind, .track); XCTAssertEqual(s?.itemId, "7"); XCTAssertEqual(s?.index, 0)
        XCTAssertEqual(s?.playing, true); XCTAssertEqual(s?.positionMs, 4_000); XCTAssertEqual(s?.durationMs, 200_000)
        XCTAssertNil(s?.error)
        backend.play(to: 6_000)
        XCTAssertEqual(states.last?.positionMs, 6_000)
    }

    func testHelloRepliesWithBothQueues() async {
        engine.handle(.setQueue(q([7, 8], index: 1, pos: 0, play: false)))
        sent = []
        engine.handle(.hello(onOpen: "nothing"))
        await engine.idle()
        XCTAssertEqual(queues(.track).count, 1); XCTAssertEqual(queues(.episode).count, 1)
        XCTAssertEqual(queues(.track).first?.items.map(\.id), ["7", "8"]); XCTAssertEqual(queues(.track).first?.index, 1)
        XCTAssertFalse(states.isEmpty)
        XCTAssertEqual(Set(states.map(\.kind)), [.track, .episode])
    }

    func testNextPrevSeekSkipOnActive() {
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        backend.start()
        engine.handle(.next(.track))
        XCTAssertEqual(engine.music.index, 1)
        backend.start(); backend.play(to: 5_000)
        engine.handle(.prev(.track))                    // past 3 s: back to the start of this one
        XCTAssertEqual(backend.calls.last, "seek:0"); XCTAssertEqual(engine.music.index, 1)
        engine.handle(.prev(.track))
        XCTAssertEqual(engine.music.index, 0)
        XCTAssertEqual(backend.loads.last?.0, .remote(URL(string: "https://lark.test/api/v1/tracks/7/stream?quality=high")!, token: "T1"))
        engine.handle(.seek(.track, ms: 50_000))
        XCTAssertEqual(backend.calls.last, "seek:50000")
        engine.handle(.skip(.track, ms: -15_000))
        XCTAssertEqual(backend.calls.last, "seek:35000")
        engine.handle(.skip(.track, ms: -100_000))
        XCTAssertEqual(backend.calls.last, "seek:0")
    }

    func testRateAppliesToEpisodesOnly() {
        engine.handle(.setRate(1.5))
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        XCTAssertEqual(backend.rate, 1)
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(backend.rate, 1.5)
        XCTAssertEqual(states.last?.rate, 1.5)
    }

    func testFinishedEpisodeStartsTheNextAtItsSavedPosition() {
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk"), episode("bbbbbbbbbbb", positionS: 120)],
                                      index: 0, positionMs: 0, play: true, source: .list)))
        backend.start(); backend.finish()
        XCTAssertEqual(engine.episodes.index, 1)
        XCTAssertEqual(backend.loads.last?.1, 120_000)
    }

    func testPlayWithNothingLoadedAfterRestoreLoadsAtSavedPosition() {
        engine.handle(.setQueue(q([7, 8], index: 1, pos: 9_000, play: false)))
        XCTAssertEqual(backend.loads.last?.2, false)
        engine.handle(.play(.track))
        XCTAssertEqual(backend.calls.last, "play")
        XCTAssertTrue(engine.playing)
    }
}

// MARK: - Shuffle and repeat (the web's modes, music only)

extension PlayerEngineTests {
    func items(_ ids: ClosedRange<Int>) -> [Item] { ids.map(track) }
    var ids: [String] { engine.music.items.map(\.id) }

    func testRepeatOneLoopsOnNaturalEndAndCountsTwoPlays() async {
        engine.handle(.setQueue(SetQueue(kind: .track, items: items(1...3), index: 0, positionMs: 0, play: true, source: .list)))
        engine.handle(.setModes(shuffle: false, repeatMode: .one))
        backend.play(to: 31_000); backend.finish()
        XCTAssertEqual(engine.current?.id, "1")
        backend.play(to: 31_000); backend.finish()
        XCTAssertEqual(engine.events.pending.filter { $0.track_id == 1 }.count, 2)
    }

    func testRepeatOneUserNextStillMoves() {
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        engine.handle(.setModes(shuffle: false, repeatMode: .one))
        engine.next()
        XCTAssertEqual(engine.current?.id, "2")
        engine.prev()
        XCTAssertEqual(engine.current?.id, "1")
        backend.finish()                                       // only the natural end loops
        XCTAssertEqual(engine.current?.id, "1")
    }

    func testRepeatAllWrapsWithoutRefill() async {
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true, source: .radio)))
        backend.start(); backend.finish()
        backend.start(); backend.finish()
        XCTAssertEqual(engine.current?.id, "1")
        XCTAssertEqual(engine.music.index, 0)
        XCTAssertEqual(ids, ["1", "2"])
        XCTAssertTrue(engine.playing)
        await engine.idle()
        XCTAssertTrue(api.radioCalls.isEmpty); XCTAssertTrue(api.randomTracksCalls.isEmpty); XCTAssertTrue(api.randomFavoritesCalls.isEmpty)
        // the user's next at the last item wraps too
        engine.next()
        engine.next()
        XCTAssertEqual(engine.current?.id, "1")
        // repeat off again: the radio refills as before
        engine.handle(.setModes(shuffle: false, repeatMode: .off))
        await engine.idle()
        XCTAssertEqual(api.radioCalls.count, 1)
    }

    func testRepeatAllShuffleStartsNewShuffledPass() async {
        engine.handle(.setModes(shuffle: true, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2, 3, 4], index: 0, pos: 0, play: true, source: .favorites)))
        let first = ids
        XCTAssertEqual(first.first, "1")
        for _ in 0..<4 { backend.start(); backend.finish() }
        XCTAssertEqual(engine.music.index, 0)
        XCTAssertEqual(ids.sorted(), ["1", "2", "3", "4"])
        XCTAssertNotEqual(ids, first)                          // a new shuffled pass
        XCTAssertNotEqual(engine.current?.id, first.last)      // never the item that just ended
        await engine.idle()
        XCTAssertTrue(api.randomFavoritesCalls.isEmpty)
    }

    func testShuffleOnKeepsCurrentLoaded() {
        engine.handle(.setQueue(q([1, 2, 3, 4, 5], index: 1, pos: 0, play: true)))
        backend.start(); backend.play(to: 5_000)
        let loads = backend.loads.count, generation = backend.generation
        sent = []
        engine.handle(.setModes(shuffle: true, repeatMode: .off))
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(backend.generation, generation)
        XCTAssertEqual(engine.current?.id, "2"); XCTAssertEqual(engine.music.index, 1)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(Array(ids.prefix(2)), ["1", "2"])
        XCTAssertEqual(ids.dropFirst(2).sorted(), ["3", "4", "5"])
        XCTAssertNotEqual(ids, ["1", "2", "3", "4", "5"])
        XCTAssertEqual(engine.modes.original, ["3", "4", "5"])
        XCTAssertEqual(queues(.track).last?.items.map(\.id), ids)
        XCTAssertEqual(backend.preloads.last, .remote(api.streamURL(engine.music.items[2], quality: "high"), token: "T1"))
    }

    func testShuffleOffRestoresOrder() {
        engine.handle(.setQueue(q([1, 2, 3, 4, 5], index: 0, pos: 0, play: true)))
        engine.handle(.setModes(shuffle: true, repeatMode: .off))
        backend.start(); backend.finish()                     // one shuffled item played
        let now = engine.current?.id
        let loads = backend.loads.count
        engine.handle(.setModes(shuffle: false, repeatMode: .off))
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(engine.current?.id, now)
        XCTAssertEqual(Array(ids.dropFirst(2)), ["2", "3", "4", "5"].filter { $0 != now })
        XCTAssertNil(engine.modes.original)
    }

    func testAListPlayedWhileShuffledIsShuffledAfterTheChosenItemAJumpIsNot() {
        engine.handle(.setModes(shuffle: true, repeatMode: .off))
        engine.handle(.setQueue(q([1, 2, 3, 4, 5], index: 1, pos: 0, play: true)))
        XCTAssertEqual(Array(ids.prefix(2)), ["1", "2"])
        XCTAssertNotEqual(ids, ["1", "2", "3", "4", "5"])
        XCTAssertEqual(engine.modes.original, ["3", "4", "5"])
        let order = ids
        engine.handle(.setQueue(SetQueue(kind: .track, items: engine.music.items, index: 3, positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(ids, order)
        XCTAssertEqual(engine.music.index, 3)
    }

    func testAMoveWhileShuffledKeepsItsNewNeighbourOnShuffleOff() {
        engine.handle(.setQueue(q([1, 2, 3, 4, 5], index: 0, pos: 0, play: true)))
        engine.handle(.setModes(shuffle: true, repeatMode: .off))      // random 0: [1, 3, 4, 5, 2]
        XCTAssertEqual(ids, ["1", "3", "4", "5", "2"])
        engine.handle(.setQueue(q([1, 5, 3, 4, 2], index: 0, pos: nil, play: true)))   // the queue sheet: 5 dragged up
        engine.handle(.setModes(shuffle: false, repeatMode: .off))
        XCTAssertEqual(ids, ["1", "2", "5", "3", "4"])                 // 5 still right before 3
    }

    func testEpisodesIgnoreModes() {
        engine.handle(.setModes(shuffle: true, repeatMode: .one))
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [episode("aaaaaaaaaaa"), episode("bbbbbbbbbbb"), episode("ccccccccccc")],
                                         index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(engine.episodes.items.map(\.id), ["aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"])
        backend.start(); backend.finish()
        XCTAssertEqual(engine.current?.id, "bbbbbbbbbbb")
        let ep = states.last { $0.kind == .episode }
        XCTAssertEqual(ep?.shuffle, false); XCTAssertEqual(ep?.repeatMode, .off)
    }

    func testModesPersistAndRestore() throws {
        engine.handle(.setQueue(q([1, 2, 3, 4], index: 0, pos: 0, play: false)))
        engine.handle(.setModes(shuffle: true, repeatMode: .all))
        let order = ids
        makeEngine()
        XCTAssertEqual(engine.modes.shuffle, true); XCTAssertEqual(engine.modes.repeatMode, .all)
        XCTAssertEqual(engine.modes.original, ["2", "3", "4"])
        XCTAssertEqual(ids, order)
        // A file written before the modes existed (v0.1.0): no `modes` key, both off.
        var s = try XCTUnwrap(store.load())
        s.modes = nil
        store.save(s)
        let json = try String(contentsOf: dir.appendingPathComponent("queues-1.json"), encoding: .utf8)
        XCTAssertFalse(json.contains("\"modes\""))
        makeEngine()
        XCTAssertEqual(engine.modes, PlayModes())
        XCTAssertEqual(ids, order)
    }

    func testStateCarriesModes() {
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        XCTAssertEqual(states.last?.shuffle, false); XCTAssertEqual(states.last?.repeatMode, .off)
        engine.handle(.setModes(shuffle: true, repeatMode: .one))
        let st = states.last { $0.kind == .track }
        XCTAssertEqual(st?.shuffle, true); XCTAssertEqual(st?.repeatMode, .one)
        sent = []
        engine.emitAll()
        XCTAssertEqual(states.first { $0.kind == .track }?.repeatMode, .one)
        XCTAssertEqual(states.first { $0.kind == .episode }?.repeatMode, .off)
    }

    /// Repeat all, offline: a cached favorite stands in for a track that can't play; it plays once and leaves,
    /// and the interrupted track is tried again before the queue starts over (the web's transient substitute).
    func testRepeatAllSubstituteIsTransient() {
        cache.favorites = [trackModel(9)]
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2], index: 1, pos: 0, play: true)))
        network.isOnline = false
        backend.fail(network: true)
        XCTAssertEqual(ids, ["1", "2", "9", "2"]); XCTAssertEqual(engine.current?.id, "9")
        network.isOnline = true
        backend.start(); backend.finish()                     // the substitute ends: it leaves, 2 is retried
        XCTAssertEqual(ids, ["1", "2"]); XCTAssertEqual(engine.current?.id, "2")
        backend.start(); backend.finish()
        XCTAssertEqual(ids, ["1", "2"]); XCTAssertEqual(engine.current?.id, "1")
    }

    func testRepeatOffKeepsTheSubstitute() {
        cache.favorites = [trackModel(9)]
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setQueue(q([1, 2], index: 1, pos: 0, play: true)))
        network.isOnline = false
        backend.fail(network: true)
        network.isOnline = true
        backend.start(); backend.finish()
        XCTAssertEqual(ids, ["1", "2", "9", "2"]); XCTAssertEqual(engine.current?.id, "2")
    }

    // MARK: Repeat all never loops on what cannot play (review: a failing queue, no token, nothing cached)

    func testRepeatAllWithEveryTrackFailingStopsInsteadOfLooping() {
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        backend.fail(network: false); backend.fail(network: false); backend.fail(network: false)
        XCTAssertEqual(backend.loads.count, 3)                // each once, then the brake: no wrap onto failed items
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(states.last?.error, PlayerEngine.cannotPlay)
    }

    func testRepeatAllOneFailingTrackFallsBackToACachedFavorite() {
        cache.favorites = [trackModel(9)]
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.fail(network: false)
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("9.m4a")))   // never-stop's favorite, not item 1 again
    }

    func testRepeatAllWithNoTokenAndNothingCachedStopsWithoutRecursing() {
        api.token = nil
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2], index: 1, pos: 0, play: true)))   // the last item: the wrap is the next choice
        XCTAssertTrue(backend.loads.isEmpty)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(states.last?.error, PlayerEngine.cannotPlay)
    }

    func testRepeatAllWrapSkipsFailedAndStartsLocalWhenItMust() {
        cache.local = [3: tmp("3.m4a")]
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2, 3], index: 1, pos: 0, play: true)))
        network.isOnline = false
        backend.start(); backend.finish()                     // offline: 3 is on the phone
        XCTAssertEqual(engine.current?.id, "3")
        backend.start(); backend.finish()                     // the wrap: only 3 can play offline
        XCTAssertEqual(engine.music.index, 2)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("3.m4a")))
    }

    // MARK: Repeat one: the looped stream is cached, so it is fetched once

    func testRepeatOnePrefetchesTheCurrentStream() {
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        backend.start()
        cache.prefetched = []
        engine.handle(.setModes(shuffle: false, repeatMode: .one))
        XCTAssertEqual(cache.prefetched.last?.first, 1)
        cache.local[1] = tmp("1.m4a"); cache.prefetched = []
        backend.finish(); backend.start()
        XCTAssertFalse(cache.prefetched.contains { $0.contains(1) })   // on the phone now: not asked again
    }
}

// MARK: - Loudness gain

/// A music item whose `meta` carries the server's `gain_db`.
func gainTrack(_ id: Int, gainDB: Double?) -> Item {
    var meta: [String: JSONValue] = ["id": .int(id)]
    meta["gain_db"] = gainDB.map(JSONValue.double) ?? .null
    return Item(kind: .track, id: String(id), title: "T\(id)", artist: "A", album: "B", durationMs: 200_000, meta: .object(meta))
}

extension PlayerEngineTests {
    func testTrackLoadsWithItsGain() {
        engine.handle(.setQueue(SetQueue(kind: .track, items: [gainTrack(1, gainDB: -6), gainTrack(2, gainDB: nil)], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(try XCTUnwrap(backend.gains.last), 0.501, accuracy: 0.001)
        XCTAssertEqual(backend.preloadGains.last, 1, "the preloaded next item carries its own gain (unmeasured: 1)")
        backend.start(); backend.finish()
        XCTAssertEqual(backend.gains.last, 1)
    }

    func testThePreloadedItemCarriesItsOwnGain() {
        engine.handle(.setQueue(SetQueue(kind: .track, items: [gainTrack(1, gainDB: nil), gainTrack(2, gainDB: -12)], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(backend.gains.last, 1)
        XCTAssertEqual(try XCTUnwrap(backend.preloadGains.last), 0.251, accuracy: 0.001)
    }

    func testLoudnessOffLoadsAtUnity() {
        engine.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: false)))
        engine.handle(.setQueue(SetQueue(kind: .track, items: [gainTrack(1, gainDB: -6), gainTrack(2, gainDB: -6)], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(backend.gains.last, 1)
        XCTAssertEqual(backend.preloadGains.last, 1)
    }

    /// Switching it while a track plays changes that track's level at once, and the preloaded next one's.
    func testLoudnessSwitchAppliesToThePlayingTrack() {
        engine.handle(.setQueue(SetQueue(kind: .track, items: [gainTrack(1, gainDB: -6), gainTrack(2, gainDB: -6)], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        backend.start()
        let loads = backend.loads.count, preloads = backend.preloads.count
        engine.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: false)))
        XCTAssertEqual(backend.loads.count, loads, "not reloaded")
        XCTAssertEqual(backend.preloads.count, preloads, "the preloaded item is changed in place, not preloaded again")
        XCTAssertEqual(backend.currentGains.last, 1)
        XCTAssertEqual(backend.nextGains.last, 1)
        engine.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: true)))
        XCTAssertEqual(try XCTUnwrap(backend.currentGains.last), 0.501, accuracy: 0.001)
        XCTAssertEqual(try XCTUnwrap(backend.nextGains.last), 0.501, accuracy: 0.001)
        XCTAssertEqual(backend.preloads.count, preloads)
        // The same prefs again change nothing.
        let calls = backend.currentGains.count
        engine.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: true)))
        XCTAssertEqual(backend.currentGains.count, calls)
    }

    func testEpisodesLoadAtUnity() {
        var ep = episode("a")
        ep = Item(kind: .episode, id: ep.id, title: ep.title, artist: ep.artist, album: ep.album, durationMs: ep.durationMs,
                  meta: ep.meta.setting("gain_db", .int(-6)))
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [ep], index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(backend.gains.last, 1)
    }

    func testNetworkRetryKeepsTheGain() {
        engine.handle(.setQueue(SetQueue(kind: .track, items: [gainTrack(1, gainDB: -6)], index: 0, positionMs: 0, play: true, source: .list)))
        backend.start(); backend.play(to: 5_000)
        backend.fail(network: true)
        scheduler.advance(2)
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(try XCTUnwrap(backend.gains.last), 0.501, accuracy: 0.001)
    }
}
