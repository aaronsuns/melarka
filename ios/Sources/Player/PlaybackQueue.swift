import Foundation

/// One kind's queue: music or 频道 episodes. The engine keeps one of each.
struct PlaybackQueue: Codable, Equatable {
    var items: [Item]
    var index: Int
    var positionMs: Int
    var source: QueueSource

    static let empty = PlaybackQueue(items: [], index: 0, positionMs: 0, source: .list)

    var current: Item? { items.indices.contains(index) ? items[index] : nil }
    /// The items after the current one.
    var upcoming: ArraySlice<Item> { index + 1 < items.count ? items[(index + 1)...] : [] }
}

extension Item {
    /// A music item from the API's track and its raw JSON (kept as `meta`, echoed to the web unchanged).
    init(track t: Track, json: JSONValue) {
        self.init(kind: .track, id: String(t.id), title: t.title, artist: t.artist, album: t.album, durationMs: t.duration_ms, meta: json)
    }

    /// The track id, for a music item.
    var trackID: Int? { kind == .track ? Int(id) : nil }
}
