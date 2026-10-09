import XCTest
@testable import Lark

/// Retries, stalls, the network coming back, episode fallbacks, late callbacks,
/// episode progress in the queue, and the smaller edges.
final class EngineRobustnessTests: EngineTestCase {
    func remote(_ id: Int) -> MediaSource {
        .remote(URL(string: "https://lark.test/api/v1/tracks/\(id)/stream?quality=high")!, token: "T1")
    }
    func playEpisodes(_ items: [Item]) {
        engine.handle(.setQueue(.init(kind: .episode, items: items, index: 0, positionMs: nil, play: true, source: .list)))
        backend.start()
    }

    // MARK: 1. A retry that worked is over

    func testPauseAfterASuccessfulRetryPausesTheAudio() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 12_000)
        backend.fail(network: true); scheduler.advance(2); backend.start()
        engine.pause()
        XCTAssertEqual(backend.calls.last, "pause")
        XCTAssertFalse(engine.playing)
    }

    func testASecondBlipRetriesFromWhereItWasNow() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 12_000)
        backend.fail(network: true); scheduler.advance(2); backend.start()
        backend.play(to: 40_000)
        backend.fail(network: true); scheduler.advance(2)
        XCTAssertEqual(backend.loads.last?.1, 40_000)
    }

    func testUnrelatedBlipsDoNotUseUpTheRetries() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        for _ in 0..<5 { backend.start(); backend.fail(network: true); scheduler.advance(10) }
        XCTAssertEqual(backend.loads.count, 6)                     // every blip got its retry
        XCTAssertEqual(backend.loads.last?.0, remote(7))
        XCTAssertTrue(engine.playing)
    }

    func testWhileARetryWaitsTheSavedPositionIsTheRetryPosition() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 12_000)
        backend.fail(network: true)
        backend.positionMs = 0                       // a failed AVPlayerItem reads 0
        engine.pause()
        XCTAssertEqual(store.load()?.music.positionMs, 12_000)
    }

    // MARK: 2. Stall switch

    func testASixSecondStallSwitchesToACachedTrackAndRequeuesTheCurrent() {
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        backend.start(); backend.buffering(true)
        scheduler.advance(5.9)
        XCTAssertEqual(backend.loads.count, 1)
        scheduler.advance(0.1)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("9.m4a")))
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "9", "7", "8"])
        XCTAssertEqual(engine.music.index, 1)
        XCTAssertEqual(notices.last, PlayerEngine.stallNotice)
    }

    func testAStallThatEndsInTimeDoesNotSwitch() {
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        backend.start(); backend.buffering(true)
        scheduler.advance(3); backend.buffering(false); scheduler.advance(10)
        XCTAssertEqual(backend.loads.count, 1)
    }

    func testACachedTrackOrAPauseNeverStallSwitches() {
        cache.local = [7: tmp("7.m4a"), 9: tmp("9.m4a")]
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        backend.start(); backend.buffering(true); scheduler.advance(10)
        XCTAssertEqual(backend.loads.count, 1)
        engine.handle(.setQueue(q([1, 9], index: 0, pos: 0, play: true)))
        backend.start(); backend.buffering(true); engine.pause(); scheduler.advance(10)
        XCTAssertEqual(backend.loads.count, 2)
    }

    // MARK: 3. The network coming back

    func testComingBackOnlineRestartsAPlayerStoppedForTheNetwork() {
        network.isOnline = false
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.fail(network: true)
        XCTAssertFalse(engine.playing)
        XCTAssertNotNil(states.last?.error)
        network.isOnline = true
        XCTAssertEqual(backend.loads.last?.0, remote(7)); XCTAssertEqual(backend.loads.last?.2, true)
        XCTAssertTrue(engine.playing)
        XCTAssertNil(states.last?.error)
    }

    func testComingBackOnlineDoesNotStartAPausedPlayer() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); engine.pause()
        network.isOnline = false; network.isOnline = true
        XCTAssertEqual(backend.loads.count, 1)
        XCTAssertFalse(engine.playing)
    }

    func testComingBackOnlineRetriesAWaitingRefill() async {
        api.refillError = URLError(.notConnectedToInternet)
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true, source: .radio)))
        await engine.idle()
        api.refillError = nil; api.radioAnswer = [trackModel(3)]
        network.isOnline = false; network.isOnline = true
        await engine.idle()
        XCTAssertEqual(api.radioCalls.count, 2)
        XCTAssertEqual(engine.music.items.map(\.id), ["1", "2", "3"])
    }

    // MARK: 4. Episodes and no network

    func testCarPlayOfflineWithAnEpisodeActivePlaysCachedFavorites() async {
        playEpisodes([episode("abcdefghijk")])
        makeEngine()
        XCTAssertEqual(engine.active, .episode)
        network.isOnline = false
        cache.local = [20: tmp("20.m4a")]; cache.favorites = [trackModel(20)]
        await engine.resumeOrShuffleFavorites()
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("20.m4a")))
        XCTAssertEqual(engine.active, .track)
    }

    func testCarPlayOfflineWithAnEpisodeActiveResumesTheMusicQueue() async {
        engine.handle(.setQueue(q([7], index: 0, pos: 5_000, play: false)))
        playEpisodes([episode("abcdefghijk")])
        makeEngine()
        network.isOnline = false
        await engine.resumeOrShuffleFavorites()
        // Offline, an uncached track is not tried as a stream; nothing is on the
        // phone, so nothing plays and the notice says why.
        XCTAssertTrue(backend.loads.isEmpty)
        XCTAssertTrue(notices.contains(PlayerEngine.nothingCachedNotice))
        // Something of the music queue cached: it plays, as music.
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 5_000, play: false)))
        cache.local = [8: tmp("8.m4a")]
        await engine.resumeOrShuffleFavorites()
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("8.m4a")))
        XCTAssertEqual(engine.active, .track)
    }

    func testAnEpisodeFailingOfflineFallsBackToMusic() async {
        cache.local = [20: tmp("20.m4a")]; cache.favorites = [trackModel(20)]
        playEpisodes([episode("abcdefghijk")])
        network.isOnline = false
        backend.fail(network: true)
        await engine.idle()
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("20.m4a")))
        XCTAssertEqual(engine.active, .track)
        XCTAssertEqual(notices.first, PlayerEngine.episodeFallbackNotice)
        XCTAssertEqual(engine.episodes.items.count, 1)            // the episode stays queued for later
    }

    func testAnEpisodeWhoseRetriesRunOutFallsBackToMusic() async {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))
        playEpisodes([episode("abcdefghijk")])
        for d in [2.0, 5, 10] { backend.fail(network: true); scheduler.advance(d) }
        backend.fail(network: true)
        await engine.idle()
        XCTAssertEqual(engine.active, .track)
        XCTAssertEqual(backend.loads.last?.0, remote(7))
    }

    // MARK: 5. Late callbacks from a replaced item

    func testALateFailureOfTheOldItemIsIgnored() {
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        let old = backend.generation
        backend.start(); engine.next()
        backend.fail(network: false, generation: old)
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(engine.music.index, 1)
    }

    func testALateEndOfTheOldItemIsIgnored() {
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        let old = backend.generation
        backend.start(); engine.next()
        backend.finish(generation: old)
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertEqual(engine.music.index, 1)
    }

    func testALatePauseOfTheOldItemIsIgnored() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        let old = backend.generation
        backend.start(); engine.next(); backend.start()
        backend.interrupt(generation: old)
        XCTAssertTrue(engine.playing)
        backend.interrupt()
        XCTAssertFalse(engine.playing)
    }

    func testARetryIsANewGeneration() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        let first = backend.generation
        backend.fail(network: true); scheduler.advance(2)
        XCTAssertNotEqual(backend.generation, first)
        backend.fail(network: false, generation: first)
        XCTAssertEqual(backend.loads.count, 2)
    }

    // MARK: 6. Episode progress kept in the queue

    func testEpisodeResumeKeepsTheSavedPositionAfterNextAndPrev() {
        playEpisodes([episode("aaaaaaaaaaa"), episode("bbbbbbbbbbb")])
        backend.play(to: 600_000)
        engine.next()
        XCTAssertEqual(engine.episodes.items[0].meta["position_s"], .int(600))
        XCTAssertEqual(queues(.episode).last?.items[0].meta["position_s"], .int(600))
        backend.start(); backend.play(to: 5_000)
        engine.prev(); engine.prev()
        XCTAssertEqual(engine.episodes.index, 0)
        XCTAssertEqual(backend.loads.last?.1, 600_000)
        makeEngine()
        XCTAssertEqual(engine.episodes.items[0].meta["position_s"], .int(600))
    }

    func testAFinishedEpisodeStartsFromZeroWhenGoneBackTo() {
        playEpisodes([episode("aaaaaaaaaaa", positionS: 120), episode("bbbbbbbbbbb")])
        XCTAssertEqual(backend.loads.last?.1, 0)                  // positionMs nil: the web decides; 0 here
        backend.finish()
        XCTAssertEqual(engine.episodes.items[0].meta["played"], .bool(true))
        XCTAssertEqual(engine.episodes.items[0].meta["position_s"], .int(0))
        engine.prev()
        XCTAssertEqual(engine.episodes.index, 0)
        XCTAssertEqual(backend.loads.last?.1, 0)
    }

    // MARK: 7. A shuffle with nothing to play says why

    func testShuffleOfflineWithNothingCachedSaysWhy() async {
        network.isOnline = false
        await engine.shuffleFavorites()
        XCTAssertEqual(notices.last, PlayerEngine.nothingCachedNotice)
        XCTAssertEqual(states.last { $0.kind == .track }?.error, PlayerEngine.nothingCachedNotice)
    }

    func testShuffleWhenTheServerFailsSaysWhy() async {
        api.refillError = URLError(.timedOut)
        await engine.shuffleFavorites()
        XCTAssertEqual(notices.last, PlayerEngine.shuffleFailedNotice)
        XCTAssertNotNil(states.last { $0.kind == .track }?.error)
    }

    // MARK: 10. Bounds and schema

    func testPendingEventsAreCappedDroppingTheOldest() {
        api.postEventsError = URLError(.timedOut)
        store.saveEvents((0..<PlayEventQueue.maxPending).map {
            PlayEvent(client_event_id: "e\($0)", track_id: 1, started_at: 1, played_seconds: 40, skipped: false, quality: "high")
        })
        events.add(trackId: 2, startedAt: 2, playedSeconds: 40, skipped: false, quality: "high")
        XCTAssertEqual(events.pending.count, PlayEventQueue.maxPending)
        XCTAssertEqual(events.pending.first?.client_event_id, "e1")
        XCTAssertEqual(events.pending.last?.track_id, 2)
    }

    func testTheSnapshotCarriesASchemaVersion() throws {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))
        let obj = try JSONSerialization.jsonObject(with: Data(contentsOf: dir.appendingPathComponent("queues-1.json"))) as? [String: Any]
        XCTAssertEqual(obj?["version"] as? Int, QueueSnapshot.currentVersion)
    }

    func testAnUnknownSchemaVersionReadsAsEmpty() throws {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))
        let f = dir.appendingPathComponent("queues-1.json")
        var obj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: f)) as? [String: Any])
        obj["version"] = 99
        try JSONSerialization.data(withJSONObject: obj).write(to: f)
        makeEngine()
        XCTAssertTrue(engine.music.items.isEmpty)
    }

    // MARK: 11. No source for an item

    func testNoTokenSkipsToACachedItem() {
        api.token = nil
        cache.local = [9: tmp("9.m4a")]
        engine.handle(.setQueue(q([7, 8, 9], index: 0, pos: 0, play: true)))
        XCTAssertEqual(backend.loads.count, 1)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("9.m4a")))
    }
}
