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
