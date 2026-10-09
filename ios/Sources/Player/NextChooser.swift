import Foundation

/// Which music item plays next, mirroring the web's `web/src/player/nextTrack.ts` (`chooseNext`, `pickFavorite`).
///
/// - Failed items (within their 30-minute penalty) are passed over.
/// - With `mustBeLocal` (offline, or after 3 failures in a row) only items whose bytes are on the phone count;
///   with none in the queue, a cached favorite is put after the current item. Otherwise the next item plays.
/// - A chosen item further down the queue is moved up to just after the current one (the engine does that),
///   so the items it jumped over still play later.
enum NextChooser {
    enum Choice: Equatable {
        case queue(Int)       // play queue entry i, moved to just after the current one
        case insert(Item)     // put this cached favorite after the current one and play it
        case wrap(mustBeLocal: Bool)   // repeat all: the queue starts over (from its first item that may play)
        case none
    }

    /// A cached favorite this far back in the queue (or the current one) is passed over while another is available.
    static let history = 30

    /// `wrap` (repeat all): with nothing playable after the current item, start the queue over instead of a
    /// favorite or stopping, but only when some item in it may play: not failed, and on the phone when
    /// `mustBeLocal` (offline, the failure brake). Otherwise, and without `wrap`, the result is as before
    /// (a cached favorite, or none), so a queue that can't play never loops.
    static func choose(_ q: PlaybackQueue, mustBeLocal: Bool, failed: (Item) -> Bool, isLocal: (Item) -> Bool,
                       favorites: [Item], random: () -> Double, wrap: Bool = false) -> Choice {
        let cands = q.items.indices.filter { $0 > q.index && !failed(q.items[$0]) }
        if wrap && cands.isEmpty && q.items.contains(where: { !failed($0) && (!mustBeLocal || isLocal($0)) }) {
            return .wrap(mustBeLocal: mustBeLocal)
        }
        if !mustBeLocal, let first = cands.first { return .queue(first) }
        if let j = cands.first(where: { isLocal(q.items[$0]) }) { return .queue(j) }
        if let fav = pickFavorite(q, failed: failed, favorites: favorites, random: random) { return .insert(fav) }
        return .none
    }

    static func pickFavorite(_ q: PlaybackQueue, failed: (Item) -> Bool, favorites: [Item], random: () -> Double) -> Item? {
        let cur = q.current?.id
        let lo = max(0, q.index - history), hi = min(q.items.count, q.index + 1)
        let recent = Set(lo < hi ? q.items[lo..<hi].map(\.id) : [])
        let all = favorites.filter { $0.id != cur && !failed($0) }
        let fresh = all.filter { !recent.contains($0.id) }
        let pool = fresh.isEmpty ? all : fresh
        guard !pool.isEmpty else { return nil }
        return pool[min(pool.count - 1, Int(random() * Double(pool.count)))]
    }

    /// Fisher–Yates with an injected random source.
    static func shuffled<T>(_ xs: [T], random: () -> Double) -> [T] {
        var out = xs
        var i = out.count - 1
        while i > 0 {
            let j = min(i, Int(random() * Double(i + 1)))
            out.swapAt(i, j)
            i -= 1
        }
        return out
    }
}
