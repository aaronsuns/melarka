import XCTest
@testable import Lark

/// The preloaded next item is what the engine loads next, matched on the item id, and a
/// network retry preloads again.
final class GaplessTests: EngineTestCase {
    func remote(_ id: Int) -> MediaSource {
        .remote(URL(string: "https://lark.test/api/v1/tracks/\(id)/stream?quality=high")!, token: "T1")
    }

    /// The next track was preloaded as a stream and meanwhile reached the cache: the end of this track still
    /// loads the preloaded stream, so the backend takes that item over (gapless) instead of starting the file.
    func testTheEndLoadsWhatWasPreloadedEvenOnceCached() {
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        backend.start()
        XCTAssertEqual(backend.preloads.last, remote(2))
        cache.local[2] = tmp("2.m4a")                 // the lookahead finished the download
        backend.finish()
        XCTAssertEqual(backend.loads.last?.0, remote(2))
        XCTAssertEqual(engine.current?.id, "2")
    }

    /// The user's next goes to the preloaded item too.
    func testNextLoadsThePreloadedSource() {
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        backend.start()
        cache.local[2] = tmp("2.m4a")
        engine.next()
        XCTAssertEqual(backend.loads.last?.0, remote(2))
    }

    /// Anything else than the preloaded item loads from its own best source: the file once cached.
    func testAnotherItemUsesTheCache() {
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        backend.start()
        cache.local[3] = tmp("3.m4a")
        engine.handle(.setQueue(q([1, 2, 3], index: 2, pos: 0, play: true)))
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("3.m4a")))
        // A later load of 2 (not the one preloaded after 3) is the file too.
        cache.local[2] = tmp("2.m4a")
        engine.handle(.setQueue(q([1, 2, 3], index: 1, pos: 0, play: true)))
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("2.m4a")))
    }

    func testNetworkRetryPreloadsAgain() {
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 5_000)
        let preloads = backend.preloads.count
        backend.fail(network: true)
        scheduler.advance(2)
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(backend.preloads.count, preloads + 1)
        XCTAssertEqual(backend.preloads.last, remote(2))
    }

    /// Repeat one: the current item's own source is preloaded, and its natural end loads exactly that source,
    /// so the backend takes the preloaded copy over (a gapless loop, nothing fresh fetched).
    func testRepeatOnePreloadsTheCurrentItemAndTheLoopAdoptsIt() {
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        backend.start()
        XCTAssertEqual(backend.preloads.last, remote(2))
        engine.handle(.setModes(shuffle: false, repeatMode: .one))
        XCTAssertEqual(backend.preloads.last, remote(1))
        cache.local[1] = tmp("1.m4a")                 // reached the cache meanwhile: the preloaded stream still wins
        let loads = backend.loads.count
        backend.finish()
        XCTAssertEqual(backend.loads.count, loads + 1)
        XCTAssertEqual(backend.loads.last?.0, remote(1))
        XCTAssertEqual(backend.loads.last?.1, 0)
        XCTAssertEqual(engine.current?.id, "1")
        XCTAssertEqual(backend.preloads.last, .file(tmp("1.m4a")))   // and the next loop is ready, from the phone now
    }

    /// Repeat all without shuffle: at the last item the first one is preloaded, so the wrap is gapless too.
    func testRepeatAllPreloadsTheFirstItemAtTheEnd() {
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2], index: 1, pos: 0, play: true)))
        XCTAssertEqual(backend.preloads.last, remote(1))
        backend.start(); backend.finish()
        XCTAssertEqual(backend.loads.last?.0, remote(1))
        XCTAssertEqual(engine.music.index, 0)
    }

    /// Modes off: what is preloaded is exactly as before (nothing after the last item).
    func testModesOffPreloadNothingAfterTheLastItem() {
        engine.handle(.setQueue(q([1, 2], index: 1, pos: 0, play: true)))
        XCTAssertEqual(backend.preloads.last, .some(nil))
    }
}

extension GaplessTests {
    /// Two tracks at different levels: the preloaded item got its own gain when it was preloaded, so the
    /// change at the end is the backend's alone. The engine sets no volume for it.
    func testThePreloadedItemHasItsOwnGain() {
        engine.handle(.setQueue(SetQueue(kind: .track, items: [gainTrack(1, gainDB: -2), gainTrack(2, gainDB: -10)], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        backend.start()
        XCTAssertEqual(backend.preloads.last, remote(2))
        XCTAssertEqual(try XCTUnwrap(backend.preloadGains.last), 0.316, accuracy: 0.001)
        backend.finish()
        XCTAssertEqual(backend.loads.last?.0, remote(2))
        XCTAssertEqual(try XCTUnwrap(backend.gains.last), 0.316, accuracy: 0.001)    // the same gain: the backend adopts the item as it is
        XCTAssertTrue(backend.volumes.isEmpty, "the master volume is the sleep timer's only")
        XCTAssertTrue(backend.currentGains.isEmpty)
    }
}
