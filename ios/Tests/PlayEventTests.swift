import XCTest
@testable import Lark

final class PlayEventTests: EngineTestCase {
    var eventsFile: URL { dir.appendingPathComponent("events-1.json") }

    func play(_ id: Int, seconds: Int) {
        engine.handle(.setQueue(q([id, 99], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: seconds * 1000)
    }

    func testFortySecondsThenNextIsNotASkip() async {
        play(7, seconds: 40)
        engine.next()
        await engine.idle()
        let e = api.postedEvents.last?.first
        XCTAssertEqual(e?.track_id, 7); XCTAssertEqual(e?.played_seconds, 40); XCTAssertEqual(e?.skipped, false)
        XCTAssertEqual(e?.quality, "high"); XCTAssertEqual(e?.started_at, Int(clock.timeIntervalSince1970))
        XCTAssertEqual(e?.client_event_id.count, 36)
    }

    func testTenSecondsThenNextIsASkip() async {
        play(7, seconds: 10)
        engine.next()
        await engine.idle()
        XCTAssertEqual(api.postedEvents.last?.first?.skipped, true)
        XCTAssertEqual(api.postedEvents.last?.first?.played_seconds, 10)
    }

    func testANaturalEndIsNotASkip() async {
        play(7, seconds: 10)
        backend.finish()
        await engine.idle()
        XCTAssertEqual(api.postedEvents.last?.first?.skipped, false)
    }

    func testSeekingDoesNotCountAsListening() async {
        play(7, seconds: 5)
        engine.seek(ms: 100_000)
        backend.play(to: 105_000)
        engine.next()
        await engine.idle()
        XCTAssertEqual(api.postedEvents.last?.first?.played_seconds, 10)
    }

    func testUnderOneSecondNoEvent() async {
        play(7, seconds: 0)
        backend.play(to: 500)
        engine.next()
        await engine.idle()
        XCTAssertTrue(api.postedEvents.isEmpty)
    }

    func testEventsPersistAndSurviveAFailedPost() async throws {
        api.postEventsError = URLError(.notConnectedToInternet)
        play(7, seconds: 40)
        engine.next()
        await engine.idle()
        XCTAssertEqual(api.postedEvents.count, 1)
        XCTAssertTrue(FileManager.default.fileExists(atPath: eventsFile.path))
        let id = try XCTUnwrap(events.pending.first?.client_event_id)

        makeEngine()                                         // a relaunch: still pending
        XCTAssertEqual(events.pending.map(\.client_event_id), [id])
        api.postEventsError = nil
        await events.flush()
        XCTAssertEqual(api.postedEvents.last?.map(\.client_event_id), [id])  // the same id: the server dedupes
        XCTAssertTrue(events.pending.isEmpty)
    }

    func testTickFlushesPendingEventsEveryThirtySeconds() async {
        api.postEventsError = URLError(.timedOut)
        play(7, seconds: 40)
        engine.next()
        await engine.idle()
        api.postEventsError = nil
        advanceClock(20); engine.tick(); await engine.idle()
        XCTAssertEqual(api.postedEvents.count, 1)
        advanceClock(10); engine.tick(); await engine.idle()
        XCTAssertEqual(api.postedEvents.count, 2)
        XCTAssertTrue(events.pending.isEmpty)
    }

    func testFlushEventsPostsThenRepliesFlushedEvenWhenThePostFails() async {
        api.postEventsError = URLError(.timedOut)
        play(7, seconds: 40)
        engine.handle(.flushEvents(id: "f1"))
        await engine.idle()
        XCTAssertEqual(api.postedEvents.last?.first?.played_seconds, 40)   // the listen in progress is recorded
        XCTAssertTrue(sent.contains(.flushed(id: "f1")))
        XCTAssertEqual(events.pending.count, 1)

        backend.play(to: 70_000)                                          // the web's finishListen("switch"): no restart,
        api.postEventsError = nil                                         // so one play is never counted twice
        engine.next()
        await engine.idle()
        XCTAssertEqual(api.postedEvents.last?.map(\.played_seconds), [40])
    }

    func testFewerAcceptedThanSentIsNotAnError() async {
        api.accepted = 0                                    // the server skipped it (an unknown track)
        let q = PlayEventQueue(api: api, store: store, now: { Date() })
        q.add(trackId: 1, startedAt: 1, playedSeconds: 5, skipped: true, quality: "high")
        await q.flush()
        XCTAssertTrue(q.pending.isEmpty)
    }
}

