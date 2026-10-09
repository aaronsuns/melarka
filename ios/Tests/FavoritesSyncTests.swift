import XCTest
@testable import Lark

final class FavoritesSyncTests: CacheTestCase {
    var sync: FavoritesSync!

    override func setUp() async throws {
        try await super.setUp()
        sync = FavoritesSync(api: api, cache: cache, network: network)
    }

    func testSyncFlagsAndDownloadsMissingOnWiFi() async throws {
        api.favoritesAnswer = [trackModel(1), trackModel(2), trackModel(3)]
        try put(1, bytes: 100, fav: false, at: t0)                       // api favorites [1,2,3], cache has 1 → downloads 2,3
        await sync.run()
        XCTAssertEqual(api.downloadCalls.sorted(), [2, 3])
        XCTAssertEqual(api.downloadQualities, ["high", "high"])
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [1, 2, 3])   // flags {1,2,3}
        XCTAssertFalse(cache.isDownloading)                              // run() returns once they are stored
        await sync.run()                                                  // nothing missing: no download
        XCTAssertEqual(api.downloadCalls.count, 2)
    }

    func testUnfavoritedKeepsFileButLosesFlag() async throws {
        api.favoritesAnswer = [trackModel(1), trackModel(2)]
        await sync.run()
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [1, 2])
        api.favoritesAnswer = [trackModel(2)]
        await sync.run()
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [2])
        XCTAssertNotNil(cache.localURL(trackID: 1))                       // an ordinary LRU entry now
        XCTAssertEqual(api.downloadCalls.sorted(), [1, 2])
        // ... and so it goes before any favorite.
        clock = t0 + 100
        cache.touch(trackID: 1)
        cache.capBytes = 100
        XCTAssertNil(cache.localURL(trackID: 1)); XCTAssertNotNil(cache.localURL(trackID: 2))
    }

    func testSyncSkippedOffOrOnExpensiveNetwork() async throws {
        api.favoritesAnswer = [trackModel(1)]
        network.isExpensive = true
        await sync.run()
        network.isExpensive = false; network.isOnline = false
        await sync.run()
        XCTAssertEqual(api.favoritesCalls, 0)
        XCTAssertEqual(api.downloadCalls, [])
        api.token = nil; network.isOnline = true
        await sync.run()
        XCTAssertEqual(api.favoritesCalls, 0)
    }

    func testSyncStopsDownloadingWhenTheNetworkTurnsExpensive() async throws {
        api.favoritesAnswer = [trackModel(1), trackModel(2), trackModel(3), trackModel(4)]
        api.holdDownloads = true
        let run = Task { await sync.run() }
        try await waitUntil { self.api.heldCount == 2 }
        network.isExpensive = true                                         // onto cellular mid-sync
        api.releaseDownloads()
        await run.value
        XCTAssertEqual(api.downloadCalls.sorted(), [1, 2])                 // the two already running finish; no more start
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [1, 2])
    }

    func testCachedFavoritesReturnsMetaForQueueItems() async throws {
        // cachedFavorites() pairs feed NextChooser and the intents: the API's raw JSON, as the web would get it.
        let raw: JSONValue = .object(["id": .int(4), "title": .string("T4"), "artist": .string("A"), "album": .string("B"),
                                      "duration_ms": .int(1000), "favorite": .bool(true), "codec": .string("flac")])
        let t = Track(id: 4, title: "T4", artist: "A", album: "B", duration_ms: 1000, favorite: true)
        api.favoritesAnswer = [(t, raw)]
        await sync.run()
        network.isOnline = false
        let favs = cache.cachedFavorites()
        XCTAssertEqual(favs.count, 1)
        XCTAssertEqual(favs[0].0, t); XCTAssertEqual(favs[0].1, raw)
        let item = Item(track: favs[0].0, json: favs[0].1)
        XCTAssertEqual(item.id, "4"); XCTAssertEqual(item.meta, raw)
        XCTAssertEqual(cache.localURL(trackID: 4)?.lastPathComponent, "4.m4a")
    }

    func testSyncRefreshesTheMetaOfCachedFavorites() async throws {
        try put(1, bytes: 100, fav: true, at: t0)
        let newer = Track(id: 1, title: "Renamed", artist: "A", album: "B", duration_ms: 1000, favorite: true)
        api.favoritesAnswer = [(newer, .object(["id": .int(1), "title": .string("Renamed")]))]
        await sync.run()
        XCTAssertEqual(cache.cachedFavorites().first?.0.title, "Renamed")
        XCTAssertEqual(cache.cachedFavorites().first?.1["title"], .string("Renamed"))
        XCTAssertEqual(api.downloadCalls, [])
    }

    func testFailedListChangesNothing() async throws {
        try put(1, bytes: 100, fav: true, at: t0)
        api.favoritesError = LarkError.http(status: 502, code: nil)
        await sync.run()
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [1])           // not taken as "no favorites"
    }

    /// With more favorites than the cap holds, a sync stops instead of evicting favorites it just fetched.
    func testSyncStopsWhenFavoritesFillTheCap() async throws {
        // 200 s tracks are estimated at 6.4 MB (AAC 256k) before they are fetched; these are that size.
        cache.capBytes = 15_000_000
        api.favoritesAnswer = (1...6).map { trackModel($0, durationMs: 200_000) }
        for id in 1...6 { api.downloadBytes[id] = 6_400_000 }
        await sync.run()
        XCTAssertEqual(api.downloadCalls.count, 2)                          // a third would not fit beside them
        XCTAssertEqual(cache.cachedFavorites().count, 2)
        XCTAssertLessThanOrEqual(cache.usedBytes, 15_000_000)
        await sync.run()                                                     // and the next sync does not churn
        XCTAssertEqual(api.downloadCalls.count, 2)
    }

    func testCancelledSyncStartsNoMoreDownloads() async throws {
        api.favoritesAnswer = (1...5).map { trackModel($0) }
        api.holdDownloads = true
        let run = Task { await sync.run() }
        try await waitUntil { self.api.heldCount == 2 }
        run.cancel()
        api.releaseDownloads()
        await run.value
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(api.downloadCalls.count, 2)
    }

    // MARK: Free space, cancelling and Low Data Mode

    func testSyncKeepsTheFreeSpaceReserve() async throws {
        // 510 MB free before anything is cached: one 6.4 MB favorite fits above the 500 MB reserve, not two.
        free = { [unowned self] in (510 << 20) - self.cache.usedBytes }
        api.favoritesAnswer = (1...4).map { trackModel($0, durationMs: 200_000) }
        for id in 1...4 { api.downloadBytes[id] = 6_400_000 }
        await sync.run()
        XCTAssertEqual(api.downloadCalls, [1])
    }

    func testSyncStopsOnTheFirstOutOfSpace() async throws {
        api.favoritesAnswer = (1...6).map { trackModel($0) }
        api.downloadErrors[1] = CocoaError(.fileWriteOutOfSpace)
        await sync.run()
        XCTAssertLessThanOrEqual(api.downloadCalls.count, 2)           // 2 may already hold the other slot
        XCTAssertFalse(api.downloadCalls.contains(3))
        // The next sync tries again (space may have been freed), and stops again if it has not.
        api.downloadErrors = [:]
        await sync.run()
        XCTAssertEqual(cache.cachedFavorites().count, 6)
    }

    /// Favorites fill to about 90% of the cap: the lookahead and estimate errors fit without evicting a favorite.
    func testSyncLeavesHeadroomUnderTheCap() async throws {
        cache.capBytes = 64_000_000                                    // ten 6.4 MB favorites would fill it exactly
        api.favoritesAnswer = (1...10).map { trackModel($0, durationMs: 200_000) }
        for id in 1...10 { api.downloadBytes[id] = 6_400_000 }
        await sync.run()
        XCTAssertEqual(api.downloadCalls.count, 9)
        XCTAssertLessThanOrEqual(cache.usedBytes, 57_600_000)
    }

    /// Cancelling the sync (background expiry) leaves a lookahead download of the same track alone.
    func testCancellingTheSyncSparesTheLookahead() async throws {
        api.holdDownloads = true
        cache.prefetch([track(3), track(4)])                           // both slots held
        try await waitUntil { self.api.heldCount == 2 }
        cache.prefetch([track(1)])                                     // the lookahead's 1 waits for a slot
        api.favoritesAnswer = [trackModel(1), trackModel(2)]
        let run = Task { await sync.run() }
        try await waitUntil { self.api.favoritesCalls == 1 }
        try await Task.sleep(nanoseconds: 50_000_000)
        run.cancel()
        api.releaseDownloads()
        await run.value
        try await settle(downloads: 3)
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(api.downloadCalls.sorted(), [1, 3, 4])          // 1 (the lookahead's) still came; 2 (the sync's) did not
    }
}

/// Low Data Mode counts as expensive.
final class NetworkStatusTests: XCTestCase {
    func testLowDataModeIsExpensive() {
        XCTAssertEqual(PathNetworkStatus.classify(satisfied: true, expensive: false, constrained: false).expensive, false)
        XCTAssertEqual(PathNetworkStatus.classify(satisfied: true, expensive: false, constrained: true).expensive, true)
        XCTAssertEqual(PathNetworkStatus.classify(satisfied: true, expensive: true, constrained: false).expensive, true)
        XCTAssertEqual(PathNetworkStatus.classify(satisfied: false, expensive: false, constrained: false).online, false)
    }
}
