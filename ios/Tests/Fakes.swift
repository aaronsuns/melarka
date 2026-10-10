import XCTest
import MediaPlayer
@testable import Lark

// MARK: - Backend

/// Records what the engine asks of the player; the test drives the delegate (`start`, `finish`, `fail`, ticks).
/// Every callback carries the generation of the last load unless the test passes an older one.
@MainActor final class FakeBackend: MediaBackend {
    weak var delegate: MediaBackendDelegate?
    var loads: [(MediaSource, Int, Bool)] = []
    var preloads: [MediaSource?] = []
    /// The loudness gain of each load and preload, in the same order as `loads` and `preloads`.
    var gains: [Float] = []
    var preloadGains: [Float] = []
    /// `setGain` calls (the current item's gain changed in place) and `setVolume` calls (the master volume).
    var currentGains: [Float] = []
    var nextGains: [Float] = []
    var volumes: [Float] = []
    var calls: [String] = []
    var positionMs = 0
    var durationMs: Int?
    var isPlaying = false
    var rate: Double = 1
    private(set) var generation = 0

    func load(_ s: MediaSource, startMs: Int, autoplay: Bool, rate: Double, gain: Float, generation: Int) {
        loads.append((s, startMs, autoplay)); positionMs = startMs; isPlaying = false; self.rate = rate
        gains.append(gain)
        self.generation = generation
    }
    func preload(_ s: MediaSource?, gain: Float) { preloads.append(s); preloadGains.append(gain) }
    func setGain(_ g: Float, next: Float) { currentGains.append(g); nextGains.append(next) }
    func setVolume(_ v: Float) { volumes.append(v) }
    func play() { calls.append("play"); isPlaying = true }
    func pause() { calls.append("pause"); isPlaying = false }
    func seek(ms: Int) { calls.append("seek:\(ms)"); positionMs = ms }
    func setRate(_ r: Double) { calls.append("rate:\(r)"); rate = r }
    func stop() { calls.append("stop"); isPlaying = false; positionMs = 0 }
    private(set) var rebuilds = 0
    func rebuild() { rebuilds += 1; isPlaying = false }

    func start(generation g: Int? = nil) { isPlaying = true; delegate?.backendStarted(generation: g ?? generation) }
    func finish(generation g: Int? = nil) { isPlaying = false; delegate?.backendFinished(generation: g ?? generation) }
    func fail(network: Bool, generation g: Int? = nil) {
        isPlaying = false; delegate?.backendFailed(network: network, generation: g ?? generation)
    }
    /// A pause the engine did not ask for (an interruption).
    func interrupt(generation g: Int? = nil) { isPlaying = false; delegate?.backendPaused(generation: g ?? generation) }
    func buffering(_ on: Bool, generation g: Int? = nil) { delegate?.backendBuffering(on, generation: g ?? generation) }
    /// Plays on to `ms` in 500 ms ticks, as the periodic time observer would.
    func play(to ms: Int) {
        while positionMs + 500 <= ms { positionMs += 500; delegate?.backendTick(positionMs: positionMs, generation: generation) }
    }
}

// MARK: - API

@MainActor final class FakeAPI: LarkAPIProtocol {
    struct ProgressCall: Equatable { let id: String; let positionS: Int; let played: Bool }
    struct SaveCall: Equatable { let trackIDs: [Int]; let index: Int; let positionMs: Int }
    struct ExcludeCall: Equatable { let n: Int; let exclude: [Int] }

    var token: String? = "T1"
    let base = URL(string: "https://lark.test")!

    // Canned answers
    var randomFavoritesAnswer: (source: String, tracks: [(Track, JSONValue)]) = ("favorites", [])
    var randomTracksAnswer: [(Track, JSONValue)] = []
    var radioAnswer: [(Track, JSONValue)] = []
    var queueAnswer: (ServerQueue, [(Track, JSONValue)]) =
        (ServerQueue(track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0), [])
    var saveQueueAnswer = ServerQueue(track_ids: [], current_index: 0, position_ms: 0, version: 1, updated_by: "device:1", updated_at: 0)
    var postEventsError: Error?
    var accepted: Int?                       // nil: all of them
    var refillError: Error?

    // Recorded calls
    var progressCalls: [ProgressCall] = []
    var saveCalls: [SaveCall] = []
    var postedEvents: [[PlayEvent]] = []
    var randomFavoritesCalls: [ExcludeCall] = []
    var randomTracksCalls: [ExcludeCall] = []
    var radioCalls: [ExcludeCall] = []
    var queueCalls = 0

    var favoritesAnswer: [(Track, JSONValue)] = []
    var favoritesError: Error?
    var favoritesCalls = 0
    func favorites() async throws -> [(Track, JSONValue)] {
        favoritesCalls += 1
        if let favoritesError { throw favoritesError }
        return favoritesAnswer
    }
    func randomFavorites(n: Int, exclude: [Int]) async throws -> (source: String, tracks: [(Track, JSONValue)]) {
        randomFavoritesCalls.append(.init(n: n, exclude: exclude))
        if let refillError { throw refillError }
        return randomFavoritesAnswer
    }
    func randomTracks(n: Int, exclude: [Int]) async throws -> [(Track, JSONValue)] {
        randomTracksCalls.append(.init(n: n, exclude: exclude))
        if let refillError { throw refillError }
        return randomTracksAnswer
    }
    func radio(n: Int, exclude: [Int]) async throws -> [(Track, JSONValue)] {
        radioCalls.append(.init(n: n, exclude: exclude))
        if let refillError { throw refillError }
        return radioAnswer
    }
    func queue() async throws -> (ServerQueue, [(Track, JSONValue)]) { queueCalls += 1; return queueAnswer }
    func saveQueue(trackIDs: [Int], index: Int, positionMs: Int) async throws -> ServerQueue? {
        saveCalls.append(.init(trackIDs: trackIDs, index: index, positionMs: positionMs))
        return saveQueueAnswer
    }
    func postEvents(_ e: [PlayEvent]) async throws -> Int {
        postedEvents.append(e)
        if let postEventsError { throw postEventsError }
        return accepted ?? e.count
    }
    func episodeProgress(_ id: String, positionS: Int, played: Bool) async throws {
        progressCalls.append(.init(id: id, positionS: positionS, played: played))
    }
    var lyricsAnswers: [Int: LyricsDoc] = [:]
    var lyricsCalls: [Int] = []
    func lyrics(_ trackID: Int) async throws -> LyricsDoc {
        lyricsCalls.append(trackID)
        guard let d = lyricsAnswers[trackID] else { throw LarkError.badResponse }
        return d
    }
    func artwork(_ item: Item) async throws -> Data { Data() }
    // Downloads: each writes `downloadBytes[id] ?? 100` bytes to `<dir>/<id>.m4a`, as LarkAPI does.
    var downloadCalls: [Int] = []
    var downloadQualities: [String] = []
    var downloadBytes: [Int: Int] = [:]
    var downloadErrors: [Int: Error] = [:]
    /// While true, downloads wait in `held` until `releaseDownloads()`.
    var holdDownloads = false
    private var held: [CheckedContinuation<Void, Never>] = []
    private(set) var downloading = 0
    private(set) var maxDownloading = 0
    var finishedDownloads = 0
    var heldCount: Int { held.count }
    func releaseDownloads() { holdDownloads = false; let h = held; held = []; h.forEach { $0.resume() } }
    func download(trackID: Int, quality: String, to dir: URL) async throws -> URL {
        downloadCalls.append(trackID); downloadQualities.append(quality)
        downloading += 1; maxDownloading = max(maxDownloading, downloading)
        defer { downloading -= 1; finishedDownloads += 1 }
        if holdDownloads { await withCheckedContinuation { held.append($0) } }
        if let e = downloadErrors[trackID] { throw e }
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let dest = dir.appendingPathComponent("\(trackID).m4a")
        try Data(repeating: 7, count: downloadBytes[trackID] ?? 100).write(to: dest)
        return dest
    }
    func streamURL(_ item: Item, quality: String) -> URL {
        switch item.kind {
        case .track: return URL(string: "https://lark.test/api/v1/tracks/\(item.id)/stream?quality=\(quality)")!
        case .episode: return URL(string: "https://lark.test/api/v1/episodes/\(item.id)/stream?kind=audio")!
        }
    }
}

// MARK: - Now Playing

/// Counts every write, as `MPNowPlayingInfoCenter` would receive them.
final class FakeSink: NowPlayingSink {
    private(set) var writes = 0
    var nowPlayingInfo: [String: Any]? { didSet { writes += 1 } }
    var title: String? { nowPlayingInfo?[MPMediaItemPropertyTitle] as? String }
    var artist: String? { nowPlayingInfo?[MPMediaItemPropertyArtist] as? String }
}

func lyricsDoc(lines: [(Int, String)] = [(0, "x")], offset: Int? = nil, synced: Bool = true, instrumental: Bool? = nil,
               found: Bool = true) -> LyricsDoc {
    LyricsDoc(found: found, synced: synced, instrumental: instrumental, lines: lines.map { .init(t_ms: $0.0, text: $0.1) },
              offset_ms: offset, text: nil)
}

// MARK: - Cache, network, scheduler

@MainActor final class FakeCache: CacheProviding {
    var local: [Int: URL] = [:]
    var favorites: [(Track, JSONValue)] = []
    var touched: [Int] = []
    var prefetched: [[Int]] = []
    func localURL(trackID: Int) -> URL? { local[trackID] }
    func touch(trackID: Int) { touched.append(trackID) }
    func cachedFavorites() -> [(Track, JSONValue)] { favorites }
    func prefetch(_ items: [Item]) { prefetched.append(items.compactMap(\.trackID)) }
}

@MainActor final class FakeNetwork: NetworkStatus {
    /// False: like a fresh `NWPathMonitor` before its first update (`answer` gives it one).
    var answered = true
    func answer(online: Bool = true, expensive: Bool = false) {
        answered = true
        isOnline = online; isExpensive = expensive
        observers.forEach { $0() }
    }
    var isOnline = true { didSet { observers.forEach { $0() } } }
    var isExpensive = false { didSet { observers.forEach { $0() } } }
    private(set) var observers: [@MainActor () -> Void] = []
    func observe(_ fn: @escaping @MainActor () -> Void) { observers.append(fn) }
}

/// Time moves only when the test says so.
@MainActor final class FakeScheduler: Scheduler {
    private final class Job: Cancellable {
        let at: Double; let fn: @MainActor () -> Void; var cancelled = false
        init(at: Double, fn: @escaping @MainActor () -> Void) { self.at = at; self.fn = fn }
        func cancel() { cancelled = true }
    }
    private(set) var now: Double = 0
    private var jobs: [Job] = []

    var pending: Int { jobs.filter { !$0.cancelled }.count }

    func after(_ seconds: Double, _ fn: @escaping @MainActor () -> Void) -> Cancellable {
        let j = Job(at: now + seconds, fn: fn); jobs.append(j); return j
    }

    /// Runs every job due within `seconds`, in time order (including jobs scheduled meanwhile).
    func advance(_ seconds: Double) {
        let end = now + seconds
        while let j = jobs.filter({ !$0.cancelled && $0.at <= end }).min(by: { $0.at < $1.at }) {
            jobs.removeAll { $0 === j }
            now = j.at
            j.fn()
        }
        now = end
        jobs.removeAll { $0.cancelled }
    }
}

// MARK: - Builders

func trackModel(_ id: Int, durationMs: Int = 200_000) -> (Track, JSONValue) {
    let t = Track(id: id, title: "T\(id)", artist: "A", album: "B", duration_ms: durationMs, favorite: true)
    let json: JSONValue = .object(["id": .int(id), "title": .string("T\(id)"), "artist": .string("A"), "album": .string("B"),
                                   "duration_ms": .int(durationMs), "favorite": .bool(true)])
    return (t, json)
}

func track(_ id: Int) -> Item {
    Item(kind: .track, id: String(id), title: "T\(id)", artist: "A", album: "B", durationMs: 200_000, meta: .object(["id": .int(id)]))
}

func episode(_ id: String, positionS: Int = 0, played: Bool = false, durationS: Int = 3600) -> Item {
    Item(kind: .episode, id: id, title: "E", artist: "C", album: "频道", durationMs: durationS * 1000,
         meta: .object(["video_id": .string(id), "position_s": .int(positionS), "played": .bool(played), "duration_s": .int(durationS)]))
}

func q(_ ids: [Int], index: Int, pos: Int?, play: Bool, source: QueueSource = .list) -> SetQueue {
    SetQueue(kind: .track, items: ids.map(track), index: index, positionMs: pos, play: play, source: source)
}

/// One engine wired to fakes, with a store in its own temporary directory.
@MainActor class EngineTestCase: XCTestCase {
    var backend: FakeBackend!
    var api: FakeAPI!
    var cache: FakeCache!
    var network: FakeNetwork!
    var scheduler: FakeScheduler!
    var store: QueueStore!
    var events: PlayEventQueue!
    var engine: PlayerEngine!
    var dir: URL!
    var clock = Date(timeIntervalSince1970: 1_800_000_000)
    var randomValue = 0.0
    var sent: [NativeEvent] = []
    /// Set before `super.setUp()` to give the engine a Now Playing controller.
    var nowPlaying: NowPlayingController?

    override func setUp() async throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("engine-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        backend = FakeBackend(); api = FakeAPI(); cache = FakeCache(); network = FakeNetwork(); scheduler = FakeScheduler()
        store = QueueStore(directory: dir); store.user = 1
        makeEngine()
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(at: dir)
    }

    /// A fresh engine on the same store, as after a process launch.
    func makeEngine() {
        backend = FakeBackend()
        events = PlayEventQueue(api: api, store: store, now: { [unowned self] in self.clock })
        engine = PlayerEngine(backend: backend, api: api, cache: cache, store: store, events: events, network: network,
                              now: { [unowned self] in self.clock }, random: { [unowned self] in self.randomValue },
                              scheduler: scheduler, nowPlaying: nowPlaying)
        sent = []
        engine.onEvent = { [unowned self] in self.sent.append($0) }
    }

    func tmp(_ name: String) -> URL { dir.appendingPathComponent(name) }

    func advanceClock(_ s: TimeInterval) { clock = clock.addingTimeInterval(s) }

    var states: [StateEvent] { sent.compactMap { if case .state(let s) = $0 { return s }; return nil } }
    func queues(_ kind: Item.Kind) -> [(items: [Item], index: Int, source: QueueSource)] {
        sent.compactMap { if case .queue(let k, let items, let index, let source) = $0, k == kind { return (items, index, source) }; return nil }
    }
    var notices: [String] { sent.compactMap { if case .notice(let t) = $0 { return t }; return nil } }
}
