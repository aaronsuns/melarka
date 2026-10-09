import XCTest
@testable import Lark

/// Task 6: the engine hands the next 2 tracks to the cache once the current one sounds, and touches
/// what it plays from the cache.
final class LookaheadTests: EngineTestCase {
    func testNextTwoArePrefetchedWhenTheTrackStartsSounding() {
        engine.handle(.setQueue(q([1, 2, 3, 4], index: 0, pos: 0, play: true)))
        XCTAssertEqual(cache.prefetched, [])            // still buffering: the current stream goes first
        backend.start()
        XCTAssertEqual(cache.prefetched, [[2, 3]])
        backend.finish()                                 // 2 loads; once it sounds, 3 and 4
        XCTAssertEqual(cache.prefetched, [[2, 3]])
        backend.start()
        XCTAssertEqual(cache.prefetched, [[2, 3], [3, 4]])
    }

    func testRestoredQueueThatIsNotPlayingPrefetchesNothing() {
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: false)))
        XCTAssertEqual(cache.prefetched, [])
    }

    func testLastTrackPrefetchesNothing() {
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.start()
        XCTAssertEqual(cache.prefetched, [])
    }

    func testEditWhileSoundingPrefetchesTheNewNext() {
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        backend.start()
        engine.handle(.setQueue(q([1, 7, 8, 2, 3], index: 0, pos: nil, play: true)))   // enqueue next
        XCTAssertEqual(cache.prefetched.last, [7, 8])
    }

    func testEpisodesArePrefetchedNever() {
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [episode("aaaaaaaaaaa"), episode("bbbbbbbbbbb")], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        backend.start()
        XCTAssertEqual(cache.prefetched, [])
    }

    func testPlayFromTheCacheTouchesIt() {
        cache.local[1] = tmp("1.m4a")
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("1.m4a")))
        XCTAssertEqual(cache.touched, [1])
        backend.finish()                                 // 2 streams: nothing to touch
        XCTAssertEqual(cache.touched, [1])
    }

    /// The engine is one observer among several: the cache wiring adds its own without replacing it.
    func testEngineObservesTheNetworkWithoutTakingItOver() {
        var other = 0
        network.observe { other += 1 }
        network.isOnline = false
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.fail(network: true)                      // offline, nothing on the phone: stopped for the network
        let loads = backend.loads.count
        network.isOnline = true                          // both observers run
        XCTAssertEqual(other, 2)
        XCTAssertEqual(backend.loads.count, loads + 1)   // the engine started again
        XCTAssertTrue(engine.playing)
    }
}
