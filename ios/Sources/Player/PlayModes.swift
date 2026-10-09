import Foundation

/// Repeat for the music queue: `off` is the never-stop behaviour (refill, favorites), `all` loops the queue,
/// `one` replays the current track when it ends on its own.
enum RepeatMode: String, Codable { case off, all, one }

/// Shuffle and repeat for the music queue, mirroring the web's `web/src/player/modes.ts` (same rules, same
/// fixtures in the tests). Episodes ignore them. Saved with the queues (`QueueSnapshot.modes`), per device.
///
/// - Shuffle on: everything after the current track is shuffled; `original` remembers the upcoming ids in
///   their order, so shuffle off can restore it. Tracks added while shuffled stay where the user put them.
/// - Repeat all: at the end the whole queue plays again from its first track (`newPass`), with no refill;
///   reshuffled each pass when shuffle is on.
struct PlayModes: Codable, Equatable {
    var shuffle = false
    var repeatMode: RepeatMode = .off               // "repeat" is a Swift keyword; on disk and on the wire it is "repeat"
    var original: [String]?                         // upcoming item ids when shuffle was turned on

    init(shuffle: Bool = false, repeatMode: RepeatMode = .off, original: [String]? = nil) {
        self.shuffle = shuffle; self.repeatMode = repeatMode; self.original = original
    }

    private enum CodingKeys: String, CodingKey { case shuffle, repeatMode = "repeat", original }

    /// Lenient: a missing or unknown field reads as its default (a file from another version never fails).
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        shuffle = (try? c.decodeIfPresent(Bool.self, forKey: .shuffle)) ?? false
        repeatMode = (try? c.decodeIfPresent(RepeatMode.self, forKey: .repeatMode)) ?? .off
        original = (try? c.decodeIfPresent([String].self, forKey: .original)) ?? nil
    }

    /// The current item stays put; everything after it is shuffled. Returns the new queue and the upcoming ids
    /// in their order before (`original`).
    static func shuffleUpcoming(_ q: PlaybackQueue, random: () -> Double) -> (PlaybackQueue, [String]) {
        let cut = min(q.items.count, q.index + 1)
        let head = Array(q.items[..<cut]), rest = Array(q.items[cut...])
        var out = q
        out.items = head + NextChooser.shuffled(rest, random: random)
        return (out, rest.map(\.id))
    }

    /// Restores the pre-shuffle order of the remaining items. Items added while shuffled keep where the user put
    /// them: those ahead of the first pre-shuffle item (play next) stay in front; the rest (add to queue,
    /// refills) follow, in their current order. Ids may repeat: matched as a multiset.
    static func unshuffle(_ q: PlaybackQueue, original: [String]) -> PlaybackQueue {
        let cut = min(q.items.count, q.index + 1)
        let head = Array(q.items[..<cut]), rest = Array(q.items[cut...])
        let wanted = Set(original)
        // A play-next item whose id is also among the shuffled ones counts as that one (they can't be told
        // apart), so it goes back to its original slot.
        guard let firstOriginal = rest.firstIndex(where: { wanted.contains($0.id) }) else { return q }
        let front = Array(rest[..<firstOriginal]), tail = Array(rest[firstOriginal...])
        // How many of each id are still upcoming (played and removed ones are gone).
        var left: [String: Int] = [:]
        for it in tail { left[it.id, default: 0] += 1 }
        var byID: [String: Item] = [:]
        for it in tail { byID[it.id] = it }
        var restored: [Item] = []
        for id in original {
            guard let n = left[id], n > 0, let it = byID[id] else { continue }
            left[id] = n - 1
            restored.append(it)
        }
        // Whatever wasn't matched was added while shuffled: it follows, in its order.
        var used: [String: Int] = [:]
        for it in restored { used[it.id, default: 0] += 1 }
        let added = tail.filter { it in
            let n = used[it.id] ?? 0
            if n == 0 { return true }
            used[it.id] = n - 1
            return false
        }
        var out = q
        out.items = head + front + restored + added
        return out
    }

    /// Repeat all at the end: the whole queue again from its first item; reshuffled when shuffle is on (never
    /// starting with the item that just ended, when there is another).
    static func newPass(_ q: PlaybackQueue, shuffle: Bool, random: () -> Double) -> PlaybackQueue {
        var out = q
        out.index = 0
        out.positionMs = 0
        guard shuffle else { return out }
        var items = NextChooser.shuffled(q.items, random: random)
        let ended = q.current?.id
        if items.count > 1, items[0].id == ended {
            let others = items.indices.filter { items[$0].id != ended }
            if !others.isEmpty {
                let j = others[min(others.count - 1, Int(random() * Double(others.count)))]
                items.swapAt(0, j)
            }
        }
        out.items = items
        return out
    }

    /// Shuffle on, a queue edited by the user (the web's queue sheet, play next, add to queue): `original` is
    /// kept in step, as the web's provider does, so shuffle off leaves the user's placement alone.
    /// - an entry inserted or taken out: that id is forgotten once (it stays where the user put it);
    /// - one entry moved: in the pre-shuffle order it goes just before the first item that now follows it and
    ///   is still in that order (or last); moved into the past, it leaves the order.
    /// Anything else (a different queue) leaves `original` as it is: unshuffle copes with ids it doesn't find.
    static func edited(original: [String], from old: PlaybackQueue, to new: PlaybackQueue) -> [String] {
        let a = old.items.map(\.id), b = new.items.map(\.id)
        var o = original
        func forget(_ id: String) { if let k = o.firstIndex(of: id) { o.remove(at: k) } }
        if b.count == a.count + 1 || b.count + 1 == a.count {
            // One inserted (b longer) or one taken out (a longer): the single entry the other lacks.
            let (long, short) = b.count > a.count ? (b, a) : (a, b)
            let i = short.indices.first(where: { long[$0] != short[$0] }) ?? short.count
            if Array(long[..<i] + long[(i + 1)...]) == short { forget(long[i]) }
            return o
        }
        guard a.count == b.count, a != b, a.sorted() == b.sorted(),
              let i = a.indices.first(where: { a[$0] != b[$0] }),
              let j = a.indices.last(where: { a[$0] != b[$0] }) else { return o }
        // Where the moved entry landed: a[i] moved down to j, or a[j] moved up to i.
        let to: Int
        if b[j] == a[i], Array(a[(i + 1)...j]) == Array(b[i..<j]) { to = j }
        else if b[i] == a[j], Array(a[i..<j]) == Array(b[(i + 1)...j]) { to = i }
        else { return o }
        let id = b[to]
        guard let k = o.firstIndex(of: id) else { return o }   // added while shuffled: it stays where it is put
        o.remove(at: k)
        if to > new.index {
            let after = b[(to + 1)...].first(where: { o.contains($0) })
            o.insert(id, at: after.flatMap { o.firstIndex(of: $0) } ?? o.count)
        }
        return o
    }
}
