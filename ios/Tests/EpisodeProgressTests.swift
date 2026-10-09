import XCTest
@testable import Lark

final class EpisodeProgressTests: EngineTestCase {
    func playEpisodes(_ ids: [String] = ["abcdefghijk", "bbbbbbbbbbb"], pos: Int = 0) {
        engine.handle(.setQueue(.init(kind: .episode, items: ids.map { episode($0) }, index: 0, positionMs: pos, play: true, source: .list)))
        backend.start()
    }

    func testProgressEveryFifteenSecondsWhilePlaying() async {
        playEpisodes()
        advanceClock(10); backend.play(to: 10_000); await engine.idle()
        XCTAssertTrue(api.progressCalls.isEmpty)
        backend.play(to: 14_500); advanceClock(5); backend.play(to: 15_000); await engine.idle()
        XCTAssertEqual(api.progressCalls, [.init(id: "abcdefghijk", positionS: 15, played: false)])
        advanceClock(15); engine.tick(); await engine.idle()
        XCTAssertEqual(api.progressCalls.count, 2)
    }

    func testProgressOnPause() async {
        playEpisodes()
        backend.play(to: 7_600)
        engine.handle(.pause(.episode))
        await engine.idle()
        XCTAssertEqual(api.progressCalls.last, .init(id: "abcdefghijk", positionS: 7, played: false))
    }

    func testTheEndMarksItPlayedThenPlaysTheNext() async {
        playEpisodes()
        backend.play(to: 20_000)
        backend.finish()
        await engine.idle()
        XCTAssertEqual(api.progressCalls.last, .init(id: "abcdefghijk", positionS: 0, played: true))
        XCTAssertEqual(engine.episodes.index, 1)
        XCTAssertEqual(backend.loads.last?.0, .remote(URL(string: "https://lark.test/api/v1/episodes/bbbbbbbbbbb/stream?kind=audio")!, token: "T1"))
        XCTAssertEqual(backend.loads.last?.2, true)
    }

    func testTheLastEpisodeEndingStops() async {
        playEpisodes(["abcdefghijk"])
        backend.finish()
        await engine.idle()
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(api.progressCalls.last?.played, true)
        XCTAssertEqual(engine.episodes.positionMs, 0)
    }

    func testEpisodesCreateNoPlayEvents() async {
        playEpisodes()
        backend.play(to: 60_000)
        engine.next()
        backend.start(); backend.play(to: 60_000); backend.finish()
        engine.handle(.flushEvents(id: "x"))
        await engine.idle()
        XCTAssertTrue(api.postedEvents.isEmpty)
        XCTAssertTrue(events.pending.isEmpty)
    }

    func testSkippingToTheNextEpisodeSavesTheOutgoingPosition() async {
        playEpisodes()
        backend.play(to: 33_000)
        engine.next()
        await engine.idle()
        XCTAssertEqual(api.progressCalls.first, .init(id: "abcdefghijk", positionS: 33, played: false))
        XCTAssertEqual(engine.episodes.index, 1)
    }

    func testANetworkErrorRetriesTheEpisodeAtItsPosition() {
        playEpisodes(pos: 60_000)
        backend.play(to: 61_000)
        backend.fail(network: true)
        scheduler.advance(2)
        XCTAssertEqual(backend.loads.count, 2); XCTAssertEqual(backend.loads.last?.1, 61_000)
    }
}
