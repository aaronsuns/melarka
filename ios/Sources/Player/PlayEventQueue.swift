import Foundation

/// Play events waiting for `POST /events/play`, like the web's `EventBuffer`: each gets a `client_event_id`
/// (UUID) once, so a retry of the same event is deduped by the server. Pending events live in
/// `events-<userId>.json`, so a failed POST or a relaunch loses nothing.
@MainActor final class PlayEventQueue {
    static let flushEvery: TimeInterval = 30
    /// Weeks offline must not grow the file without bound: past this, the oldest events are dropped.
    static let maxPending = 2000
    private let api: LarkAPIProtocol
    private let store: QueueStore
    private let now: () -> Date
    private var memory: [PlayEvent] = []          // no user: kept in memory only
    private var inFlight: Task<Void, Never>?
    private var lastFlush: Date?
    let tasks = TaskBag()

    init(api: LarkAPIProtocol, store: QueueStore, now: @escaping () -> Date = Date.init) {
        self.api = api; self.store = store; self.now = now
    }

    var pending: [PlayEvent] { store.user == nil ? memory : store.loadEvents() }

    private func save(_ e: [PlayEvent]) {
        if store.user == nil { memory = e } else { store.saveEvents(e) }
    }

    /// Records one listen and starts sending it.
    func add(trackId: Int, startedAt: Int, playedSeconds: Int, skipped: Bool, quality: String) {
        var all = pending + [PlayEvent(client_event_id: UUID().uuidString.lowercased(), track_id: trackId, started_at: startedAt,
                                       played_seconds: playedSeconds, skipped: skipped, quality: quality)]
        if all.count > Self.maxPending { all.removeFirst(all.count - Self.maxPending) }   // the oldest go
        save(all)
        tasks.run { [weak self] in await self?.flush() }
    }

    /// Sends everything pending. A flush already in flight is waited for, then whatever is left goes too.
    /// The server may accept fewer than it was sent (unknown tracks are skipped): those are dropped, since
    /// sending them again would be skipped again. A failed POST keeps everything for the next flush.
    func flush() async {
        while let t = inFlight { await t.value }   // the task clears `inFlight` itself before it finishes
        let batch = pending
        lastFlush = now()
        guard !batch.isEmpty else { return }
        let t = Task { @MainActor [weak self, api] in
            var posted = false
            do { _ = try await api.postEvents(batch); posted = true } catch {
                // kept for the next flush; the server dedupes by client_event_id
            }
            guard let self else { return }
            if posted {
                let sent = Set(batch.map(\.client_event_id))
                self.save(self.pending.filter { !sent.contains($0.client_event_id) })
            }
            self.inFlight = nil
        }
        inFlight = t
        await t.value
    }

    /// Called from the engine's 10 s tick: a flush every 30 s while anything is pending.
    func flushIfDue() {
        guard !pending.isEmpty, lastFlush.map({ now().timeIntervalSince($0) >= Self.flushEvery }) ?? true else { return }
        tasks.run { [weak self] in await self?.flush() }
    }

    /// Sign-out: forget every pending event of this user, on disk too.
    func clear() {
        memory = []
        store.saveEvents([])
    }
}
