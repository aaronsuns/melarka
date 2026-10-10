import Foundation
import os

/// At most `limit` holders at once; the others wait in arrival order.
actor AsyncSemaphore {
    private var free: Int
    private var waiters: [CheckedContinuation<Void, Never>] = []

    init(_ limit: Int) { free = limit }

    func wait() async {
        if free > 0 { free -= 1; return }
        await withCheckedContinuation { waiters.append($0) }
    }

    func signal() {
        if waiters.isEmpty { free += 1 } else { waiters.removeFirst().resume() }
    }
}

/// One user's offline cache: `Library/Caches/lark/<userId>/`.
///
/// - `files/<id>.<ext>`: the audio, always the `high` tier (AAC 256k, the web's `OFFLINE_QUALITY`).
/// - `staging/`: downloads in progress (`LarkAPI.download` moves its temp file here), moved into `files/`
///   by `store` — a rename on the same volume, so a file in `files/` is always complete.
/// - `index.json`: `trackID → {file, bytes, lastPlayed, favorite, track, meta}`, written atomically. `file` is
///   relative to the root, because the app container's path changes across updates. Writes are coalesced:
///   at most one per `indexWriteDelay` (a 2,000-entry index is about 1 MB), and `flush()` writes at once
///   (backgrounding, termination, `close`, the end of a background run). A crash loses at most that much
///   recency; a file stored but not yet indexed is an orphan, deleted at the next launch.
///
/// LRU with favorites last: over the cap, the least recently played non-favorite goes first, then the least
/// recently played favorite. Everything is excluded from backup. iOS may purge `Library/Caches` at any time,
/// index included: a file that is gone is dropped from the index when noticed (`localURL`, `cachedFavorites`,
/// launch), and the directories are made again on the next store.
///
/// Disk space: a download starts only while the volume keeps `reserveBytes` free beside it, and an
/// out-of-space error stops the running sync (`outOfSpace`), so a full phone is not fed download after download.
@MainActor final class CacheStore: CacheProviding {
    struct Entry: Codable, Equatable {
        var file: String
        var bytes: Int64
        var lastPlayed: Double          // seconds since 1970
        var favorite: Bool
        var track: Track
        var meta: JSONValue
    }

    static let quality = "high"
    static let maxConcurrentDownloads = 2
    nonisolated static let defaultCap: Int64 = 2 << 30
    /// The index is written at most this often (seconds); `flush()` writes at once.
    nonisolated static let indexWriteDelay: TimeInterval = 5
    /// Free space a download must leave on the volume.
    static let reserveBytes: Int64 = 500 << 20
    /// AAC 256k: bytes per second of audio, to estimate a download before it is made.
    static let bytesPerSecond: Int64 = 32_000
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "cache")

    /// `Library/Caches/lark`; a user's cache is the `<userId>` folder in it.
    static func defaultRoot() -> URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("lark", isDirectory: true)
    }

    let root: URL
    let api: LarkAPIProtocol
    private let network: NetworkStatus
    private let now: () -> Date
    private let fm = FileManager.default
    private var entries: [Int: Entry] = [:]
    /// The server's favorites as last seen (`setFavorites`); nil until then, when the track's own flag counts.
    private var knownFavorites: Set<Int>?
    /// Downloads queued or running, one per track.
    private var inFlight: [Int: Task<Void, Never>] = [:]
    /// Downloads holding a slot right now.
    private(set) var activeDownloads = 0
    /// Bumped by `clear` and `close`: a download started before it is dropped when it finishes.
    private var epoch = 0
    private var closed = false
    private var dirty = false
    private var pendingWrite: Task<Void, Never>?
    private let indexDelay: TimeInterval
    /// Index files written (tests).
    private(set) var indexWrites = 0
    /// An out-of-space error since the last `resetOutOfSpace()`: the sync starts nothing more.
    private(set) var outOfSpace = false
    /// Free bytes on the cache's volume for opportunistic use; nil when unknown (then only the cap counts).
    var freeBytes: () -> Int64? = CacheStore.volumeFreeBytes
    private let slots = AsyncSemaphore(CacheStore.maxConcurrentDownloads)

    /// Settable from Settings; setting it evicts down to it at once.
    var capBytes: Int64 { didSet { evict() } }
    var usedBytes: Int64 { entries.values.reduce(0) { $0 + $1.bytes } }
    var favoriteBytes: Int64 { entries.values.reduce(0) { $0 + ($1.favorite ? $1.bytes : 0) } }
    var isDownloading: Bool { !inFlight.isEmpty }

    private var indexURL: URL { root.appendingPathComponent("index.json") }
    private var filesDir: URL { root.appendingPathComponent("files", isDirectory: true) }
    private var stagingDir: URL { root.appendingPathComponent("staging", isDirectory: true) }
    /// `covers/<id>`: a cached track's cover (the offline player's artwork); an empty file marks a track with none.
    private var coversDir: URL { root.appendingPathComponent("covers", isDirectory: true) }
    private func coverURL(_ id: Int) -> URL { coversDir.appendingPathComponent(String(id)) }
    /// A cover bigger than this is not kept (the server's are 300 px).
    static let maxCoverBytes = 2 << 20

    init(root: URL, api: LarkAPIProtocol, network: NetworkStatus, capBytes: Int64 = CacheStore.defaultCap,
         now: @escaping () -> Date = Date.init, indexDelay: TimeInterval = CacheStore.indexWriteDelay) {
        self.root = root; self.api = api; self.network = network; self.capBytes = capBytes; self.now = now
        self.indexDelay = indexDelay
        load()
    }

    // MARK: CacheProviding

    func localURL(trackID id: Int) -> URL? {
        guard let e = entries[id] else { return nil }
        let url = root.appendingPathComponent(e.file)
        guard fm.fileExists(atPath: url.path) else {
            Self.log.info("cached file of track \(id) is gone; dropped")
            entries[id] = nil
            removeCover(id)
            markDirty()
            return nil
        }
        return url
    }

    func touch(trackID id: Int) {
        guard entries[id] != nil else { return }
        entries[id]?.lastPlayed = now().timeIntervalSince1970
        markDirty()
    }

    func cachedFavorites() -> [(Track, JSONValue)] {
        var gone: [Int] = []
        var out: [(Track, JSONValue)] = []
        for (id, e) in entries.sorted(by: { $0.key < $1.key }) where e.favorite {
            if fm.fileExists(atPath: root.appendingPathComponent(e.file).path) { out.append((e.track, e.meta)) } else { gone.append(id) }
        }
        if !gone.isEmpty {
            gone.forEach { entries[$0] = nil; removeCover($0) }
            markDirty()
        }
        return out
    }

    /// The lookahead. Any network will do (cellular too: it is 2 tracks); none while offline or signed out.
    func prefetch(_ items: [Item]) {
        guard !closed, network.isOnline, api.token != nil else { return }
        for item in items {
            guard let id = item.trackID else { continue }
            let track = Track(id: id, title: item.title, artist: item.artist, album: item.album, duration_ms: item.durationMs,
                              favorite: item.meta["favorite"] == .bool(true))
            download(track, meta: item.meta)
        }
    }

    // MARK: Store, flags, eviction

    /// Moves `file` into the cache as this track's audio (replacing an older copy), then evicts down to the cap;
    /// the track just stored is the last to go.
    func store(trackID id: Int, file: URL, track: Track, meta: JSONValue, favorite: Bool) throws {
        try fm.createDirectory(at: filesDir, withIntermediateDirectories: true)
        excludeFromBackup(root)
        let ext = file.pathExtension.isEmpty ? "audio" : file.pathExtension
        let name = "files/\(id).\(ext)"
        let dest = root.appendingPathComponent(name)
        if let old = entries[id], old.file != name { try? fm.removeItem(at: root.appendingPathComponent(old.file)) }
        if fm.fileExists(atPath: dest.path) {
            _ = try fm.replaceItemAt(dest, withItemAt: file)
        } else {
            try fm.moveItem(at: file, to: dest)
        }
        excludeFromBackup(dest)
        let bytes = ((try? fm.attributesOfItem(atPath: dest.path))?[.size] as? NSNumber)?.int64Value ?? 0
        var (t, m) = (track, meta)
        Self.flag(&t, &m, favorite)
        entries[id] = Entry(file: name, bytes: bytes, lastPlayed: now().timeIntervalSince1970, favorite: favorite, track: t, meta: m)
        evict(keeping: id, save: false)
        markDirty()
    }

    /// The server's favorites: flags only. An unfavorited file stays, as an ordinary LRU entry.
    func setFavorites(_ ids: Set<Int>) {
        knownFavorites = ids
        var changed = false
        for id in entries.keys {
            let on = ids.contains(id)
            if entries[id]?.favorite != on { setFlag(id, on); changed = true }
        }
        if changed { markDirty() }
    }

    /// One favorite toggled in the web: flagged at once, before any sync (an offline shuffle respects it).
    func markFavorite(_ id: Int, _ on: Bool) {
        if on { knownFavorites?.insert(id) } else { knownFavorites?.remove(id) }
        guard let e = entries[id], e.favorite != on else { return }
        setFlag(id, on)
        markDirty()
    }

    /// Newer metadata for tracks already cached (a favorites list): the index keeps what the web would get.
    func refresh(_ tracks: [(Track, JSONValue)]) {
        var changed = false
        for (t, m) in tracks {
            guard var e = entries[t.id] else { continue }
            var (nt, nm) = (t, m)
            Self.flag(&nt, &nm, e.favorite)
            if e.track != nt || e.meta != nm { e.track = nt; e.meta = nm; entries[t.id] = e; changed = true }
        }
        if changed { markDirty() }
    }

    /// Deletes every file and the index; downloads in flight are dropped when they finish.
    func clear() {
        dropDownloads()
        entries = [:]
        pendingWrite?.cancel(); pendingWrite = nil; dirty = false
        for u in [filesDir, stagingDir, coversDir, indexURL] { try? fm.removeItem(at: u) }
    }

    /// Sign-out or another user: this cache stays on disk for its user's next sign-in, and does nothing more.
    func close() {
        dropDownloads()
        flush()
        closed = true
    }

    /// Writes the index now if anything changed since the last write.
    func flush() {
        pendingWrite?.cancel(); pendingWrite = nil
        guard dirty else { return }
        dirty = false
        writeIndex()
    }

    /// Room on the volume for `bytes` more, keeping `reserveBytes` free. Unknown free space only counts the cap.
    func hasRoom(for bytes: Int64) -> Bool {
        guard let free = freeBytes() else { return true }
        return free - bytes >= Self.reserveBytes
    }

    func resetOutOfSpace() { outOfSpace = false }

    nonisolated static func volumeFreeBytes() -> Int64? {
        let home = URL(fileURLWithPath: NSHomeDirectory(), isDirectory: true)
        return (try? home.resourceValues(forKeys: [.volumeAvailableCapacityForOpportunisticUsageKey]))?
            .volumeAvailableCapacityForOpportunisticUsage
    }

    /// ENOSPC in any of its forms, also as an underlying error.
    nonisolated static func isOutOfSpace(_ error: Error) -> Bool {
        var e: NSError? = error as NSError
        while let n = e {
            switch (n.domain, n.code) {
            case (NSCocoaErrorDomain, NSFileWriteOutOfSpaceError), (NSURLErrorDomain, NSURLErrorCannotWriteToFile),
                 (NSPOSIXErrorDomain, Int(ENOSPC)):
                return true
            default:
                e = n.userInfo[NSUnderlyingErrorKey] as? NSError
            }
        }
        return false
    }

    private func dropDownloads() {
        epoch += 1
        inFlight.values.forEach { $0.cancel() }
        inFlight = [:]
    }

    private func setFlag(_ id: Int, _ on: Bool) {
        guard var e = entries[id] else { return }
        e.favorite = on
        Self.flag(&e.track, &e.meta, on)
        entries[id] = e
    }

    /// The track's and the raw JSON's `favorite` follow the flag, so a cached favorite reaches the web as one.
    private static func flag(_ t: inout Track, _ m: inout JSONValue, _ on: Bool) {
        if t.favorite != on { t = Track(id: t.id, title: t.title, artist: t.artist, album: t.album, duration_ms: t.duration_ms, favorite: on) }
        if case .object = m, m["favorite"] != .bool(on) { m = m.setting("favorite", .bool(on)) }
    }

    private func evict(keeping keep: Int? = nil, save: Bool = true) {
        var used = usedBytes
        guard used > capBytes else { return }
        let order = entries.filter { $0.key != keep }.sorted { a, b in
            if a.value.favorite != b.value.favorite { return !a.value.favorite }
            if a.value.lastPlayed != b.value.lastPlayed { return a.value.lastPlayed < b.value.lastPlayed }
            return a.key < b.key
        }
        for (id, e) in order {
            guard used > capBytes else { break }
            try? fm.removeItem(at: root.appendingPathComponent(e.file))
            removeCover(id)
            entries[id] = nil
            used -= e.bytes
        }
        if save { markDirty() }
    }

    // MARK: Downloads

    /// Fetches one track into the cache unless it is there or already coming. At most 2 run at once; the rest
    /// wait for a slot, and `shouldStart` is asked when the slot is theirs (the sync's network and cap checks).
    /// Returns the download's task and whether this call created it (`false`: another caller's download of the
    /// same track, which only its owner may cancel); nil when there is nothing to do.
    @discardableResult
    func download(_ track: Track, meta: JSONValue, favorite: Bool? = nil,
                  shouldStart: @escaping @MainActor () -> Bool = { true }) -> (task: Task<Void, Never>, created: Bool)? {
        let id = track.id
        if let running = inFlight[id] { return (running, false) }
        guard !closed, localURL(trackID: id) == nil else { return nil }
        let e = epoch
        let slots = self.slots
        let task = Task { @MainActor [weak self] in
            await slots.wait()
            await self?.fetch(track, meta: meta, favorite: favorite, epoch: e, shouldStart: shouldStart)
            await slots.signal()
            if let self, self.epoch == e { self.inFlight[id] = nil }
        }
        inFlight[id] = task
        return (task, true)
    }

    private func fetch(_ track: Track, meta: JSONValue, favorite: Bool?, epoch e: Int, shouldStart: @MainActor () -> Bool) async {
        guard !Task.isCancelled, epoch == e, network.isOnline, shouldStart(), hasRoom(for: Self.estimatedBytes(track)),
              localURL(trackID: track.id) == nil else { return }
        activeDownloads += 1
        defer { activeDownloads -= 1 }
        do {
            let file = try await api.download(trackID: track.id, quality: Self.quality, to: stagingDir)
            // A finished download is kept even if its caller gave up meanwhile; not after a clear or a sign-out.
            guard epoch == e else { try? fm.removeItem(at: file); return }
            let fav = knownFavorites.map { $0.contains(track.id) } ?? favorite ?? track.favorite
            try store(trackID: track.id, file: file, track: track, meta: meta, favorite: fav)
        } catch {
            if Self.isOutOfSpace(error) { outOfSpace = true }
            Self.log.error("download of track \(track.id) failed: \(String(describing: error), privacy: .public)")
            return
        }
        await cacheCover(trackID: track.id)
    }

    // MARK: Covers

    /// A cached track's cover on disk; nil when the track is not cached, its cover was not fetched yet, or it has none.
    func artworkURL(trackID id: Int) -> URL? {
        guard entries[id] != nil else { return nil }
        let url = coverURL(id)
        guard let size = (try? fm.attributesOfItem(atPath: url.path))?[.size] as? NSNumber, size.int64Value > 0 else { return nil }
        return url
    }

    /// Fetches a cached track's cover once (after its download; the favorites sync for tracks cached before covers
    /// were kept). Best effort: a network error leaves it for the next time; a track with no cover (404) gets an
    /// empty marker, so it is not asked again. Covers are small and do not count against the cap.
    func cacheCover(trackID id: Int) async {
        guard !closed, let e = entries[id], !fm.fileExists(atPath: coverURL(id).path) else { return }
        let ep = epoch
        var data: Data
        do {
            data = try await api.artwork(Item(track: e.track, json: e.meta))
        } catch LarkError.http(status: 404, _) {
            data = Data()
        } catch {
            return
        }
        guard epoch == ep, !closed, entries[id] != nil else { return }
        if data.count > Self.maxCoverBytes { data = Data() }
        do {
            try fm.createDirectory(at: coversDir, withIntermediateDirectories: true)
            excludeFromBackup(coversDir)
            try data.write(to: coverURL(id), options: .atomic)
        } catch {
            if Self.isOutOfSpace(error) { outOfSpace = true }
        }
    }

    /// Cached tracks among `ids` whose cover was never fetched.
    func missingCovers(_ ids: [Int]) -> [Int] {
        ids.filter { entries[$0] != nil && !fm.fileExists(atPath: coverURL($0).path) }
    }

    private func removeCover(_ id: Int) { try? fm.removeItem(at: coverURL(id)) }

    /// A track's size before it is fetched: its duration at AAC 256k, at least a minute's worth.
    static func estimatedBytes(_ t: Track) -> Int64 {
        max(60, Int64(t.duration_ms / 1000)) * bytesPerSecond
    }

    // MARK: Disk

    private func load() {
        defer {
            try? fm.removeItem(at: stagingDir)       // partial downloads of an earlier run
            removeOrphans()
        }
        guard let data = try? Data(contentsOf: indexURL) else { return }
        guard let map = try? JSONDecoder().decode([String: Entry].self, from: data) else {
            Self.log.error("cache index unreadable; starting empty")
            return
        }
        var dropped = false
        for (key, e) in map {
            guard let id = Int(key), e.file.hasPrefix("files/"), !e.file.contains(".."),
                  fm.fileExists(atPath: root.appendingPathComponent(e.file).path) else { dropped = true; continue }
            entries[id] = e
        }
        if dropped { markDirty() }
        evict()
    }

    /// Files no index entry accounts for (the index was purged or unreadable) would count against nothing.
    private func removeOrphans() {
        if let names = try? fm.contentsOfDirectory(atPath: filesDir.path) {
            let known = Set(entries.values.map(\.file))
            for n in names where !known.contains("files/\(n)") { try? fm.removeItem(at: filesDir.appendingPathComponent(n)) }
        }
        if let names = try? fm.contentsOfDirectory(atPath: coversDir.path) {
            for n in names where Int(n).map({ entries[$0] == nil }) ?? true { try? fm.removeItem(at: coversDir.appendingPathComponent(n)) }
        }
    }

    /// Something changed: the index is written once `indexDelay` has passed (or at the next `flush`).
    private func markDirty() {
        guard !closed else { return }
        dirty = true
        guard pendingWrite == nil else { return }
        let delay = indexDelay
        pendingWrite = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            guard !Task.isCancelled, let self else { return }
            self.pendingWrite = nil
            self.flush()
        }
    }

    private func writeIndex() {
        indexWrites += 1
        do {
            try fm.createDirectory(at: root, withIntermediateDirectories: true)
            excludeFromBackup(root)
            let enc = JSONEncoder(); enc.outputFormatting = [.sortedKeys]
            let data = try enc.encode(Dictionary(uniqueKeysWithValues: entries.map { (String($0.key), $0.value) }))
            try data.write(to: indexURL, options: .atomic)
        } catch {
            Self.log.error("cache index not saved: \(String(describing: error), privacy: .public)")
        }
    }

    private func excludeFromBackup(_ url: URL) {
        var u = url
        var v = URLResourceValues(); v.isExcludedFromBackup = true
        try? u.setResourceValues(v)
    }
}

/// The cache the engine holds for the app's life: the signed-in user's `CacheStore`, nothing while signed out.
@MainActor final class UserCache: CacheProviding {
    private(set) var store: CacheStore?

    /// Replaces the store; the old one is closed (its files stay for its user).
    func set(_ s: CacheStore?) {
        guard s !== store else { return }
        store?.close()
        store = s
    }

    func localURL(trackID: Int) -> URL? { store?.localURL(trackID: trackID) }
    func touch(trackID: Int) { store?.touch(trackID: trackID) }
    func cachedFavorites() -> [(Track, JSONValue)] { store?.cachedFavorites() ?? [] }
    func prefetch(_ items: [Item]) { store?.prefetch(items) }
}
