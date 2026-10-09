import XCTest
@testable import Lark

/// The same fixtures as the web's `web/src/player/modes.test.ts`, with the same results.
final class PlayModesTests: XCTestCase {
    private func pq(_ ids: [Int], _ index: Int = 0) -> PlaybackQueue {
        PlaybackQueue(items: ids.map(track), index: index, positionMs: 0, source: .list)
    }
    private func ids(_ q: PlaybackQueue) -> [String] { q.items.map(\.id) }
    private func s(_ xs: [Int]) -> [String] { xs.map(String.init) }
    /// A repeatable "random" source: the given values, round and round.
    private func seq(_ xs: Double...) -> () -> Double {
        var i = 0
        return { defer { i += 1 }; return xs[i % xs.count] }
    }

    func testShuffleKeepsTheCurrentItemAndPermutesOnlyWhatFollows() {
        let (out, original) = PlayModes.shuffleUpcoming(pq([1, 2, 3, 4, 5], 1), random: seq(0.1, 0.9, 0.5))
        XCTAssertEqual(Array(ids(out).prefix(2)), s([1, 2]))
        XCTAssertEqual(out.index, 1)
        XCTAssertEqual(ids(out).dropFirst(2).sorted(), s([3, 4, 5]))
        XCTAssertEqual(original, s([3, 4, 5]))
        XCTAssertEqual(out.source, .list)
    }

    func testUnshuffleRestoresTheOrderPlayNextStaysInFrontAppendedAtTheEnd() {
        let shuffled = pq([1, 9, 5, 3, 4, 7], 0)   // 9 = play next, 7 = add to queue, original [3,4,5]
        XCTAssertEqual(ids(PlayModes.unshuffle(shuffled, original: s([3, 4, 5]))), s([1, 9, 3, 4, 5, 7]))
    }

    func testUnshuffleDropsRemovedAndAlreadyPlayedItems() {
        // Shuffled from [1, 2, 3, 4, 5] at 1; then 4 played (now current) and 5 removed.
        let out = PlayModes.unshuffle(pq([1, 4, 3, 2], 1), original: s([2, 3, 4, 5]))
        XCTAssertEqual(ids(out), s([1, 4, 2, 3]))
        XCTAssertEqual(out.index, 1)
    }

    func testUnshuffleMatchesRepeatedIDsAsAMultiset() {
        XCTAssertEqual(ids(PlayModes.unshuffle(pq([1, 2, 3], 1), original: s([2, 3, 2]))), s([1, 2, 3]))
        XCTAssertEqual(ids(PlayModes.unshuffle(pq([1, 2, 3, 2], 0), original: s([3, 2, 2]))), s([1, 3, 2, 2]))
    }

    func testAShuffledNewPassNeverStartsWithTheItemThatJustEnded() {
        // Without the guard this random source puts 4 (just ended) first: [4, 2, 3, 1].
        let pass = PlayModes.newPass(pq([1, 2, 3, 4], 3), shuffle: true, random: seq(0, 0.99, 0.99))
        XCTAssertNotEqual(pass.items[0].id, "4")
        XCTAssertEqual(ids(pass).sorted(), s([1, 2, 3, 4]))
        for _ in 0..<200 {
            XCTAssertNotEqual(PlayModes.newPass(pq([1, 2, 3], 2), shuffle: true, random: { .random(in: 0..<1) }).items[0].id, "3")
        }
        XCTAssertEqual(ids(PlayModes.newPass(pq([5], 0), shuffle: true, random: seq(0))), s([5]))   // nothing else to start with
    }

    func testUnshuffleWithNothingRememberedKeepsTheCurrentOrder() {
        XCTAssertEqual(ids(PlayModes.unshuffle(pq([1, 3, 2], 0), original: [])), s([1, 3, 2]))
    }

    func testNewPassSameOrderFromZeroOrAFreshShuffle() {
        let end = pq([1, 2, 3, 4], 3)
        let plain = PlayModes.newPass(end, shuffle: false, random: seq(0))
        XCTAssertEqual(ids(plain), s([1, 2, 3, 4]))
        XCTAssertEqual(plain.index, 0)
        XCTAssertEqual(plain.source, .list)
        let mixed = PlayModes.newPass(end, shuffle: true, random: seq(0, 0, 0))
        XCTAssertEqual(mixed.index, 0)
        XCTAssertEqual(ids(mixed).sorted(), s([1, 2, 3, 4]))
        XCTAssertNotEqual(ids(mixed), s([1, 2, 3, 4]))
    }

    // MARK: Persistence and the wire name

    func testCodableUsesRepeatAndDecodesLeniently() throws {
        let data = try JSONEncoder().encode(PlayModes(shuffle: true, repeatMode: .one, original: ["3", "4"]))
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(obj["repeat"] as? String, "one")
        XCTAssertNil(obj["repeatMode"])
        XCTAssertEqual(try JSONDecoder().decode(PlayModes.self, from: data), PlayModes(shuffle: true, repeatMode: .one, original: ["3", "4"]))
        XCTAssertEqual(try JSONDecoder().decode(PlayModes.self, from: Data(#"{"shuffle":"yes","repeat":"sometimes","original":"x"}"#.utf8)), PlayModes())
        XCTAssertEqual(try JSONDecoder().decode(PlayModes.self, from: Data(#"{"repeat":"all"}"#.utf8)), PlayModes(repeatMode: .all))
    }

    // MARK: Edits while shuffled (the web provider's forgetOriginal and move)

    func testAnInsertedOrRemovedEntryIsForgottenOnce() {
        // add to queue 7 after [1 | 3 4 5] (shuffled from [3 4 5]): 7 is not in the order, nothing changes
        XCTAssertEqual(PlayModes.edited(original: s([3, 4, 5]), from: pq([1, 5, 3, 4]), to: pq([1, 7, 5, 3, 4])), s([3, 4, 5]))
        // play next of 4, which had already played (a second copy): that id is forgotten once
        XCTAssertEqual(PlayModes.edited(original: s([3, 4, 5]), from: pq([4, 1, 5, 3], 1), to: pq([4, 1, 4, 5, 3], 1)), s([3, 5]))
        // the queue sheet's ✕ on 3
        XCTAssertEqual(PlayModes.edited(original: s([3, 4, 5]), from: pq([1, 5, 3, 4]), to: pq([1, 5, 4])), s([4, 5]))
    }

    func testAMovedEntryKeepsItsNewNeighbourOnUnshuffle() {
        // [1 | 5 3 4] shuffled from [3 4 5]; 4 dragged up before 5: in the order it goes before 5
        let o = PlayModes.edited(original: s([3, 4, 5]), from: pq([1, 5, 3, 4]), to: pq([1, 4, 5, 3]))
        XCTAssertEqual(o, s([3, 4, 5]))
        // 5 dragged to the end: last
        XCTAssertEqual(PlayModes.edited(original: s([3, 4, 5]), from: pq([1, 5, 3, 4]), to: pq([1, 3, 4, 5])), s([3, 4, 5]))
        // 3 dragged to just before 5 in [1 | 5 4 3]: before 5
        XCTAssertEqual(PlayModes.edited(original: s([3, 4, 5]), from: pq([1, 5, 4, 3]), to: pq([1, 3, 5, 4])), s([4, 3, 5]))
        // moved into the past (before the current item): it leaves the order
        XCTAssertEqual(PlayModes.edited(original: s([3, 4, 5]), from: pq([1, 5, 3, 4]), to: pq([4, 1, 5, 3], 1)), s([3, 5]))
        // and unshuffle keeps it next to its new neighbour
        let moved = pq([1, 3, 5, 4])
        XCTAssertEqual(ids(PlayModes.unshuffle(moved, original: s([4, 3, 5]))), s([1, 4, 3, 5]))
    }

    func testAnotherQueueLeavesTheOrderAlone() {
        XCTAssertEqual(PlayModes.edited(original: s([3, 4]), from: pq([1, 3, 4]), to: pq([8, 9])), s([3, 4]))
        XCTAssertEqual(PlayModes.edited(original: s([3, 4]), from: pq([1, 3, 4]), to: pq([1, 3, 4])), s([3, 4]))
    }
}
