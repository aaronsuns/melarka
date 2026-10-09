import Foundation
import os

/// Everything the engine persists for one user: both queues, which one is active, when they last changed
/// (`savedAt`, unix seconds) and the server's name for this device (`ownDevice`, from our own `PUT /queue`).
/// `version` is the schema: a file with another version reads as no queue (logged), never as a crash.
/// A later schema adds fields with `decodeIfPresent` defaults and bumps the version only when it must.
/// `modes` (shuffle and repeat) came later: a file without it reads as both off, still version 1.
struct QueueSnapshot: Codable, Equatable {
    static let currentVersion = 1
    var version = QueueSnapshot.currentVersion
    var music: PlaybackQueue
    var episodes: PlaybackQueue
    var active: Item.Kind
    var savedAt: Int
    var ownDevice: String?
    var modes: PlayModes?

    private enum CodingKeys: String, CodingKey { case version, music, episodes, active, savedAt, ownDevice, modes }
}

extension QueueSnapshot {
    /// A malformed `modes` (a damaged file) reads as none, so the queues are still restored.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decodeIfPresent(Int.self, forKey: .version) ?? QueueSnapshot.currentVersion
        music = try c.decode(PlaybackQueue.self, forKey: .music)
        episodes = try c.decode(PlaybackQueue.self, forKey: .episodes)
        active = try c.decode(Item.Kind.self, forKey: .active)
        savedAt = try c.decode(Int.self, forKey: .savedAt)
        ownDevice = try c.decodeIfPresent(String.self, forKey: .ownDevice)
        modes = (try? c.decodeIfPresent(PlayModes.self, forKey: .modes)) ?? nil
    }
}

/// Per-user files in one directory: `queues-<userId>.json` and `events-<userId>.json`. With no user nothing is
/// read or written. Writes are atomic; an unreadable file reads as nothing.
@MainActor final class QueueStore {
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "store")
    let directory: URL
    var user: Int?

    init(directory: URL) { self.directory = directory }

    /// The default place: Application Support/lark (backed up, not purged like Caches).
    nonisolated static func defaultDirectory() -> URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? FileManager.default.temporaryDirectory
        return base.appendingPathComponent("lark", isDirectory: true)
    }

    private func file(_ prefix: String) -> URL? {
        user.map { directory.appendingPathComponent("\(prefix)-\($0).json") }
    }

    func load() -> QueueSnapshot? {
        struct Head: Decodable { let version: Int? }
        guard let head = read(Head.self, "queues") else { return nil }
        guard head.version == QueueSnapshot.currentVersion else {
            Self.log.error("ignored a queue file with schema version \(head.version ?? -1, privacy: .public)")
            return nil
        }
        return read(QueueSnapshot.self, "queues")
    }
    func save(_ s: QueueSnapshot) { write(s, "queues") }

    func loadEvents() -> [PlayEvent] { read([PlayEvent].self, "events") ?? [] }
    func saveEvents(_ e: [PlayEvent]) {
        if e.isEmpty, let f = file("events") { try? FileManager.default.removeItem(at: f) } else { write(e, "events") }
    }

    /// Deletes the current user's queues and pending events.
    func wipe() {
        for prefix in ["queues", "events"] {
            if let f = file(prefix) { try? FileManager.default.removeItem(at: f) }
        }
    }

    private func read<T: Decodable>(_ type: T.Type, _ prefix: String) -> T? {
        guard let f = file(prefix), let data = try? Data(contentsOf: f) else { return nil }
        do { return try JSONDecoder().decode(T.self, from: data) } catch {
            Self.log.error("ignored an unreadable \(prefix, privacy: .public) file")
            return nil
        }
    }

    private func write<T: Encodable>(_ v: T, _ prefix: String) {
        guard let f = file(prefix) else { return }
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            try JSONEncoder().encode(v).write(to: f, options: [.atomic])
        } catch {
            Self.log.error("could not write the \(prefix, privacy: .public) file")
        }
    }
}
