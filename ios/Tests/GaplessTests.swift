import XCTest
@testable import Lark

/// Task 5 review: the preloaded next item is what the engine loads next, matched on the item id, and a
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
}
