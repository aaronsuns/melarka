import MediaPlayer
import UIKit

/// Where Now Playing info goes. `MPNowPlayingInfoCenter.default()` in the app; a counting fake in tests.
protocol NowPlayingSink: AnyObject { var nowPlayingInfo: [String: Any]? { get set } }
extension MPNowPlayingInfoCenter: NowPlayingSink {}

/// The lock screen, Control Center and the car display (Bluetooth AVRCP, CarPlay's Now Playing).
/// With a car-lyrics line, the line is the title and "song · artist" the artist, as on the web.
/// Artwork is fetched once per item and kept for the last 20 items, as one `MPMediaItemArtwork` object reused on
/// every write (some head units send the cover again when the object changes). "No cover" (the closure's nil) is
/// remembered; a fetch that threw (offline, a 5xx) is asked again after 30 s, then 60 s, … up to 10 minutes.
@MainActor final class NowPlayingController {
    static let artworkCacheSize = 20
    static let artworkRetryS: Double = 30
    static let artworkRetryMaxS: Double = 600

    private let sink: NowPlayingSink
    private let artwork: (Item) async throws -> UIImage?
    /// nil: asked, and there is no cover; not asked again while it is in the cache.
    private var images: [String: MPMediaItemArtwork?] = [:]
    /// Fetches that threw: when to ask again, and the wait after the next failure.
    private var retry: [String: (at: Date, nextWait: Double)] = [:]
    private var imageOrder: [String] = []            // least recently used first
    private var fetching: Set<String> = []
    private let now: () -> Date
    /// The last write: an arriving artwork rewrites it, with the elapsed time moved on to now.
    private var last: (item: Item, positionMs: Int, durationMs: Int?, rate: Double, playing: Bool, lyricLine: String?, at: Date)?

    init(sink: NowPlayingSink, artwork: @escaping (Item) async throws -> UIImage?, now: @escaping () -> Date = Date.init) {
        self.sink = sink
        self.artwork = artwork
        self.now = now
    }

    func show(item: Item, positionMs: Int, durationMs: Int?, rate: Double, playing: Bool, lyricLine: String?) {
        last = (item, positionMs, durationMs, rate, playing, lyricLine, now())
        var info: [String: Any] = [
            MPMediaItemPropertyAlbumTitle: item.album,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: Double(max(0, positionMs)) / 1000,
            MPNowPlayingInfoPropertyPlaybackRate: playing ? rate : 0.0,      // a Double either way (not an Int 0)
            MPNowPlayingInfoPropertyDefaultPlaybackRate: rate,
            MPNowPlayingInfoPropertyMediaType: NSNumber(value: MPNowPlayingInfoMediaType.audio.rawValue),
        ]
        if let line = lyricLine {
            info[MPMediaItemPropertyTitle] = line
            info[MPMediaItemPropertyArtist] = item.artist.isEmpty ? item.title : "\(item.title) · \(item.artist)"
        } else {
            info[MPMediaItemPropertyTitle] = item.title
            info[MPMediaItemPropertyArtist] = item.artist
        }
        let duration = durationMs ?? item.durationMs
        if duration > 0 { info[MPMediaItemPropertyPlaybackDuration] = Double(duration) / 1000 }
        let key = Self.key(item)
        if let cached = images[key] {
            touch(key)
            if let art = cached { info[MPMediaItemPropertyArtwork] = art }
        } else {
            fetchArtwork(item)
        }
        sink.nowPlayingInfo = info
    }

    /// Nothing to show (stopped, signed out).
    func clear() {
        last = nil
        sink.nowPlayingInfo = nil
    }

    private static func key(_ item: Item) -> String { "\(item.kind.rawValue):\(item.id)" }

    private func fetchArtwork(_ item: Item) {
        let key = Self.key(item)
        guard !fetching.contains(key) else { return }
        if let r = retry[key], now() < r.at { return }
        fetching.insert(key)
        Task { @MainActor [weak self, artwork] in
            let image: UIImage?
            do {
                image = try await artwork(item)
            } catch {
                guard let self else { return }
                self.fetching.remove(key)
                let wait = self.retry[key]?.nextWait ?? Self.artworkRetryS
                self.retry[key] = (self.now().addingTimeInterval(wait), min(wait * 2, Self.artworkRetryMaxS))
                if self.retry.count > Self.artworkCacheSize * 5 { self.retry = self.retry.filter { $0.value.at > self.now() } }
                return
            }
            guard let self else { return }
            self.fetching.remove(key)
            self.retry[key] = nil
            self.images[key] = .some(image.map(Self.makeArtwork))
            self.touch(key)
            while self.imageOrder.count > Self.artworkCacheSize {
                self.images.removeValue(forKey: self.imageOrder.removeFirst())
            }
            if image != nil, let l = self.last, Self.key(l.item) == key {
                let moved = l.playing ? Int(self.now().timeIntervalSince(l.at) * 1000 * l.rate) : 0
                self.show(item: l.item, positionMs: l.positionMs + moved, durationMs: l.durationMs, rate: l.rate,
                          playing: l.playing, lyricLine: l.lyricLine)
            }
        }
    }

    private func touch(_ key: String) {
        imageOrder.removeAll { $0 == key }
        imageOrder.append(key)
    }

    /// Built outside the main actor: MediaPlayer calls the handler on its own queue.
    nonisolated private static func makeArtwork(_ image: UIImage) -> MPMediaItemArtwork {
        MPMediaItemArtwork(boundsSize: image.size) { _ in image }
    }
}
