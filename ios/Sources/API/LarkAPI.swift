import Foundation

@MainActor protocol LarkAPIProtocol: AnyObject {
    func favorites() async throws -> [(Track, JSONValue)]                  // GET /tracks?favorite=1&limit=500 (+cursor) — all pages
    func randomFavorites(n: Int, exclude: [Int]) async throws -> (source: String, tracks: [(Track, JSONValue)]) // GET /tracks/random?source=favorites
    func randomTracks(n: Int, exclude: [Int]) async throws -> [(Track, JSONValue)]   // GET /tracks/random
    func radio(n: Int, exclude: [Int]) async throws -> [(Track, JSONValue)]          // GET /radio/next
    func queue() async throws -> (ServerQueue, [(Track, JSONValue)])                 // GET /queue
    @discardableResult
    func saveQueue(trackIDs: [Int], index: Int, positionMs: Int) async throws -> ServerQueue?  // PUT /queue; the stored queue (nil: unreadable answer)
    func postEvents(_ e: [PlayEvent]) async throws -> Int                            // POST /events/play
    func episodeProgress(_ id: String, positionS: Int, played: Bool) async throws    // PUT /episodes/{id}/progress
    func lyrics(_ trackID: Int) async throws -> LyricsDoc                            // GET /tracks/{id}/lyrics
    func artwork(_ item: Item) async throws -> Data                                  // track: /tracks/{id}/cover?size=300; episode: /episodes/{id}/thumbnail
    func download(trackID: Int, quality: String, to dir: URL) async throws -> URL    // GET /tracks/{id}/stream?quality= → file with ext from Content-Type
    func streamURL(_ item: Item, quality: String) -> URL                             // /api/v1/tracks/{id}/stream?quality= or /api/v1/episodes/{id}/stream?kind=audio
    var token: String? { get }
}

/// The Melarka server's HTTP API. Every call sends `Authorization: Bearer <token>`; with no token it throws
/// `.signedOut` without a request. The token is never put in a URL (`streamURL` has none; the player adds
/// the header itself).
@MainActor final class LarkAPI: LarkAPIProtocol {
    let base: URL
    private let session: URLSession
    private let auth: AuthStore
    /// Run on a 401, before `.unauthorized` is thrown. `AppServices` wires `signOut()` here, which clears
    /// the token and deletes the session cookie. With no hook set, the API clears the token itself.
    var onUnauthorized: (() async -> Void)?

    static let jsonTimeout: TimeInterval = 15
    static let downloadTimeout: TimeInterval = 120
    private static let pageLimit = 500
    private static let maxPages = 100

    init(base: URL, session: URLSession, auth: AuthStore) {
        self.base = base; self.session = session; self.auth = auth
    }

    var token: String? { auth.token }

    // MARK: URLs

    private func url(_ path: String, _ query: [(String, String)] = []) -> URL {
        var c = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        var prefix = c.path
        while prefix.hasSuffix("/") { prefix.removeLast() }
        c.path = prefix + "/api/v1" + path
        c.queryItems = query.isEmpty ? nil : query.map { URLQueryItem(name: $0.0, value: $0.1) }
        return c.url!
    }

    /// YouTube video ids only: they go straight into a path.
    private func episodePath(_ id: String, _ tail: String) throws -> String {
        guard id.range(of: "^[A-Za-z0-9_-]{11}$", options: .regularExpression) != nil else { throw LarkError.badRequest }
        return "/episodes/\(id)\(tail)"
    }

    func streamURL(_ item: Item, quality: String) -> URL {
        switch item.kind {
        case .track: return url("/tracks/\(item.id)/stream", [("quality", quality)])
        case .episode: return url("/episodes/\(item.id)/stream", [("kind", "audio")])
        }
    }

    // MARK: Transport

    private func request(_ method: String, _ url: URL, body: Data? = nil, timeout: TimeInterval = jsonTimeout) throws -> URLRequest {
        guard let token = auth.token else { throw LarkError.signedOut }
        var r = URLRequest(url: url, timeoutInterval: timeout)
        r.httpMethod = method
        r.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if let body { r.httpBody = body; r.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        return r
    }

    private struct ErrorBody: Decodable { let code: String? }

    /// Maps a non-2xx answer to an error; a 401 first runs the sign-out hook.
    private func check(_ resp: URLResponse, body: Data?, sent: URLRequest) async throws {
        guard let http = resp as? HTTPURLResponse else { throw LarkError.badResponse }
        guard !(200..<300).contains(http.statusCode) else { return }
        if http.statusCode == 401 {
            // Only the token this request carried can be signed out: a late 401 for an old token, after a
            // re-login (or after another 401 already signed out), must leave the current state alone.
            let sentToken = sent.value(forHTTPHeaderField: "Authorization").map { String($0.dropFirst("Bearer ".count)) }
            if let sentToken, auth.token == sentToken { await signOutOnce() }
            throw LarkError.unauthorized
        }
        let code = body.flatMap { try? JSONDecoder().decode(ErrorBody.self, from: $0) }?.code
        throw LarkError.http(status: http.statusCode, code: code)
    }

    private var signOutTask: Task<Void, Never>?

    /// Concurrent 401s share one in-flight sign-out.
    private func signOutOnce() async {
        if let t = signOutTask { await t.value; return }
        let hook = onUnauthorized
        let t = Task { @MainActor [auth] in if let hook { await hook() } else { auth.clear() } }
        signOutTask = t
        await t.value
        signOutTask = nil
    }

    private func send(_ r: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, resp) = try await session.data(for: r)
        try await check(resp, body: data, sent: r)
        return (data, resp as! HTTPURLResponse)
    }

    private func get<T: Decodable>(_ type: T.Type, _ url: URL) async throws -> T {
        let (data, _) = try await send(try request("GET", url))
        do { return try JSONDecoder().decode(T.self, from: data) } catch { throw LarkError.badResponse }
    }

    private func encode<T: Encodable>(_ v: T) throws -> Data {
        let e = JSONEncoder(); e.outputFormatting = [.sortedKeys]
        return try e.encode(v)
    }

    private static func pairs(_ raws: [Raw<Track>]?) -> [(Track, JSONValue)] { (raws ?? []).map { ($0.value, $0.json) } }

    private func ids(_ a: [Int]) -> String { a.map(String.init).joined(separator: ",") }

    // MARK: Library

    func favorites() async throws -> [(Track, JSONValue)] {
        struct Page: Decodable { let items: [Raw<Track>]?; let next_cursor: String? }
        var out: [(Track, JSONValue)] = []
        var cursor: String?
        for _ in 0..<Self.maxPages {
            var q = [("favorite", "1"), ("limit", String(Self.pageLimit))]
            if let cursor { q.append(("cursor", cursor)) }
            let page = try await get(Page.self, url("/tracks", q))
            out += Self.pairs(page.items)
            let next = page.next_cursor ?? ""
            if next.isEmpty || next == cursor { break }   // a repeated cursor would loop forever
            cursor = next
        }
        return out
    }

    func randomFavorites(n: Int, exclude: [Int]) async throws -> (source: String, tracks: [(Track, JSONValue)]) {
        struct R: Decodable { let source: String; let tracks: [Raw<Track>]? }
        var q = [("source", "favorites"), ("n", String(n))]
        if !exclude.isEmpty { q.append(("exclude", ids(exclude))) }
        let r = try await get(R.self, url("/tracks/random", q))
        return (r.source, Self.pairs(r.tracks))
    }

    func randomTracks(n: Int, exclude: [Int]) async throws -> [(Track, JSONValue)] {
        var q = [("n", String(n))]
        if !exclude.isEmpty { q.append(("exclude", ids(exclude))) }
        return Self.pairs(try await get([Raw<Track>]?.self, url("/tracks/random", q)))
    }

    func radio(n: Int, exclude: [Int]) async throws -> [(Track, JSONValue)] {
        var q = [("n", String(n))]
        if !exclude.isEmpty { q.append(("exclude", ids(exclude))) }
        return Self.pairs(try await get([Raw<Track>]?.self, url("/radio/next", q)))
    }

    // MARK: Personal

    func queue() async throws -> (ServerQueue, [(Track, JSONValue)]) {
        struct R: Decodable { let queue: ServerQueue; let tracks: [Raw<Track>]? }
        let r = try await get(R.self, url("/queue"))
        return (r.queue, Self.pairs(r.tracks))
    }

    /// Answers the queue as stored, whose `updated_by` is the server's name for this device: the engine
    /// uses it to tell its own queue from another device's. An answer it cannot read is not an error.
    @discardableResult
    func saveQueue(trackIDs: [Int], index: Int, positionMs: Int) async throws -> ServerQueue? {
        // The server's decoder rejects unknown fields: exactly these three, no version.
        struct Body: Encodable { let track_ids: [Int]; let current_index: Int; let position_ms: Int }
        struct R: Decodable { let queue: ServerQueue }
        let body = try encode(Body(track_ids: trackIDs, current_index: index, position_ms: positionMs))
        let (data, _) = try await send(try request("PUT", url("/queue"), body: body))
        return (try? JSONDecoder().decode(R.self, from: data))?.queue
    }

    func postEvents(_ e: [PlayEvent]) async throws -> Int {
        guard !e.isEmpty else { return 0 }
        struct Body: Encodable { let events: [PlayEvent] }
        struct R: Decodable { let accepted: Int }
        let (data, _) = try await send(try request("POST", url("/events/play"), body: try encode(Body(events: e))))
        do { return try JSONDecoder().decode(R.self, from: data).accepted } catch { throw LarkError.badResponse }
    }

    func episodeProgress(_ id: String, positionS: Int, played: Bool) async throws {
        let path = try episodePath(id, "/progress")
        // `played` only when true: the server treats a missing field as "leave the played flag alone".
        let obj: [String: Any] = played ? ["position_s": positionS, "played": true] : ["position_s": positionS]
        let body = try JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys])
        _ = try await send(try request("PUT", url(path), body: body))
    }

    func lyrics(_ trackID: Int) async throws -> LyricsDoc {
        try await get(LyricsDoc.self, url("/tracks/\(trackID)/lyrics"))
    }

    // MARK: Binary

    func artwork(_ item: Item) async throws -> Data {
        switch item.kind {
        case .track: return try await send(try request("GET", url("/tracks/\(item.id)/cover", [("size", "300")]))).0
        case .episode: return try await send(try request("GET", url(try episodePath(item.id, "/thumbnail")))).0
        }
    }

    func download(trackID: Int, quality: String, to dir: URL) async throws -> URL {
        let r = try request("GET", url("/tracks/\(trackID)/stream", [("quality", quality)]), timeout: Self.downloadTimeout)
        let (tmp, resp) = try await session.download(for: r)
        defer { try? FileManager.default.removeItem(at: tmp) }   // gone after a successful move; the partial otherwise
        let errBody = (resp as? HTTPURLResponse).flatMap { (200..<300).contains($0.statusCode) ? nil : Self.head(of: tmp) }
        try await check(resp, body: errBody, sent: r)
        let type = ((resp as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Type") ?? "")
            .split(separator: ";").first.map { $0.trimmingCharacters(in: .whitespaces).lowercased() } ?? ""
        guard let ext = Self.fileExtension(forContentType: type) else { throw LarkError.badResponse }
        let fm = FileManager.default
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let dest = dir.appendingPathComponent("\(trackID).\(ext)")
        if fm.fileExists(atPath: dest.path) { try fm.removeItem(at: dest) }
        try fm.moveItem(at: tmp, to: dest)
        return dest
    }

    /// At most the first 4 KB: enough for a JSON error, never a whole failed download.
    private static func head(of file: URL) -> Data? {
        guard let h = try? FileHandle(forReadingFrom: file) else { return nil }
        defer { try? h.close() }
        return try? h.read(upToCount: 4096)
    }

    static func fileExtension(forContentType t: String) -> String? {
        switch t {
        case "audio/mp4", "audio/x-m4a", "audio/m4a": return "m4a"
        case "audio/mpeg", "audio/mp3": return "mp3"
        case "audio/flac", "audio/x-flac": return "flac"
        case "audio/aac": return "aac"
        case "audio/wav", "audio/x-wav", "audio/wave": return "wav"
        case "audio/ogg", "audio/opus": return "ogg"
        default: return t.hasPrefix("audio/") ? "audio" : nil
        }
    }
}
