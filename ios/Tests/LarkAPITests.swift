import XCTest
@testable import Lark

@MainActor final class LarkAPITests: XCTestCase {
    var auth: AuthStore!
    var api: LarkAPI!
    var dir: URL!

    override func setUp() async throws {
        StubURLProtocol.reset()
        let secrets = MemorySecretStore()
        secrets.set("token", "T1")
        auth = AuthStore(secrets: secrets)
        api = LarkAPI(base: URL(string: "https://lark.test")!, session: stubSession(), auth: auth)
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("larkapi-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }
    override func tearDown() async throws { try? FileManager.default.removeItem(at: dir) }

    func trackItem(_ id: Int) -> Item {
        Item(kind: .track, id: String(id), title: "t", artist: "a", album: "b", durationMs: 1000, meta: .null)
    }
    func episodeItem(_ id: String) -> Item {
        Item(kind: .episode, id: id, title: "t", artist: "a", album: "频道", durationMs: 1000, meta: .null)
    }
    func trackJSON(_ id: Int, extra: String = "") -> String {
        #"{"id":\#(id),"title":"T\#(id)","artist":"A","album":"B","duration_ms":1000,"favorite":true,"codec":"flac"\#(extra)}"#
    }
    func respond(_ body: String, status: Int = 200, headers: [String: String] = ["Content-Type": "application/json"]) {
        StubURLProtocol.handler = { _ in (.status(status, headers: headers), Data(body.utf8)) }
    }

    func testSaveQueueBodyIsExactlyThreeFields() async throws {
        respond(#"{"queue":{"track_ids":[1,2],"current_index":1,"position_ms":5000,"version":2,"updated_by":"device:9","updated_at":1},"tracks":[]}"#)
        let stored = try await api.saveQueue(trackIDs: [1, 2], index: 1, positionMs: 5000)
        XCTAssertEqual(stored?.updated_by, "device:9")
        let r = StubURLProtocol.requests.last!
        XCTAssertEqual(r.httpMethod, "PUT"); XCTAssertEqual(r.url?.path, "/api/v1/queue")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Authorization"), "Bearer T1")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Content-Type"), "application/json")
        XCTAssertEqual(try jsonObject(r.bodyData), ["track_ids": [1, 2], "current_index": 1, "position_ms": 5000] as NSDictionary)
    }

    func testQueueDecodesQueueAndTracksWithRawMeta() async throws {
        respond(#"{"queue":{"track_ids":[5],"current_index":0,"position_ms":10,"version":3,"updated_by":"device:1","updated_at":7},"tracks":[\#(trackJSON(5, extra: #","path":"/x""#))]}"#)
        let (q, tracks) = try await api.queue()
        XCTAssertEqual(q.track_ids, [5]); XCTAssertEqual(q.version, 3); XCTAssertEqual(q.updated_by, "device:1")
        XCTAssertEqual(tracks.first?.0.title, "T5")
        XCTAssertEqual(tracks.first?.1["path"], .string("/x"))   // the raw object, unknown fields kept
        XCTAssertEqual(StubURLProtocol.requests.last?.httpMethod, "GET")
    }

    func testEmptyQueueWithNullTracks() async throws {
        respond(#"{"queue":{"track_ids":[],"current_index":0,"position_ms":0,"version":0,"updated_by":"","updated_at":0},"tracks":null}"#)
        let (q, tracks) = try await api.queue()
        XCTAssertTrue(q.track_ids.isEmpty); XCTAssertTrue(tracks.isEmpty)
    }

    func testPostEventsShape() async throws {
        respond(#"{"accepted":1}"#)
        let e = PlayEvent(client_event_id: "e1", track_id: 4, started_at: 1700, played_seconds: 30, skipped: false, quality: "high")
        let n = try await api.postEvents([e])
        XCTAssertEqual(n, 1)
        let r = StubURLProtocol.requests.last!
        XCTAssertEqual(r.httpMethod, "POST"); XCTAssertEqual(r.url?.path, "/api/v1/events/play")
        XCTAssertEqual(try jsonObject(r.bodyData), ["events": [["client_event_id": "e1", "track_id": 4, "started_at": 1700,
                                                               "played_seconds": 30, "skipped": false, "quality": "high"]]] as NSDictionary)
    }

    func testPostNoEventsSendsNothing() async throws {
        let n = try await api.postEvents([])
        XCTAssertEqual(n, 0); XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testEpisodeProgressPlayed() async throws {
        respond("", status: 204, headers: [:])
        try await api.episodeProgress("abcdefghijk", positionS: 0, played: true)
        var r = StubURLProtocol.requests.last!
        XCTAssertEqual(r.httpMethod, "PUT"); XCTAssertEqual(r.url?.path, "/api/v1/episodes/abcdefghijk/progress")
        XCTAssertEqual(try jsonObject(r.bodyData), ["position_s": 0, "played": true] as NSDictionary)
        try await api.episodeProgress("abcdefghijk", positionS: 12, played: false)
        r = StubURLProtocol.requests.last!
        XCTAssertEqual(try jsonObject(r.bodyData), ["position_s": 12] as NSDictionary)
    }

    func testEpisodeProgressRejectsBadIDWithoutRequest() async {
        await XCTAssertThrowsErrorAsync(try await api.episodeProgress("../queue", positionS: 1, played: false)) {
            XCTAssertEqual($0 as? LarkError, .badRequest)
        }
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testFavoritesFollowsCursor() async throws {
        var n = 0
        StubURLProtocol.handler = { _ in
            n += 1
            let body = n == 1 ? #"{"items":[\#(self.trackJSON(1)),\#(self.trackJSON(2))],"next_cursor":"c2"}"#
                              : #"{"items":[\#(self.trackJSON(3))],"next_cursor":""}"#
            return (.ok(), Data(body.utf8))
        }
        let all = try await api.favorites()
        XCTAssertEqual(all.map(\.0.id), [1, 2, 3])
        let reqs = StubURLProtocol.requests
        XCTAssertEqual(reqs.count, 2)
        XCTAssertEqual(reqs[0].url?.path, "/api/v1/tracks")
        XCTAssertEqual(reqs[0].queryItems, [URLQueryItem(name: "favorite", value: "1"), URLQueryItem(name: "limit", value: "500")])
        XCTAssertEqual(reqs[1].queryItems, [URLQueryItem(name: "favorite", value: "1"), URLQueryItem(name: "limit", value: "500"),
                                            URLQueryItem(name: "cursor", value: "c2")])
    }

    func testFavoritesStopsOnRepeatedCursor() async throws {
        respond(#"{"items":[\#(trackJSON(1))],"next_cursor":"same"}"#)
        let all = try await api.favorites()
        XCTAssertEqual(all.count, 2)   // first page, then "same" once; the repeat ends the loop
        XCTAssertEqual(StubURLProtocol.requests.count, 2)
    }

    func testRandomFavoritesFallbackSource() async throws {
        respond(#"{"source":"all","tracks":[\#(trackJSON(9))]}"#)
        let r = try await api.randomFavorites(n: 20, exclude: [1, 2])
        XCTAssertEqual(r.source, "all"); XCTAssertEqual(r.tracks.map(\.0.id), [9])
        let q = StubURLProtocol.requests.last!
        XCTAssertEqual(q.url?.path, "/api/v1/tracks/random")
        XCTAssertEqual(q.queryItems, [URLQueryItem(name: "source", value: "favorites"), URLQueryItem(name: "n", value: "20"),
                                      URLQueryItem(name: "exclude", value: "1,2")])
    }

    func testRandomTracksAndRadioAreBareArrays() async throws {
        respond("[\(trackJSON(1)),\(trackJSON(2))]")
        let a = try await api.randomTracks(n: 5, exclude: [])
        XCTAssertEqual(a.count, 2)
        XCTAssertEqual(StubURLProtocol.requests.last?.queryItems, [URLQueryItem(name: "n", value: "5")])
        let b = try await api.radio(n: 5, exclude: [3])
        XCTAssertEqual(b.count, 2)
        XCTAssertEqual(StubURLProtocol.requests.last?.url?.path, "/api/v1/radio/next")
        XCTAssertEqual(StubURLProtocol.requests.last?.queryItems, [URLQueryItem(name: "n", value: "5"), URLQueryItem(name: "exclude", value: "3")])
        respond("null")
        let c = try await api.radio(n: 5, exclude: [])
        XCTAssertTrue(c.isEmpty)
    }

    func testLyricsDecodes() async throws {
        respond(#"{"found":true,"id":4,"synced":true,"lines":[{"t_ms":1000,"text":"hi"}],"offset_ms":-200}"#)
        let d = try await api.lyrics(3)
        XCTAssertTrue(d.found); XCTAssertTrue(d.synced); XCTAssertEqual(d.offset_ms, -200)
        XCTAssertEqual(d.lines, [LyricsDoc.Line(t_ms: 1000, text: "hi")])
        XCTAssertEqual(StubURLProtocol.requests.last?.url?.path, "/api/v1/tracks/3/lyrics")
        respond(#"{"found":false,"synced":false,"offset_ms":0}"#)
        let none = try await api.lyrics(3)
        XCTAssertFalse(none.found); XCTAssertNil(none.lines)
    }

    func testArtworkURLs() async throws {
        StubURLProtocol.handler = { _ in (.ok(["Content-Type": "image/jpeg"]), Data([1, 2, 3])) }
        let a = try await api.artwork(trackItem(7))
        XCTAssertEqual(a, Data([1, 2, 3]))
        var r = StubURLProtocol.requests.last!
        XCTAssertEqual(r.url?.path, "/api/v1/tracks/7/cover"); XCTAssertEqual(r.queryItems, [URLQueryItem(name: "size", value: "300")])
        _ = try await api.artwork(episodeItem("abcdefghijk"))
        r = StubURLProtocol.requests.last!
        XCTAssertEqual(r.url?.path, "/api/v1/episodes/abcdefghijk/thumbnail")
    }

    func testDownloadNamesFileByContentType() async throws {
        for (type, ext) in [("audio/mp4", "m4a"), ("audio/mpeg", "mp3"), ("audio/flac", "flac")] {
            StubURLProtocol.handler = { _ in (.ok(["Content-Type": type]), Data("AUDIO".utf8)) }
            let url = try await api.download(trackID: 12, quality: "high", to: dir)
            XCTAssertEqual(url.lastPathComponent, "12.\(ext)")
            XCTAssertEqual(try Data(contentsOf: url), Data("AUDIO".utf8))
            let r = StubURLProtocol.requests.last!
            XCTAssertEqual(r.url?.path, "/api/v1/tracks/12/stream"); XCTAssertEqual(r.queryItems, [URLQueryItem(name: "quality", value: "high")])
            XCTAssertEqual(r.value(forHTTPHeaderField: "Authorization"), "Bearer T1")
            try FileManager.default.removeItem(at: url)
        }
    }

    func testDownloadLeavesNothingOnError() async throws {
        respond(#"{"error":"nope","code":"unplayable"}"#, status: 415)
        await XCTAssertThrowsErrorAsync(try await api.download(trackID: 12, quality: "high", to: dir)) {
            XCTAssertEqual($0 as? LarkError, .http(status: 415, code: "unplayable"))
        }
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: dir.path), [])
        // A non-audio 200 is refused too.
        StubURLProtocol.handler = { _ in (.ok(["Content-Type": "text/html"]), Data("<html>".utf8)) }
        await XCTAssertThrowsErrorAsync(try await api.download(trackID: 12, quality: "high", to: dir))
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: dir.path), [])
    }

    func test401ClearsTokenAndSignals() async throws {
        StubURLProtocol.handler = { _ in (.status(401), Data(#"{"error":"not signed in","code":"not_signed_in"}"#.utf8)) }
        var signalled = false
        api.onUnauthorized = { [auth] in signalled = true; auth!.clear() }   // AppServices wires signOut() here
        await XCTAssertThrowsErrorAsync(try await api.lyrics(1)) { XCTAssertEqual($0 as? LarkError, .unauthorized) }
        XCTAssertNil(auth.token); XCTAssertTrue(signalled)
    }

    func testConcurrent401sSignOutExactlyOnce() async throws {
        StubURLProtocol.handler = { _ in (.status(401), Data()) }
        var calls = 0
        api.onUnauthorized = { [auth] in calls += 1; try? await Task.sleep(nanoseconds: 100_000_000); auth!.clear() }
        let api = self.api!
        await withTaskGroup(of: Void.self) { g in
            for i in 1...5 { g.addTask { @MainActor in _ = try? await api.lyrics(i) } }
        }
        XCTAssertEqual(calls, 1)
        XCTAssertNil(auth.token)
    }

    func testStale401LeavesTheNewTokenAlone() async throws {
        // The request goes out with T1; a re-login to T2 lands before the 401 comes back.
        var calls = 0
        api.onUnauthorized = { calls += 1 }
        let secretsAuth = auth!
        // Swap the token mid-flight: clear() then adopt T2 through a harvest.
        StubURLProtocol.handler = { _ in
            DispatchQueue.main.sync { MainActor.assumeIsolated {
                secretsAuth.clear()
                secretsAuth.harvest(from: [HTTPCookie(properties: [.name: "lark_token", .value: "T2", .domain: "lark.test", .path: "/",
                                                                   HTTPCookiePropertyKey("HttpOnly"): "TRUE"])!],
                                    serverHost: "lark.test", userId: nil)
            } }
            return (.status(401), Data())
        }
        await XCTAssertThrowsErrorAsync(try await api.lyrics(1)) { XCTAssertEqual($0 as? LarkError, .unauthorized) }
        XCTAssertEqual(calls, 0)
        XCTAssertEqual(auth.token, "T2")
    }

    func testDownloadErrorBodyIsReadCapped() async throws {
        let big = #"{"error":"x","code":"unplayable"}"# + String(repeating: " ", count: 100_000)
        respond(big, status: 415)
        await XCTAssertThrowsErrorAsync(try await api.download(trackID: 1, quality: "high", to: dir)) {
            XCTAssertEqual($0 as? LarkError, .http(status: 415, code: "unplayable"))
        }
    }

    func test401WithoutHookStillClearsTheToken() async throws {
        StubURLProtocol.handler = { _ in (.status(401), Data()) }
        await XCTAssertThrowsErrorAsync(try await api.queue()) { XCTAssertEqual($0 as? LarkError, .unauthorized) }
        XCTAssertNil(auth.token)
    }

    func test401OnDownloadSignals() async throws {
        StubURLProtocol.handler = { _ in (.status(401), Data()) }
        var signalled = false
        api.onUnauthorized = { signalled = true }
        await XCTAssertThrowsErrorAsync(try await api.download(trackID: 1, quality: "high", to: dir)) {
            XCTAssertEqual($0 as? LarkError, .unauthorized)
        }
        XCTAssertTrue(signalled)
    }

    func testNoTokenNoRequest() async throws {
        auth.clear()
        await XCTAssertThrowsErrorAsync(try await api.lyrics(1)) { XCTAssertEqual($0 as? LarkError, .signedOut) }
        await XCTAssertThrowsErrorAsync(try await api.download(trackID: 1, quality: "high", to: dir)) { XCTAssertEqual($0 as? LarkError, .signedOut) }
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testStreamURLCarriesNoToken() {
        let u = api.streamURL(trackItem(7), quality: "high")
        XCTAssertFalse(u.absoluteString.contains("T1"))
        XCTAssertEqual(u.absoluteString, "https://lark.test/api/v1/tracks/7/stream?quality=high")
        XCTAssertEqual(api.streamURL(episodeItem("abcdefghijk"), quality: "high").absoluteString,
                       "https://lark.test/api/v1/episodes/abcdefghijk/stream?kind=audio")
        XCTAssertEqual(api.token, "T1")
    }

    func testHTTPErrorCarriesServerCode() async {
        respond(#"{"error":"x","code":"rate_limited"}"#, status: 429)
        await XCTAssertThrowsErrorAsync(try await api.queue()) { XCTAssertEqual($0 as? LarkError, .http(status: 429, code: "rate_limited")) }
    }
}
