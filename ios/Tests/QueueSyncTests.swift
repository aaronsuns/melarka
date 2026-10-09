import XCTest
@testable import Lark

final class QueueSyncTests: EngineTestCase {
    func serverQueue(_ ids: [Int], index: Int, pos: Int, by: String, at: Int) -> (ServerQueue, [(Track, JSONValue)]) {
        (ServerQueue(track_ids: ids, current_index: index, position_ms: pos, version: 3, updated_by: by, updated_at: at), ids.map { trackModel($0) })
    }
    var nowS: Int { Int(clock.timeIntervalSince1970) }

    func testPutIsDebouncedOneSecondAfterEdits() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        engine.handle(.setQueue(q([7, 9, 8], index: 0, pos: nil, play: false)))
        scheduler.advance(0.9); await engine.idle()
        XCTAssertTrue(api.saveCalls.isEmpty)
        engine.handle(.setQueue(q([7, 9, 8, 10], index: 0, pos: nil, play: false)))
        scheduler.advance(0.9); await engine.idle()
        XCTAssertTrue(api.saveCalls.isEmpty)
        scheduler.advance(0.1); await engine.idle()
        XCTAssertEqual(api.saveCalls, [.init(trackIDs: [7, 9, 8, 10], index: 0, positionMs: 0)])
    }

    func testPutEveryFifteenSecondsWhilePlaying() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.start()
        scheduler.advance(1); await engine.idle()
        XCTAssertEqual(api.saveCalls.count, 1)
        advanceClock(10); backend.play(to: 10_000); await engine.idle()
        XCTAssertEqual(api.saveCalls.count, 1)
        backend.play(to: 14_500); advanceClock(5); backend.play(to: 15_000); await engine.idle()
        XCTAssertEqual(api.saveCalls.count, 2)
        XCTAssertEqual(api.saveCalls.last, .init(trackIDs: [7, 8], index: 0, positionMs: 15_000))
        engine.pause()
        advanceClock(60); engine.tick(); await engine.idle()
        scheduler.advance(1); await engine.idle()
        XCTAssertEqual(api.saveCalls.count, 3)                 // the pause, then nothing while paused
        advanceClock(60); engine.tick(); scheduler.advance(1); await engine.idle()
        XCTAssertEqual(api.saveCalls.count, 3)
    }

    func testEpisodesAreNeverSent() async {
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        backend.start()
        advanceClock(30); backend.play(to: 30_000); engine.tick()
        scheduler.advance(5); await engine.idle()
        XCTAssertTrue(api.saveCalls.isEmpty)
    }

    func testColdStartWithAnEmptyLocalQueueAdoptsTheServers() async {
        api.queueAnswer = serverQueue([4, 5, 6], index: 1, pos: 7_000, by: "device:2", at: nowS - 3600)
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["4", "5", "6"]); XCTAssertEqual(engine.music.index, 1)
        XCTAssertEqual(engine.music.positionMs, 7_000); XCTAssertEqual(engine.music.source, .restored)
        XCTAssertEqual(engine.music.items[1].meta["title"], .string("T5"))
        XCTAssertFalse(engine.playing); XCTAssertTrue(backend.loads.isEmpty)
        XCTAssertEqual(queues(.track).last?.items.count, 3)
        scheduler.advance(2); await engine.idle()
        XCTAssertTrue(api.saveCalls.isEmpty)                   // not echoed back
    }

    func testServerTracksMissingFromTheAnswerAreDropped() async {
        api.queueAnswer = (ServerQueue(track_ids: [4, 5, 6], current_index: 2, position_ms: 0, version: 1, updated_by: "device:2", updated_at: 1),
                           [trackModel(4), trackModel(6)])
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["4", "6"]); XCTAssertEqual(engine.music.index, 1)
    }

    func testANewerQueueFromAnotherDeviceIsAdoptedWhenNothingPlays() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        scheduler.advance(1); await engine.idle()             // our PUT: the server says we are device:1
        makeEngine()
        api.queueAnswer = serverQueue([4], index: 0, pos: 0, by: "device:2", at: nowS + 6)
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["4"])
    }

    func testTheServerQueueIsNotAdoptedWhenNotNewerEnough() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        makeEngine()
        api.queueAnswer = serverQueue([4], index: 0, pos: 0, by: "device:2", at: nowS + 5)
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "8"])
    }

    func testOurOwnQueueOnTheServerIsNotAdopted() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        scheduler.advance(1); await engine.idle()
        makeEngine()
        api.queueAnswer = serverQueue([4], index: 0, pos: 0, by: "device:1", at: nowS + 60)
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "8"])
    }

    func testWhilePlayingTheServerQueueIsNeverAdopted() async {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.start()
        api.queueAnswer = serverQueue([4], index: 0, pos: 0, by: "device:2", at: nowS + 600)
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "8"])
    }

    func testOnlyTheFirstHelloAfterLaunchRunsOnOpen() async {
        engine.handle(.hello(onOpen: "nothing"))
        await engine.idle()
        api.queueAnswer = serverQueue([4], index: 0, pos: 0, by: "device:2", at: 1)
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(api.queueCalls, 0)
    }

    func testOnOpenShuffleFavoritesPlaysOnlyWhenNothingPlays() async {
        cache.local = [20: tmp("20.m4a")]; cache.favorites = [trackModel(20)]
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start()
        engine.handle(.hello(onOpen: "shuffle_favorites"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.map(\.id), ["7"])

        makeEngine()
        engine.handle(.hello(onOpen: "shuffle_favorites"))
        await engine.idle()
        XCTAssertEqual(engine.music.items.first?.id, "20")
        XCTAssertEqual(engine.music.source, .favorites)
    }
}
