import XCTest
@testable import Lark

final class NeverStopTests: EngineTestCase {
    func remote(_ id: Int) -> MediaSource {
        .remote(URL(string: "https://lark.test/api/v1/tracks/\(id)/stream?quality=high")!, token: "T1")
    }

    func testFailedItemIsSkipped() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        sent = []
        backend.fail(network: false)
        XCTAssertEqual(backend.loads.last?.0, remote(8))
        XCTAssertEqual(queues(.track).last?.items.map(\.id), ["7", "8"])
        XCTAssertEqual(queues(.track).last?.index, 1)
    }

    func testAFailedItemIsPassedOverForThirtyMinutes() {
        engine.handle(.setQueue(q([7, 8, 7, 9], index: 0, pos: 0, play: true)))
        backend.fail(network: false)                 // 7 failed → 8
        backend.start(); backend.finish()            // 8 ends: the second 7 is passed over
        XCTAssertEqual(backend.loads.last?.0, remote(9))
        advanceClock(31 * 60)
        engine.handle(.setQueue(q([1, 7], index: 0, pos: 0, play: true)))
        backend.start(); backend.finish()
        XCTAssertEqual(backend.loads.last?.0, remote(7))
    }

    func testThreeFailuresInARowOnlyLocalChoicesCount() {
        engine.handle(.setQueue(q([1, 2, 3, 4, 5], index: 0, pos: 0, play: true)))
        backend.fail(network: false); backend.fail(network: false); backend.fail(network: false)
        XCTAssertEqual(backend.loads.count, 3)
        XCTAssertFalse(engine.playing)
        XCTAssertNotNil(states.last?.error)
    }

    func testThreeFailuresInARowPlaysACachedOneWhenThereIsOne() {
        cache.local = [5: tmp("5.m4a")]
        engine.handle(.setQueue(q([1, 2, 3, 4, 5], index: 0, pos: 0, play: true)))
        backend.fail(network: false); backend.fail(network: false); backend.fail(network: false)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("5.m4a")))
        XCTAssertEqual(engine.music.items.map(\.id), ["1", "2", "3", "5", "4"])   // moved up, 4 stays to play later
        backend.start()
        backend.finish()                                                         // a success resets the count
        XCTAssertEqual(backend.loads.last?.0, remote(4))
    }

    func testOfflineSkipsToNextCachedItem() {
        network.isOnline = false
        let f = tmp("9.m4a"); cache.local = [9: f]
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        engine.next()
        XCTAssertEqual(backend.loads.last?.0, .file(f))
        XCTAssertEqual(engine.music.items[engine.music.index].id, "9")
    }

    func testOfflineNothingCachedInQueueFallsBackToCachedFavorites() {
        network.isOnline = false
        cache.local = [20: tmp("20.m4a"), 21: tmp("21.m4a")]
        cache.favorites = [trackModel(20), trackModel(21)]
        randomValue = 0.99
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.fail(network: true)                  // offline: no retry, straight to what is on the phone
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("21.m4a")))
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "21", "7", "8"])   // 7 is requeued, to play once online
        XCTAssertEqual(notices.last, "离线：正在播放已缓存的收藏")
        XCTAssertEqual(engine.music.items[1].meta["title"], .string("T21"))
    }

    func testCachedFavoriteInTheRecentHistoryIsPassedOver() {
        network.isOnline = false
        cache.local = [20: tmp("20.m4a"), 21: tmp("21.m4a")]
        cache.favorites = [trackModel(20), trackModel(21)]
        randomValue = 0
        engine.handle(.setQueue(q([20, 7, 8], index: 1, pos: 0, play: true)))
        engine.next()
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("21.m4a")))
    }

    func testNetworkErrorRetriesSameItemThenMovesToCached() {
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 12_000)
        backend.fail(network: true)
        XCTAssertEqual(backend.loads.count, 1)
        scheduler.advance(1.9)
        XCTAssertEqual(backend.loads.count, 1)
        scheduler.advance(0.1)
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(backend.loads.last?.0, remote(7)); XCTAssertEqual(backend.loads.last?.1, 12_000); XCTAssertEqual(backend.loads.last?.2, true)
        backend.fail(network: true); scheduler.advance(5)
        XCTAssertEqual(backend.loads.count, 3); XCTAssertEqual(backend.loads.last?.1, 12_000)
        backend.fail(network: true); scheduler.advance(10)
        XCTAssertEqual(backend.loads.count, 4)
        backend.fail(network: true)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("9.m4a")))
    }

    func testAUserActionCancelsAPendingRetry() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.fail(network: true)
        engine.next()
        let n = backend.loads.count
        scheduler.advance(30)
        XCTAssertEqual(backend.loads.count, n)
    }

    func testEndOfFavoritesQueueRefills() async {
        api.randomFavoritesAnswer = ("favorites", [trackModel(30), trackModel(8), trackModel(31)])
        engine.handle(.setQueue(q([5, 6, 7, 8], index: 0, pos: 0, play: true, source: .favorites)))
        await engine.idle()
        XCTAssertTrue(api.randomFavoritesCalls.isEmpty)       // 3 upcoming: not yet
        backend.start(); backend.finish()                     // 2 upcoming
        await engine.idle()
        XCTAssertEqual(api.randomFavoritesCalls, [.init(n: 20, exclude: [7, 8])])
        XCTAssertEqual(engine.music.items.map(\.id), ["5", "6", "7", "8", "30", "31"])   // 8 is still upcoming: not twice
        XCTAssertEqual(queues(.track).last?.items.count, 6)
        XCTAssertEqual(engine.music.items.last?.meta["title"], .string("T31"))
    }

    func testFavoritesRefillThatFellBackBecomesAShuffle() async {
        api.randomFavoritesAnswer = ("all", [trackModel(30)])
        engine.handle(.setQueue(q([5, 6], index: 0, pos: 0, play: true, source: .favorites)))
        await engine.idle()
        XCTAssertEqual(engine.music.source, .shuffle)
    }

    func testRadioAndShuffleRefill() async {
        api.radioAnswer = [trackModel(40), trackModel(5)]
        engine.handle(.setQueue(q([5, 6], index: 0, pos: 0, play: true, source: .radio)))
        await engine.idle()
        XCTAssertEqual(api.radioCalls, [.init(n: 10, exclude: [5, 6])])
        XCTAssertEqual(engine.music.items.map(\.id), ["5", "6", "40"])           // radio never repeats a queued track

        api.randomTracksAnswer = [trackModel(50)]
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true, source: .shuffle)))
        await engine.idle()
        XCTAssertEqual(api.randomTracksCalls, [.init(n: 20, exclude: [1, 2])])
        XCTAssertEqual(engine.music.items.map(\.id), ["1", "2", "50"])
    }

    func testListQueueIsNeverRefilledAndAnExhaustedRadioStopsAsking() async {
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true, source: .list)))
        await engine.idle()
        XCTAssertTrue(api.radioCalls.isEmpty && api.randomTracksCalls.isEmpty && api.randomFavoritesCalls.isEmpty)

        api.radioAnswer = []
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true, source: .radio)))
        await engine.idle()
        backend.start(); backend.finish()
        await engine.idle()
        XCTAssertEqual(api.radioCalls.count, 1)
    }

    func testAFailedRefillIsRetriedAfterThirtySeconds() async {
        api.refillError = URLError(.notConnectedToInternet)
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true, source: .radio)))
        await engine.idle()
        XCTAssertEqual(api.radioCalls.count, 1)
        api.refillError = nil; api.radioAnswer = [trackModel(3)]
        scheduler.advance(30)
        await engine.idle()
        XCTAssertEqual(api.radioCalls.count, 2)
        XCTAssertEqual(engine.music.items.map(\.id), ["1", "2", "3"])
    }

    func testShuffleFavoritesPrefersTheCacheThenTheServer() async {
        cache.local = [20: tmp("20.m4a"), 21: tmp("21.m4a")]
        cache.favorites = [trackModel(20), trackModel(21)]
        await engine.shuffleFavorites()
        XCTAssertEqual(engine.music.source, .favorites)
        XCTAssertEqual(Set(engine.music.items.map(\.id)), ["20", "21"])
        XCTAssertFalse(api.randomFavoritesCalls.contains { $0.n == 50 })   // no network for the start
        if case .file = backend.loads.last?.0 {} else { XCTFail("expected a cached file") }
        XCTAssertEqual(backend.loads.last?.2, true)

        cache.favorites = []
        api.randomFavoritesAnswer = ("favorites", [trackModel(30), trackModel(31)])
        await engine.shuffleFavorites()
        XCTAssertEqual(api.randomFavoritesCalls.first { $0.n == 50 }, .init(n: 50, exclude: []))
        XCTAssertEqual(engine.music.items.map(\.id).prefix(2), ["30", "31"])
    }

    func testResumeOrShuffleFavoritesResumesTheRestoredQueue() async {
        engine.handle(.setQueue(q([7, 8], index: 1, pos: 9_000, play: false)))
        makeEngine()
        cache.favorites = [trackModel(20)]
        await engine.resumeOrShuffleFavorites()
        XCTAssertEqual(backend.loads.last?.0, remote(8)); XCTAssertEqual(backend.loads.last?.1, 9_000); XCTAssertEqual(backend.loads.last?.2, true)
        XCTAssertTrue(engine.playing)
    }
}
