import Foundation

/// What the offline player drives: the engine, through the same messages the web sends (`WebMessage`), so the
/// engine's rules (shuffle after the chosen song, never-stop, the cache first) apply exactly as they do for the web.
@MainActor protocol OfflinePlayerControl: AnyObject {
    func handle(_ m: WebMessage)
    /// Re-sends both queues and states (the model is seeded from them).
    func emitAll()
}

extension PlayerEngine: OfflinePlayerControl {}

/// The native offline player (the 无法连接服务器 overlay's 播放离线收藏): the cached favorites as a list, the music
/// queue, and transport and modes for what the engine is playing. Everything comes from the cache index and the
/// engine's own events (`receive`), the same `state` and `queue` events the web mirrors; nothing touches the
/// network except `retry`, which asks the app to load the web page again.
@MainActor final class OfflinePlayerModel: ObservableObject {
    /// One entry of the queue still to come, with its place in the whole queue (a tap jumps there).
    struct Upcoming: Identifiable, Equatable {
        let index: Int
        let item: Item
        var id: Int { index }
    }

    /// The cached favorites, in title order.
    @Published private(set) var favorites: [Item] = []
    /// The music queue and its current index, as the engine last said.
    @Published private(set) var queue: [Item] = []
    @Published private(set) var index = 0
    @Published private(set) var playing = false
    @Published private(set) var buffering = false
    @Published private(set) var positionMs = 0
    @Published private(set) var durationMs = 0
    @Published private(set) var shuffle = false
    @Published private(set) var repeatMode: RepeatMode = .off
    /// The engine's reason the music stopped (offline with nothing cached, …), if any.
    @Published private(set) var error: String?
    /// 重试连接 is waiting for the page.
    @Published private(set) var connecting = false
    /// The last retry failed: the server still can't be reached.
    @Published private(set) var stillOffline = false

    private let player: OfflinePlayerControl
    private let cachedFavorites: () -> [(Track, JSONValue)]
    private let retryAction: () -> Void
    private let artwork: (Int) -> URL?
    private var currentID: String?
    private var source = QueueSource.list

    /// `artwork`: a cached track's cover file (`CacheStore.artworkURL`), nil for none.
    init(player: OfflinePlayerControl, favorites: @escaping () -> [(Track, JSONValue)], retry: @escaping () -> Void,
         artwork: @escaping (Int) -> URL? = { _ in nil }) {
        self.player = player
        cachedFavorites = favorites
        retryAction = retry
        self.artwork = artwork
        reload()
    }

    /// The item's cover on the phone, nil when there is none (the view draws a tile instead).
    func artworkURL(_ item: Item) -> URL? { item.trackID.flatMap(artwork) }

    /// The song the engine is on (paused or playing), nil with an empty music queue.
    var current: Item? {
        guard let currentID, queue.indices.contains(index), queue[index].id == currentID else { return nil }
        return queue[index]
    }

    /// What follows the current song, in the engine's order.
    var upNext: [Upcoming] {
        guard index + 1 < queue.count else { return [] }
        return (index + 1..<queue.count).map { Upcoming(index: $0, item: queue[$0]) }
    }

    /// Reads the cache index again (cheap: no network, no waiting).
    func reload() {
        favorites = cachedFavorites().map { Item(track: $0.0, json: $0.1) }.sorted { a, b in
            switch a.title.localizedStandardCompare(b.title) {
            case .orderedAscending: return true
            case .orderedDescending: return false
            case .orderedSame: return a.id < b.id
            }
        }
    }

    // MARK: Engine events

    /// The engine's events (forwarded by `AppServices.send`): music only; episodes are left to the web.
    func receive(_ event: NativeEvent) {
        switch event {
        case .state(let s) where s.kind == .track:
            currentID = s.itemId
            index = s.index
            playing = s.playing
            buffering = s.buffering
            positionMs = max(0, s.positionMs)
            durationMs = max(0, s.durationMs)
            shuffle = s.shuffle
            repeatMode = s.repeatMode
            error = s.error
        case .queue(let kind, let items, let index, let source) where kind == .track:
            queue = items
            self.index = index
            self.source = source
            if items.indices.contains(index) { currentID = items[index].id } else { currentID = nil }
        default:
            break
        }
    }

    // MARK: Actions

    /// A tap in the favorites list: the list becomes the queue, from that song (the web's `playList`).
    func play(_ item: Item) {
        guard let i = favorites.firstIndex(where: { $0.id == item.id }) else { return }
        player.handle(.setQueue(SetQueue(kind: .track, items: favorites, index: i, positionMs: 0, play: true, source: .favorites)))
    }

    /// A tap in 播放队列: that entry of the same queue plays (the web's `jump`).
    func jump(to i: Int) {
        guard queue.indices.contains(i) else { return }
        player.handle(.setQueue(SetQueue(kind: .track, items: queue, index: i, positionMs: 0, play: true, source: source)))
    }

    func togglePlay() { player.handle(playing ? .pause(.track) : .play(.track)) }
    func next() { player.handle(.next(.track)) }
    func previous() { player.handle(.prev(.track)) }
    func seek(to ms: Int) { player.handle(.seek(.track, ms: max(0, ms))) }

    func toggleShuffle() { player.handle(.setModes(shuffle: !shuffle, repeatMode: repeatMode)) }

    /// off → all → one → off, as the web's button.
    func cycleRepeat() {
        let next: RepeatMode
        switch repeatMode {
        case .off: next = .all
        case .all: next = .one
        case .one: next = .off
        }
        player.handle(.setModes(shuffle: shuffle, repeatMode: next))
    }

    // MARK: 重试连接

    /// Asks the app to load the web page again; the app closes this player once the page has loaded.
    func retry() {
        retryStarted()
        retryAction()
    }

    /// A load of the page began (this player's button, or the app's own retry when the network came back).
    func retryStarted() {
        connecting = true
        stillOffline = false
    }

    /// The page failed again.
    func retryFailed() {
        connecting = false
        stillOffline = true
    }

    // MARK: Formatting

    /// "1:05", "1:02:05".
    nonisolated static func time(_ ms: Int) -> String {
        let s = max(0, ms) / 1000
        let h = s / 3600, m = (s % 3600) / 60, sec = s % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, sec) : String(format: "%d:%02d", m, sec)
    }
}
