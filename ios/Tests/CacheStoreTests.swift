import XCTest
@testable import Lark

@MainActor class CacheTestCase: XCTestCase {
    var root: URL!
    var scratch: URL!
    var api: FakeAPI!
    var network: FakeNetwork!
    var cache: CacheStore!
    var clock = Date(timeIntervalSince1970: 1_800_000_000)
    var t0: Date { Date(timeIntervalSince1970: 1_800_000_000) }

    override func setUp() async throws {
        let base = FileManager.default.temporaryDirectory.appendingPathComponent("cache-\(UUID().uuidString)")
        root = base.appendingPathComponent("lark/5")
        scratch = base.appendingPathComponent("scratch")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
        api = FakeAPI(); network = FakeNetwork()
        cache = makeCache()
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: root.deletingLastPathComponent().deletingLastPathComponent())
    }

    /// Free space the cache sees (plenty, unless a test says otherwise).
    var free: () -> Int64? = { 100 << 30 }

    /// A cache on `root`, as after a process launch (the previous one flushed its index, as on backgrounding).
    func makeCache(indexDelay: TimeInterval = CacheStore.indexWriteDelay) -> CacheStore {
        if let old = cache { old.flush() }
        let c = CacheStore(root: root, api: api, network: network, now: { [unowned self] in self.clock }, indexDelay: indexDelay)
        c.freeBytes = { [unowned self] in self.free() }
        return c
    }

    func put(_ id: Int, bytes: Int, fav: Bool, at t: Date) throws {
        clock = t
        let f = scratch.appendingPathComponent("\(id).m4a")
        try Data(repeating: 1, count: bytes).write(to: f)
        let (track, json) = trackModel(id)
        try cache.store(trackID: id, file: f, track: track, meta: json, favorite: fav)
    }

    /// Waits until every download the fake API started has finished and been stored.
    func settle(downloads n: Int) async throws {
        try await waitUntil { self.api.finishedDownloads >= n && !self.cache.isDownloading }
    }

    func indexJSON() throws -> [String: Any] {
        cache.flush()
        let d = try Data(contentsOf: root.appendingPathComponent("index.json"))
        return try XCTUnwrap(JSONSerialization.jsonObject(with: d) as? [String: Any])
    }
}

final class CacheStoreTests: CacheTestCase {
    func testEvictsOldestNonFavoriteFirstThenOldestFavorite() throws {
        cache.capBytes = 300
        try put(1, bytes: 100, fav: true, at: t0); try put(2, bytes: 100, fav: false, at: t0 + 1); try put(3, bytes: 100, fav: false, at: t0 + 2)
        try put(4, bytes: 100, fav: false, at: t0 + 3)       // over cap → 2 goes (oldest non-favorite), 1 stays
        XCTAssertNil(cache.localURL(trackID: 2)); XCTAssertNotNil(cache.localURL(trackID: 1))
        cache.capBytes = 100                                  // shrink → 3, 4, then favorite 1 (only favorites left over cap)
        XCTAssertEqual(cache.usedBytes, 100)
        XCTAssertNotNil(cache.localURL(trackID: 1))
        XCTAssertNil(cache.localURL(trackID: 3)); XCTAssertNil(cache.localURL(trackID: 4))
        cache.capBytes = 50                                   // only a favorite is left: it goes too
        XCTAssertEqual(cache.usedBytes, 0)
        XCTAssertNil(cache.localURL(trackID: 1))
    }

    func testOldestFavoriteGoesWhenOnlyFavoritesAreLeft() throws {
        cache.capBytes = 200
        try put(1, bytes: 100, fav: true, at: t0); try put(2, bytes: 100, fav: true, at: t0 + 1)
        try put(3, bytes: 100, fav: true, at: t0 + 2)
        XCTAssertNil(cache.localURL(trackID: 1))
        XCTAssertNotNil(cache.localURL(trackID: 2)); XCTAssertNotNil(cache.localURL(trackID: 3))
        XCTAssertEqual(cache.usedBytes, 200)
    }

    func testEvictedFilesAreDeletedFromDisk() throws {
        cache.capBytes = 100
        try put(1, bytes: 100, fav: false, at: t0)
        let first = try XCTUnwrap(cache.localURL(trackID: 1))
        try put(2, bytes: 100, fav: false, at: t0 + 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: first.path))
    }

    func testTouchRefreshesRecency() throws {
        cache.capBytes = 200
        try put(1, bytes: 100, fav: false, at: t0); try put(2, bytes: 100, fav: false, at: t0 + 1)
        clock = t0 + 5
        cache.touch(trackID: 1)
        try put(3, bytes: 100, fav: false, at: t0 + 6)        // 2 is now the least recently played
        XCTAssertNotNil(cache.localURL(trackID: 1)); XCTAssertNil(cache.localURL(trackID: 2))
        // The new recency is on disk too.
        let entry = try XCTUnwrap(try indexJSON()["1"] as? [String: Any])
        XCTAssertEqual(entry["lastPlayed"] as? Double, (t0 + 5).timeIntervalSince1970)
    }

    func testIndexSurvivesRestartAndDropsMissingFiles() throws {
        try put(1, bytes: 100, fav: true, at: t0); try put(2, bytes: 150, fav: false, at: t0 + 1)
        let index = try indexJSON()
        XCTAssertEqual(Set(index.keys), ["1", "2"])
        let e1 = try XCTUnwrap(index["1"] as? [String: Any])
        XCTAssertEqual(Set(e1.keys), ["file", "bytes", "lastPlayed", "favorite", "track", "meta"])
        XCTAssertEqual(e1["file"] as? String, "files/1.m4a")       // relative: the container path changes on updates
        XCTAssertEqual(e1["bytes"] as? Int, 100); XCTAssertEqual(e1["favorite"] as? Bool, true)

        cache = makeCache()                                          // a relaunch
        XCTAssertEqual(cache.usedBytes, 250)
        XCTAssertNotNil(cache.localURL(trackID: 1)); XCTAssertNotNil(cache.localURL(trackID: 2))
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [1])

        try FileManager.default.removeItem(at: try XCTUnwrap(cache.localURL(trackID: 2)))
        cache = makeCache()
        XCTAssertNil(cache.localURL(trackID: 2))
        XCTAssertEqual(cache.usedBytes, 100)
        XCTAssertEqual(Set(try indexJSON().keys), ["1"])             // the dropped entry is gone from disk too
    }

    func testFileDeletedWhileRunningIsDropped() throws {
        try put(1, bytes: 100, fav: true, at: t0); try put(2, bytes: 100, fav: true, at: t0 + 1)
        try FileManager.default.removeItem(at: try XCTUnwrap(cache.localURL(trackID: 1)))
        XCTAssertNil(cache.localURL(trackID: 1))                     // not an instant playback failure
        XCTAssertEqual(cache.usedBytes, 100)
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [2])
        XCTAssertEqual(Set(try indexJSON().keys), ["2"])
    }

    /// iOS may empty `Library/Caches` at any time, the index included.
    func testPurgedDirectoryEmptiesTheCacheAndItFillsAgain() throws {
        try put(1, bytes: 100, fav: true, at: t0)
        try FileManager.default.removeItem(at: root)
        XCTAssertNil(cache.localURL(trackID: 1))
        XCTAssertTrue(cache.cachedFavorites().isEmpty)
        XCTAssertEqual(cache.usedBytes, 0)
        try put(2, bytes: 100, fav: true, at: t0 + 1)                // the directories are made again
        XCTAssertNotNil(cache.localURL(trackID: 2))
        cache = makeCache()
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [2])
    }

    func testRelaunchWithoutAnIndexRemovesOrphanFilesAndStaging() throws {
        try put(1, bytes: 100, fav: true, at: t0)
        let file = try XCTUnwrap(cache.localURL(trackID: 1))
        cache.flush()
        let staged = root.appendingPathComponent("staging/9.m4a")
        try FileManager.default.createDirectory(at: staged.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data([1]).write(to: staged)
        try FileManager.default.removeItem(at: root.appendingPathComponent("index.json"))
        cache = makeCache()
        XCTAssertEqual(cache.usedBytes, 0)
        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))   // nothing unaccounted for stays on disk
        XCTAssertFalse(FileManager.default.fileExists(atPath: staged.path))
    }

    func testCorruptIndexIsAnEmptyCache() throws {
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try Data("{not json".utf8).write(to: root.appendingPathComponent("index.json"))
        cache = makeCache()
        XCTAssertEqual(cache.usedBytes, 0)
        try put(1, bytes: 100, fav: false, at: t0)
        XCTAssertNotNil(cache.localURL(trackID: 1))
    }

    func testFilesAndDirectoryAreExcludedFromBackup() throws {
        try put(1, bytes: 100, fav: true, at: t0)
        let file = try XCTUnwrap(cache.localURL(trackID: 1))
        XCTAssertEqual(try file.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup, true)
        XCTAssertEqual(try root.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup, true)
        XCTAssertTrue(file.path.hasPrefix(root.path))
    }

    func testStoringAgainReplacesTheFile() throws {
        try put(1, bytes: 100, fav: false, at: t0)
        try put(1, bytes: 40, fav: false, at: t0 + 1)
        XCTAssertEqual(cache.usedBytes, 40)
        let file = try XCTUnwrap(cache.localURL(trackID: 1))
        XCTAssertEqual(try Data(contentsOf: file).count, 40)
    }

    func testClearRemovesEverything() throws {
        try put(1, bytes: 100, fav: true, at: t0); try put(2, bytes: 100, fav: false, at: t0 + 1)
        let file = try XCTUnwrap(cache.localURL(trackID: 1))
        cache.clear()
        XCTAssertEqual(cache.usedBytes, 0)
        XCTAssertNil(cache.localURL(trackID: 1)); XCTAssertTrue(cache.cachedFavorites().isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))
        cache = makeCache()
        XCTAssertEqual(cache.usedBytes, 0)
    }

    func testSetFavoritesFlagsOnlyAndPatchesTheMeta() throws {
        try put(1, bytes: 100, fav: true, at: t0); try put(2, bytes: 100, fav: false, at: t0 + 1)
        cache.setFavorites([2, 3])
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [2])
        let (track, meta) = try XCTUnwrap(cache.cachedFavorites().first)
        XCTAssertTrue(track.favorite); XCTAssertEqual(meta["favorite"], .bool(true))
        XCTAssertNotNil(cache.localURL(trackID: 1))                  // the file stays, as an ordinary LRU entry
        XCTAssertEqual(api.downloadCalls, [])                         // flags only: nothing is fetched
        cache = makeCache()
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [2])
    }

    func testCachedFavoritesReturnTheStoredMeta() throws {
        clock = t0
        let f = scratch.appendingPathComponent("8.m4a")
        try Data(repeating: 1, count: 10).write(to: f)
        let track = Track(id: 8, title: "Song", artist: "Art", album: "Alb", duration_ms: 1234, favorite: true)
        let meta: JSONValue = .object(["id": .int(8), "title": .string("Song"), "codec": .string("flac"), "favorite": .bool(true)])
        try cache.store(trackID: 8, file: f, track: track, meta: meta, favorite: true)
        cache = makeCache()
        let favs = cache.cachedFavorites()
        XCTAssertEqual(favs.count, 1)
        XCTAssertEqual(favs[0].0, track); XCTAssertEqual(favs[0].1, meta)   // unknown fields survive for the web
    }

    // MARK: Prefetch (the lookahead)

    func testPrefetchDownloadsOnlyMissingAndNotTwiceConcurrently() async throws {
        try put(7, bytes: 100, fav: false, at: t0)
        api.holdDownloads = true
        cache.prefetch([track(5), track(5), track(6), track(7)])     // 7 is cached; 5 is asked twice
        cache.prefetch([track(5)])                                    // still downloading: not again
        try await waitUntil { self.api.heldCount == 2 }
        api.releaseDownloads()
        try await settle(downloads: 2)
        XCTAssertEqual(api.downloadCalls.sorted(), [5, 6])
        XCTAssertEqual(api.downloadQualities, ["high", "high"])       // the web's OFFLINE_QUALITY
        XCTAssertNotNil(cache.localURL(trackID: 5)); XCTAssertNotNil(cache.localURL(trackID: 6))
        cache.prefetch([track(5), track(6)])                          // cached now: nothing to do
        XCTAssertFalse(cache.isDownloading)
        XCTAssertEqual(api.downloadCalls.count, 2)
    }

    func testAtMostTwoDownloadsAtOnce() async throws {
        api.holdDownloads = true
        cache.prefetch([track(1), track(2), track(3), track(4)])
        try await waitUntil { self.api.heldCount == 2 }
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(api.downloadCalls.count, 2)                    // the other two wait for a slot
        api.releaseDownloads()
        try await settle(downloads: 4)
        XCTAssertEqual(api.maxDownloading, 2)
        XCTAssertEqual(api.downloadCalls.sorted(), [1, 2, 3, 4])
    }

    func testPrefetchSkipsOnCellularWhenNotAllowed() async throws {
        // The lookahead is allowed on cellular (2 tracks); the favorites sync is not (FavoritesSyncTests).
        network.isExpensive = true
        cache.prefetch([track(1), track(2)])
        try await settle(downloads: 2)
        XCTAssertEqual(api.downloadCalls.sorted(), [1, 2])
        // Offline, or signed out: nothing is asked.
        network.isOnline = false
        cache.prefetch([track(3)])
        network.isOnline = true
        api.token = nil
        cache.prefetch([track(4)])
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(api.downloadCalls.count, 2)
        XCTAssertFalse(cache.isDownloading)
    }

    func testPrefetchStoresTheItemsMetaAndItsFavoriteFlag() async throws {
        var fav = track(3)
        fav = Item(kind: .track, id: "3", title: "T3", artist: "A", album: "B", durationMs: 1000,
                   meta: .object(["id": .int(3), "favorite": .bool(true)]))
        cache.prefetch([fav, track(4), episode("abcdefghijk")])
        try await settle(downloads: 2)
        XCTAssertEqual(api.downloadCalls.sorted(), [3, 4])            // episodes are never cached
        XCTAssertEqual(cache.cachedFavorites().map(\.0.id), [3])
        XCTAssertEqual(cache.cachedFavorites().first?.1, fav.meta)
    }

    func testFailedDownloadLeavesNothingAndCanBeRetried() async throws {
        api.downloadErrors[5] = LarkError.http(status: 500, code: nil)
        cache.prefetch([track(5)])
        try await settle(downloads: 1)
        XCTAssertNil(cache.localURL(trackID: 5))
        XCTAssertEqual(cache.usedBytes, 0)
        api.downloadErrors = [:]
        cache.prefetch([track(5)])
        try await settle(downloads: 2)
        XCTAssertNotNil(cache.localURL(trackID: 5))
    }

    func testDownloadFinishingAfterClearIsDropped() async throws {
        api.holdDownloads = true
        cache.prefetch([track(5)])
        try await waitUntil { self.api.heldCount == 1 }
        cache.clear()
        api.releaseDownloads()
        try await waitUntil { self.api.finishedDownloads == 1 }
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertNil(cache.localURL(trackID: 5))
        XCTAssertEqual(cache.usedBytes, 0)
        let staging = root.appendingPathComponent("staging")
        XCTAssertEqual((try? FileManager.default.contentsOfDirectory(atPath: staging.path)) ?? [], [])
    }

    func testClosedCacheDropsDownloadsInFlight() async throws {
        api.holdDownloads = true
        cache.prefetch([track(5)])
        try await waitUntil { self.api.heldCount == 1 }
        cache.close()                                                 // signed out: this user's cache is set aside
        api.releaseDownloads()
        try await waitUntil { self.api.finishedDownloads == 1 }
        try await Task.sleep(nanoseconds: 100_000_000)
        cache.prefetch([track(6)])                                    // and asks for nothing more
        XCTAssertEqual(api.downloadCalls, [5])
        XCTAssertNil(makeCache().localURL(trackID: 5))
    }

    /// The real API: `quality=high`, the token as a header and never in the URL, the file moved into `files/`.
    func testDownloadGoesThroughLarkAPIWithTheTokenHeader() async throws {
        StubURLProtocol.reset()
        defer { StubURLProtocol.reset() }
        let secrets = MemorySecretStore(); secrets.set("token", "T1")
        let real = LarkAPI(base: URL(string: "https://lark.test")!, session: stubSession(), auth: AuthStore(secrets: secrets))
        cache = CacheStore(root: root, api: real, network: network, now: { [unowned self] in self.clock })
        cache.freeBytes = { 100 << 30 }
        StubURLProtocol.handler = { _ in (.ok(["Content-Type": "audio/mp4"]), Data(repeating: 9, count: 321)) }
        cache.prefetch([track(12)])
        try await waitUntil { self.cache.localURL(trackID: 12) != nil }
        let r = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(r.url?.path, "/api/v1/tracks/12/stream")
        XCTAssertEqual(r.queryItems, [URLQueryItem(name: "quality", value: "high")])
        XCTAssertEqual(r.value(forHTTPHeaderField: "Authorization"), "Bearer T1")
        XCTAssertFalse(r.url!.absoluteString.contains("T1"))
        let file = try XCTUnwrap(cache.localURL(trackID: 12))
        XCTAssertEqual(file.lastPathComponent, "12.m4a")
        XCTAssertEqual(file.deletingLastPathComponent().lastPathComponent, "files")
        XCTAssertEqual(cache.usedBytes, 321)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.appendingPathComponent("staging").path), [])
    }
}

/// Review R1: the index is written at most once per `indexWriteDelay`, and flushed when it must be.
final class IndexWriteTests: CacheTestCase {
    func testTouchesAreCoalescedIntoOneWrite() throws {
        try put(1, bytes: 100, fav: true, at: t0)
        XCTAssertEqual(cache.indexWrites, 0)                          // not yet: the 5 s delay is pending
        cache.flush()
        XCTAssertEqual(cache.indexWrites, 1)
        for i in 1...10 { clock = t0 + Double(i); cache.touch(trackID: 1) }
        XCTAssertEqual(cache.indexWrites, 1)
        cache.flush()
        XCTAssertEqual(cache.indexWrites, 2)
        cache.flush()                                                 // nothing new: no write
        XCTAssertEqual(cache.indexWrites, 2)
        let e = try XCTUnwrap(try indexJSON()["1"] as? [String: Any])
        XCTAssertEqual(e["lastPlayed"] as? Double, (t0 + 10).timeIntervalSince1970)
    }

    func testTheDelayedWriteLandsOnceOnItsOwn() async throws {
        cache = makeCache(indexDelay: 0.1)
        try put(1, bytes: 100, fav: true, at: t0)
        for i in 1...10 { clock = t0 + Double(i); cache.touch(trackID: 1) }
        try await waitUntil { self.cache.indexWrites == 1 }
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertEqual(cache.indexWrites, 1)
        let d = try Data(contentsOf: root.appendingPathComponent("index.json"))
        let e = try XCTUnwrap((try JSONSerialization.jsonObject(with: d) as? [String: Any])?["1"] as? [String: Any])
        XCTAssertEqual(e["lastPlayed"] as? Double, (t0 + 10).timeIntervalSince1970)
    }

    func testCloseFlushes() throws {
        try put(1, bytes: 100, fav: true, at: t0)
        cache.close()
        XCTAssertEqual(cache.indexWrites, 1)
        let reopened = CacheStore(root: root, api: api, network: network)
        XCTAssertNotNil(reopened.localURL(trackID: 1))
    }

    /// After iOS purges the files, noticing each missing one is not a write each.
    func testPurgeRecoveryWritesTheIndexOnce() throws {
        for id in 1...20 { try put(id, bytes: 10, fav: true, at: t0 + Double(id)) }
        cache.flush()
        let writes = cache.indexWrites
        try FileManager.default.removeItem(at: root.appendingPathComponent("files"))
        for id in 1...20 { XCTAssertNil(cache.localURL(trackID: id)) }
        XCTAssertEqual(cache.indexWrites, writes)
        cache.flush()
        XCTAssertEqual(cache.indexWrites, writes + 1)
    }
}

/// Review findings 4 and 5: disk space and headroom.
final class CacheSpaceTests: CacheTestCase {
    func testLookaheadSkipsWhenTheDiskIsNearlyFull() async throws {
        free = { 400 << 20 }                                          // under the 500 MB reserve
        cache.prefetch([track(1), track(2)])
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(api.downloadCalls, [])
        free = { 100 << 30 }
        cache.prefetch([track(1)])
        try await settle(downloads: 1)
        XCTAssertEqual(api.downloadCalls, [1])
    }

    func testOutOfSpaceErrorsAreRecognised() {
        XCTAssertTrue(CacheStore.isOutOfSpace(CocoaError(.fileWriteOutOfSpace)))
        XCTAssertTrue(CacheStore.isOutOfSpace(URLError(.cannotWriteToFile)))
        XCTAssertTrue(CacheStore.isOutOfSpace(NSError(domain: NSPOSIXErrorDomain, code: Int(ENOSPC))))
        XCTAssertTrue(CacheStore.isOutOfSpace(NSError(domain: "x", code: 1,
                                                      userInfo: [NSUnderlyingErrorKey: NSError(domain: NSPOSIXErrorDomain, code: Int(ENOSPC))])))
        XCTAssertFalse(CacheStore.isOutOfSpace(URLError(.notConnectedToInternet)))
        XCTAssertFalse(CacheStore.isOutOfSpace(LarkError.http(status: 500, code: nil)))
    }
}
