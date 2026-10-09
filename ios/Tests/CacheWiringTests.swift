import XCTest
import UIKit
import WebKit
@testable import Lark

/// `AppServices`: the per-user cache, the sync triggers, sign-out.
@MainActor final class CacheWiringTests: XCTestCase {
    var dir: URL!
    var network: FakeNetwork!
    var backend: FakeBackend!
    var clock = Date(timeIntervalSince1970: 1_800_000_000)

    override func setUp() async throws {
        StubURLProtocol.reset()
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("wiring-\(UUID().uuidString)")
        network = FakeNetwork(); backend = FakeBackend()
    }

    override func tearDown() async throws {
        StubURLProtocol.reset()
        try? FileManager.default.removeItem(at: dir)
    }

    var caches: URL { dir.appendingPathComponent("caches") }
    static let server = URL(string: "https://cache.lark.test/")!
    /// This server's folder: `caches/<host-hash>`.
    var serverDir: URL { caches.appendingPathComponent(AppServices.serverKey(Self.server)) }

    func makeServices(user: Int = 5) throws -> AppServices {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", String(user))
        defaults.set("https://cache.lark.test/", forKey: AppServices.serverURLKey)
        let s = AppServices(defaults: defaults, secrets: secrets, session: stubSession(), dataDirectory: dir.appendingPathComponent("data"),
                            backend: backend, network: network, cacheRoot: caches)
        s.now = { [unowned self] in self.clock }
        return s
    }

    /// The server: these favorites, and a 50-byte AAC file for any stream.
    func serve(favorites ids: [Int]) {
        let items = ids.map { #"{"id":\#($0),"title":"T\#($0)","artist":"A","album":"B","duration_ms":1000,"favorite":true}"# }
        let page = Data(#"{"items":[\#(items.joined(separator: ","))],"next_cursor":""}"#.utf8)
        StubURLProtocol.handler = { r in
            let path = r.url?.path ?? ""
            if path == "/api/v1/tracks" { return (.ok(), page) }
            if path.hasSuffix("/stream") { return (.ok(["Content-Type": "audio/mp4"]), Data(repeating: 1, count: 50)) }
            return (.status(404), Data())
        }
    }

    var listCalls: Int { StubURLProtocol.requests.filter { $0.url?.path == "/api/v1/tracks" }.count }

    func testFavoriteChangedSyncsTheCacheOfThatUser() async throws {
        serve(favorites: [2])
        let s = try makeServices()
        s.handle(.favoriteChanged(trackId: 2, on: true))
        try await waitUntil { s.cache.localURL(trackID: 2) != nil }
        let file = try XCTUnwrap(s.cache.localURL(trackID: 2))
        XCTAssertEqual(file.path, serverDir.appendingPathComponent("5/files/2.m4a").path)   // Library/Caches/lark/<host-hash>/<userId>
        XCTAssertEqual(s.cache.cachedFavorites().map(\.0.id), [2])
        let list = try XCTUnwrap(StubURLProtocol.requests.first { $0.url?.path == "/api/v1/tracks" })
        XCTAssertTrue(list.queryItems.contains(URLQueryItem(name: "favorite", value: "1")))
    }

    func testFavoriteChangedOnCellularWaitsForWiFi() async throws {
        serve(favorites: [2])
        network.isExpensive = true
        let s = try makeServices()
        s.handle(.favoriteChanged(trackId: 2, on: true))
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(StubURLProtocol.requests.count, 0)
        network.isExpensive = false                                   // onto Wi-Fi: the skipped sync runs
        try await waitUntil { s.cache.localURL(trackID: 2) != nil }
    }

    func testUnfavoriteIsFlaggedAtOnceEvenOffline() async throws {
        serve(favorites: [2, 3])
        let s = try makeServices()
        await s.syncFavoritesNow()
        XCTAssertEqual(s.cache.cachedFavorites().map(\.0.id), [2, 3])
        network.isOnline = false
        s.handle(.favoriteChanged(trackId: 2, on: false))
        XCTAssertEqual(s.cache.cachedFavorites().map(\.0.id), [3])    // an offline shuffle no longer picks it
        XCTAssertNotNil(s.cache.localURL(trackID: 2))
    }

    func testSignOutSetsTheCacheAsideUntilThatUserSignsInAgain() async throws {
        serve(favorites: [2])
        let s = try makeServices()
        await s.syncFavoritesNow()
        let file = try XCTUnwrap(s.cache.localURL(trackID: 2))

        await s.signOut(flush: false)
        XCTAssertNil(s.cache.localURL(trackID: 2))                    // not used while signed out ...
        XCTAssertTrue(s.cache.cachedFavorites().isEmpty)
        XCTAssertTrue(FileManager.default.fileExists(atPath: file.path))   // ... but kept

        s.handle(.auth(signedIn: true, userId: 6))                     // another user: their own (empty) cache
        XCTAssertNil(s.cache.localURL(trackID: 2))
        s.handle(.auth(signedIn: true, userId: 5))                     // the same user again: it is back
        XCTAssertEqual(s.cache.localURL(trackID: 2), file)
    }

    func testSignInSyncs() async throws {
        serve(favorites: [4])
        let s = try makeServices()
        s.handle(.auth(signedIn: true, userId: 5))
        try await waitUntil { s.cache.localURL(trackID: 4) != nil }
    }

    func testLaunchSyncsAndActivationSyncsAtMostOnceAnHour() async throws {
        serve(favorites: [])
        let s = try makeServices()
        s.start()                                                      // launch
        try await waitUntil { self.listCalls == 1 }
        s.scenePhaseChanged(.active)                                   // the launch's own activation
        clock = clock.addingTimeInterval(1800)
        s.scenePhaseChanged(.background); s.scenePhaseChanged(.active)
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(listCalls, 1)
        clock = clock.addingTimeInterval(1801)
        s.scenePhaseChanged(.active)
        try await waitUntil { self.listCalls == 2 }
    }

    func testTriggersDuringASyncRunOneMoreAfterIt() async throws {
        serve(favorites: [2])
        let s = try makeServices()
        s.handle(.favoriteChanged(trackId: 2, on: true))
        s.handle(.favoriteChanged(trackId: 3, on: true))
        s.handle(.favoriteChanged(trackId: 4, on: true))
        try await waitUntil { self.listCalls == 2 }                   // one running, one queued: not three
        try await Task.sleep(nanoseconds: 150_000_000)
        XCTAssertEqual(listCalls, 2)
    }

    func testTheEngineReadsTheUsersCache() async throws {
        serve(favorites: [2])
        let s = try makeServices()
        await s.syncFavoritesNow()
        network.isOnline = false
        s.handle(.setQueue(q([2], index: 0, pos: 0, play: true)))
        XCTAssertEqual(s.engine?.current?.id, "2")
        XCTAssertEqual(backend.loads.last?.0, .file(serverDir.appendingPathComponent("5/files/2.m4a")))
    }

    // MARK: Per server

    func testServerKeyNormalisesTheBaseURL() {
        let k = AppServices.serverKey
        XCTAssertEqual(k(URL(string: "https://Cache.Lark.test/")!), k(URL(string: "https://cache.lark.test")!))
        XCTAssertNotEqual(k(URL(string: "https://cache.lark.test")!), k(URL(string: "https://cache.lark.test:8443")!))
        XCTAssertNotEqual(k(URL(string: "https://cache.lark.test")!), k(URL(string: "https://cache.lark.test/lark")!))
        XCTAssertNotEqual(k(URL(string: "https://cache.lark.test")!), k(URL(string: "http://cache.lark.test")!))
        XCTAssertEqual(k(URL(string: "https://cache.lark.test")!).count, 16)
    }

    /// The same user id on another server is another library: nothing of the first server's cache is used,
    /// and its folder is deleted. (Leaving a server also signs out of it, so the new server's
    /// cache starts once the user signs in there.)
    func testAnotherServerHasItsOwnCacheAndTheOldOneIsDeleted() async throws {
        serve(favorites: [2])
        let s = try makeServices()
        await s.syncFavoritesNow()
        XCTAssertNotNil(s.cache.localURL(trackID: 2))
        let otherServer = URL(string: "https://other.lark.test/")!
        await s.setServerURL(otherServer)
        XCTAssertNil(s.auth.token)                                     // the old server's token never goes to the new one
        XCTAssertNil(s.cache.localURL(trackID: 2))
        XCTAssertTrue(s.cache.cachedFavorites().isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: serverDir.path))
        // Signing in on the new server: its own folder.
        let cookies = s.web(for: otherServer).configuration.websiteDataStore.httpCookieStore
        let props: [HTTPCookiePropertyKey: Any] = [.name: "lark_token", .value: "T2", .domain: "other.lark.test", .path: "/",
                                                   HTTPCookiePropertyKey("HttpOnly"): "TRUE"]
        let cookie = try XCTUnwrap(HTTPCookie(properties: props))
        await cookies.setCookie(cookie)
        s.handle(.auth(signedIn: true, userId: 5))
        let other = caches.appendingPathComponent(AppServices.serverKey(otherServer))
        try await waitUntil { s.cache.localURL(trackID: 2) != nil }
        XCTAssertEqual(s.cache.localURL(trackID: 2)?.path, other.appendingPathComponent("5/files/2.m4a").path)
        await cookies.deleteCookie(cookie)
    }

    func testLegacyPerUserFoldersAreRemovedAtLaunch() throws {
        let legacy = caches.appendingPathComponent("5/files")
        try FileManager.default.createDirectory(at: legacy, withIntermediateDirectories: true)
        try Data([1]).write(to: legacy.appendingPathComponent("2.m4a"))
        let s = try makeServices()
        XCTAssertFalse(FileManager.default.fileExists(atPath: caches.appendingPathComponent("5").path))
        XCTAssertNil(s.cache.localURL(trackID: 2))
    }

    // MARK: The first network answer

    /// A cold background launch: the monitor has not answered yet. The sync waits for it (bounded), then runs
    /// inside the call, so the background task covers it.
    func testBackgroundSyncWaitsForTheFirstNetworkAnswer() async throws {
        serve(favorites: [2])
        network.answered = false; network.isExpensive = true          // what PathNetworkStatus reports before its answer
        let s = try makeServices()
        let run = Task { await s.syncFavoritesNow() }
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertEqual(StubURLProtocol.requests.map { $0.url?.absoluteString ?? "" }, [])
        network.answer(online: true, expensive: false)
        await run.value
        XCTAssertNotNil(s.cache.localURL(trackID: 2))                  // done before the call returned
    }

    func testTheWaitForTheFirstAnswerIsBounded() async throws {
        serve(favorites: [2])
        network.answered = false
        let s = try makeServices()
        s.networkAnswerTimeout = 0.3
        let t0 = Date()
        await s.syncFavoritesNow()
        XCTAssertLessThan(Date().timeIntervalSince(t0), 1.5)
        XCTAssertEqual(StubURLProtocol.requests.count, 0)              // unknown is not Wi-Fi
        network.answer(online: true, expensive: false)                 // the skipped sync runs on the answer
        try await waitUntil { s.cache.localURL(trackID: 2) != nil }
    }

    // MARK: Cancelled syncs

    func testACancelledSyncDoesNotLeaveARerunBehind() async throws {
        let page = Data(#"{"items":[{"id":2,"title":"T2","artist":"A","album":"B","duration_ms":1000,"favorite":true}],"next_cursor":""}"#.utf8)
        StubURLProtocol.handler = { r in
            if r.url?.path == "/api/v1/tracks" { return (.ok(), page) }
            Thread.sleep(forTimeInterval: 0.4)
            return (.ok(["Content-Type": "audio/mp4"]), Data(repeating: 1, count: 50))
        }
        let s = try makeServices()
        let run = Task { await s.syncFavoritesNow() }
        try await waitUntil { self.listCalls == 1 }
        s.syncFavorites()                                              // a trigger during the run: one more is due
        run.cancel()                                                   // the background task expires
        await run.value
        try await Task.sleep(nanoseconds: 600_000_000)
        XCTAssertEqual(listCalls, 1)
        s.syncFavorites()                                              // a later trigger runs once, not twice
        try await waitUntil { self.listCalls == 2 }
        try await Task.sleep(nanoseconds: 900_000_000)
        XCTAssertEqual(listCalls, 2)
    }

    // MARK: Flushes

    func testBackgroundingAndTerminationFlushTheIndex() async throws {
        serve(favorites: [2])
        let s = try makeServices()
        await s.syncFavoritesNow()
        let index = serverDir.appendingPathComponent("5/index.json")
        XCTAssertFalse(FileManager.default.fileExists(atPath: index.path))   // coalesced: not written yet
        s.scenePhaseChanged(.background)
        XCTAssertTrue(FileManager.default.fileExists(atPath: index.path))
        try FileManager.default.removeItem(at: index)
        s.handle(.favoriteChanged(trackId: 2, on: false))              // a change, then the app is terminated
        NotificationCenter.default.post(name: UIApplication.willTerminateNotification, object: nil)
        XCTAssertTrue(FileManager.default.fileExists(atPath: index.path))
        // The unfavorite started a sync: let it end here, or its requests land in the next test's stub log.
        try await waitUntil { !s.isSyncing }
    }

    func testNetworkHasBothTheEngineAndTheCacheAsObservers() throws {
        let s = try makeServices()
        _ = s.engine
        XCTAssertEqual(network.observers.count, 2)
    }
}

/// The OS background refresh: `BackgroundRefresh` runs the sync, cancels it on expiry, and asks again ≥ 6 h later.
@MainActor final class BackgroundRefreshTests: XCTestCase {
    final class FakeTask: RefreshTask, @unchecked Sendable {
        var expiration: (@Sendable () -> Void)?
        var completed: [Bool] = []
        func onExpiration(_ fn: @escaping @Sendable () -> Void) { expiration = fn }
        func complete(success: Bool) { completed.append(success) }
    }

    func testRunsTheSyncAndSchedulesTheNextInSixHours() async throws {
        var submitted: [Date] = []
        var runs = 0
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let r = BackgroundRefresh(run: { runs += 1 }, submit: { submitted.append($0) }, now: { now })
        r.schedule()
        XCTAssertEqual(submitted, [now.addingTimeInterval(6 * 3600)])
        XCTAssertEqual(BackgroundRefresh.identifier, "io.github.aaronsuns.melarka.refresh")
        let task = FakeTask()
        r.perform(task)
        try await waitUntil { task.completed == [true] }
        XCTAssertEqual(runs, 1)
        XCTAssertEqual(submitted.count, 2)                             // rescheduled each time
        XCTAssertGreaterThanOrEqual(submitted[1].timeIntervalSince(now), 6 * 3600)
    }

    func testExpirationCancelsTheRun() async throws {
        var sawCancel = false
        let r = BackgroundRefresh(run: {
            while !Task.isCancelled { try? await Task.sleep(nanoseconds: 20_000_000) }
            sawCancel = true
        }, submit: { _ in }, now: Date.init)
        let task = FakeTask()
        r.perform(task)
        try await waitUntil { task.expiration != nil }
        task.expiration?()
        try await waitUntil { task.completed == [false] }
        XCTAssertTrue(sawCancel)
    }

    func testASubmitFailureIsNotFatal() {
        struct Refused: Error {}
        let r = BackgroundRefresh(run: {}, submit: { _ in throw Refused() }, now: Date.init)
        r.schedule()                                                    // best effort: logged, nothing else
    }
}
