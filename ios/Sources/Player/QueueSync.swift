import Foundation

/// `PUT /queue` for the music queue (episodes never go to the server): 1 s after the last edit (debounced),
/// and every 15 s while music plays. Also the rule for adopting the server's queue (`shouldAdopt`).
@MainActor final class QueueSync {
    static let debounce: Double = 1
    static let everyWhilePlaying: TimeInterval = 15
    /// The server's queue must be this much newer than the local one to replace it.
    static let newerBy = 5

    struct Snapshot: Equatable { let trackIDs: [Int]; let index: Int; let positionMs: Int }

    private let api: LarkAPIProtocol
    private let scheduler: Scheduler
    private let now: () -> Date
    private var debounced: Cancellable?
    private var lastPush: Date?
    private var epoch = 0
    let tasks = TaskBag()

    /// The music queue as it should be sent now.
    var snapshot: () -> Snapshot = { Snapshot(trackIDs: [], index: 0, positionMs: 0) }
    /// The server's `updated_by` for our own PUT: this device's name there.
    var onOwnDevice: ((String) -> Void)?

    init(api: LarkAPIProtocol, scheduler: Scheduler, now: @escaping () -> Date) {
        self.api = api; self.scheduler = scheduler; self.now = now
    }

    /// The music queue changed: send it once the edits stop for a second.
    func edited() {
        debounced?.cancel()
        debounced = scheduler.after(Self.debounce) { [weak self] in self?.push() }
    }

    /// Music is playing (called on the backend's ticks and the 10 s tick).
    /// The first tick only starts the clock: the edit that started playback is already being sent.
    func playingTick() {
        guard let lastPush else { lastPush = now(); return }
        if now().timeIntervalSince(lastPush) >= Self.everyWhilePlaying { push() }
    }

    func push() {
        debounced?.cancel(); debounced = nil
        lastPush = now()
        let s = snapshot(), e = epoch
        tasks.run { [weak self, api] in
            // Errors (offline, 5xx) are left for the next edit or the next 15 s tick: the queue is also on disk.
            guard let stored = try? await api.saveQueue(trackIDs: s.trackIDs, index: s.index, positionMs: s.positionMs),
                  let self, self.epoch == e else { return }
            self.onOwnDevice?(stored.updated_by)
        }
    }

    /// Sign-out: nothing more goes out for the old user.
    func cancel() {
        epoch += 1
        debounced?.cancel(); debounced = nil
        lastPush = nil
        tasks.cancelAll()
    }

    /// Adopt the server's queue? Always when the local one is empty. Otherwise only when it is newer
    /// (`updated_at` > local `savedAt` + 5 s), came from another device, and nothing is playing.
    static func shouldAdopt(_ server: ServerQueue, localEmpty: Bool, savedAt: Int, ownDevice: String?, playing: Bool) -> Bool {
        guard !playing, !server.track_ids.isEmpty else { return false }
        if localEmpty { return true }
        return server.updated_at > savedAt + newerBy && server.updated_by != ownDevice
    }
}
