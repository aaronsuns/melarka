import XCTest
import WebKit
@testable import Lark

final class AppServicesTests: XCTestCase {
    override func setUp() async throws {
        try await ColdStart.webKit()
    }

    @MainActor func testSignOutDeletesTheSessionCookieSoItCannotBeHarvestedAgain() async throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let s = AppServices(defaults: defaults, secrets: MemorySecretStore())
        let server = URL(string: "https://signout.lark.test/")!
        await s.setServerURL(server)
        let store = s.web(for: server).configuration.websiteDataStore.httpCookieStore
        let props: [HTTPCookiePropertyKey: Any] = [.name: "lark_token", .value: "T1", .domain: "signout.lark.test", .path: "/",
                                                   HTTPCookiePropertyKey("HttpOnly"): "TRUE"]
        try await store.setCookieAndWait(try XCTUnwrap(HTTPCookie(properties: props)))
        try await store.setCookieAndWait(try XCTUnwrap(HTTPCookie(properties: [.name: "other", .value: "keep", .domain: "signout.lark.test", .path: "/"])))
        await s.harvestCookies(userId: 3)
        XCTAssertEqual(s.auth.token, "T1")

        await s.signOut()
        XCTAssertNil(s.auth.token)
        var left: [HTTPCookie] = []
        try await waitUntil(timeout: 30, state: { "left: \(left.map(\.name))" }) {
            left = await store.allCookies().filter { $0.domain == "signout.lark.test" }
            return left.map(\.name) == ["other"]
        }
        await s.harvestCookies(userId: 3)
        XCTAssertNil(s.auth.token)
        for c in left { await store.deleteCookie(c) }
    }
}

extension AppServicesTests {
    /// The API's 401 hook is `AppServices.signOut()`: token cleared AND the session cookie deleted.
    @MainActor func test401FromTheAPIRunsSignOut() async throws {
        StubURLProtocol.reset()
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let s = AppServices(defaults: defaults, secrets: MemorySecretStore())
        let server = URL(string: "https://api401.lark.test/")!
        await s.setServerURL(server)
        let store = s.web(for: server).configuration.websiteDataStore.httpCookieStore
        let props: [HTTPCookiePropertyKey: Any] = [.name: "lark_token", .value: "T9", .domain: "api401.lark.test", .path: "/",
                                                   HTTPCookiePropertyKey("HttpOnly"): "TRUE"]
        try await store.setCookieAndWait(try XCTUnwrap(HTTPCookie(properties: props)))
        await s.harvestCookies(userId: 1)
        XCTAssertEqual(s.auth.token, "T9")

        let api = try XCTUnwrap(s.api(session: stubSession()))
        StubURLProtocol.handler = { _ in (.status(401), Data()) }
        await XCTAssertThrowsErrorAsync(try await api.queue()) { XCTAssertEqual($0 as? LarkError, .unauthorized) }
        XCTAssertNil(s.auth.token)
        try await waitUntil(timeout: 30, state: { "the session cookie is still in the store" }) {
            await store.allCookies().filter { $0.name == "lark_token" && $0.domain == "api401.lark.test" }.isEmpty
        }
    }
}

extension AppServicesTests {
    @MainActor func makeServices(user: Int = 5, backend: FakeBackend, dir: URL) throws -> AppServices {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", String(user))
        defaults.set("https://engine.lark.test/", forKey: AppServices.serverURLKey)
        let s = AppServices(defaults: defaults, secrets: secrets, session: stubSession(), dataDirectory: dir,
                            backend: backend, cache: FakeCache(), network: FakeNetwork())
        return s
    }

    /// Sign-out: the listen in progress is posted while the token still works, then playback stops, the old
    /// user's queues and pending events are wiped, the token is cleared and the web is told to sign in again.
    @MainActor func testSignOutStopsPlaybackWipesTheUsersDataAndSendsAuthRequired() async throws {
        StubURLProtocol.reset()
        StubURLProtocol.handler = { _ in (.ok(), Data(#"{"accepted":1}"#.utf8)) }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("svc-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let backend = FakeBackend()
        let s = try makeServices(backend: backend, dir: dir)
        var sent: [NativeEvent] = []
        s.eventSink = { sent.append($0) }

        s.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 40_000)
        let queueFile = dir.appendingPathComponent("queues-5.json")
        XCTAssertTrue(FileManager.default.fileExists(atPath: queueFile.path))
        XCTAssertTrue(sent.contains { if case .state = $0 { return true }; return false })   // engine events reach the bridge

        await s.signOut()
        XCTAssertEqual(backend.calls.last, "stop")
        XCTAssertFalse(FileManager.default.fileExists(atPath: queueFile.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("events-5.json").path))
        XCTAssertNil(s.auth.token)
        XCTAssertEqual(sent.last, .authRequired)
        let post = StubURLProtocol.requests.first { $0.url?.path == "/api/v1/events/play" }
        XCTAssertEqual(post?.value(forHTTPHeaderField: "Authorization"), "Bearer T1")
    }

    /// A 401 during sign-out's own flush must not deadlock or loop.
    @MainActor func testA401DuringTheSignOutFlushStillSignsOutOnce() async throws {
        StubURLProtocol.reset()
        StubURLProtocol.handler = { _ in (.status(401), Data()) }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("svc-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let backend = FakeBackend()
        let s = try makeServices(backend: backend, dir: dir)
        var sent: [NativeEvent] = []
        s.eventSink = { sent.append($0) }
        s.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 40_000)

        await s.signOut()
        XCTAssertNil(s.auth.token)
        XCTAssertEqual(sent.filter { $0 == .authRequired }.count, 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-5.json").path))
    }

    /// Another user signing in: the old user's queue is wiped and the new user's own one is used.
    @MainActor func testAUserSwitchWipesTheOldUsersQueue() async throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("svc-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let backend = FakeBackend()
        let s = try makeServices(backend: backend, dir: dir)
        s.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-5.json").path))

        s.handle(.auth(signedIn: true, userId: 6))
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-5.json").path))
        XCTAssertEqual(backend.calls.last, "stop")
        XCTAssertEqual(s.engine?.music.items.count, 0)
        s.handle(.setQueue(q([8], index: 0, pos: 0, play: false)))
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-6.json").path))
    }

    @MainActor func testTheSameUserSigningInAgainKeepsTheQueue() async throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("svc-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let s = try makeServices(backend: FakeBackend(), dir: dir)
        s.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))
        s.handle(.auth(signedIn: true, userId: 5))
        XCTAssertEqual(s.engine?.music.items.map(\.id), ["7"])
    }
}

extension AppServicesTests {
    /// A server that hangs cannot hold sign-out up: the flush gets `signOutFlushTimeout` seconds.
    @MainActor func testTheSignOutFlushIsBoundedByATimeout() async throws {
        StubURLProtocol.reset()
        StubURLProtocol.handler = { _ in Thread.sleep(forTimeInterval: 2); return (.ok(), Data(#"{"accepted":1}"#.utf8)) }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("svc-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let backend = FakeBackend()
        let s = try makeServices(backend: backend, dir: dir)
        s.signOutFlushTimeout = 0.3
        s.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 40_000)
        let t0 = Date()
        await s.signOut()
        XCTAssertLessThan(Date().timeIntervalSince(t0), 1.5)
        XCTAssertNil(s.auth.token)
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-5.json").path))
    }

    /// Another user signing in while the old sign-out is still flushing: the new user's data and token are left alone.
    @MainActor func testALoginDuringTheSignOutFlushIsLeftAlone() async throws {
        StubURLProtocol.reset()
        StubURLProtocol.handler = { _ in Thread.sleep(forTimeInterval: 1); return (.ok(), Data(#"{"accepted":1}"#.utf8)) }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("svc-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let backend = FakeBackend()
        let s = try makeServices(backend: backend, dir: dir)
        var sent: [NativeEvent] = []
        s.eventSink = { sent.append($0) }
        s.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 40_000)

        let signOut = Task { await s.signOut() }
        try await Task.sleep(nanoseconds: 200_000_000)
        s.handle(.auth(signedIn: true, userId: 6))
        s.handle(.setQueue(q([8], index: 0, pos: 0, play: false)))
        await signOut.value

        XCTAssertEqual(s.queueStore.user, 6)
        XCTAssertEqual(s.auth.token, "T1")
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-6.json").path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("queues-5.json").path))
        XCTAssertFalse(sent.contains(.authRequired))
    }
}
