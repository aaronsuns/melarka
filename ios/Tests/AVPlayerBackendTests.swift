import XCTest
import AVFoundation
@testable import Lark

/// Records every callback with its generation. `onFinished` lets a test answer like the engine does.
@MainActor final class RecordingDelegate: MediaBackendDelegate {
    var events: [String] = []
    var ticks: [(ms: Int, generation: Int)] = []
    var onFinished: ((Int) -> Void)?

    func backendStarted(generation: Int) { events.append("started:\(generation)") }
    func backendPaused(generation: Int) { events.append("paused:\(generation)") }
    func backendFinished(generation: Int) { events.append("finished:\(generation)"); onFinished?(generation) }
    func backendFailed(network: Bool, generation: Int) { events.append("failed:\(network):\(generation)") }
    func backendBuffering(_ on: Bool, generation: Int) { events.append("buffering:\(on):\(generation)") }
    func backendTick(positionMs: Int, generation: Int) { ticks.append((positionMs, generation)) }

    var started: Bool { events.contains { $0.hasPrefix("started:") } }
    var finished: Bool { events.contains { $0.hasPrefix("finished:") } }
    func has(_ e: String) -> Bool { events.contains(e) }
}

/// Silence, 44.1 kHz mono PCM, in a .caf in the temporary directory: real AVFoundation, no server.
func makeSilentFile(seconds: Double) throws -> URL {
    let url = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("silence-\(UUID().uuidString).caf")
    let format = AVAudioFormat(standardFormatWithSampleRate: 44_100, channels: 1)!
    let file = try AVAudioFile(forWriting: url, settings: format.settings)
    let frames = AVAudioFrameCount(44_100 * seconds)
    let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames)!
    buffer.frameLength = frames            // zero-filled
    try file.write(from: buffer)
    return url
}

extension AVPlayerBackend {
    /// What a timed-out wait reports: the callbacks so far and the player's state.
    func debugState(_ d: RecordingDelegate) -> String {
        let item = player.currentItem as? LarkPlayerItem
        let items = player.items().map { i -> String in
            let l = i as? LarkPlayerItem
            return "g=\(l?.generation.map(String.init) ?? "nil") status=\(i.status.rawValue) t=\(String(format: "%.2f", i.currentTime().seconds))"
        }
        return "events=\(d.events) ticks=\(d.ticks.map { "\($0.generation):\($0.ms)" }) adopted=\(adoptedPreloads) "
            + "rate=\(player.rate) tcs=\(player.timeControlStatus.rawValue) waiting=\(player.reasonForWaitingToPlay?.rawValue ?? "-") "
            + "pending=\(item?.pendingSeekMs.map(String.init) ?? "nil") seeks=\(item?.seekCount ?? -1) items=\(items)"
    }
}

@MainActor final class AVPlayerBackendTests: XCTestCase {
    var files: [URL] = []
    var backend: AVPlayerBackend!

    override func setUp() async throws {
        try await ColdStart.audio()
    }

    override func tearDown() async throws {
        backend?.stop(); backend = nil
        for f in files { try? FileManager.default.removeItem(at: f) }
        files = []
    }

    func silent(_ seconds: Double) throws -> URL {
        let u = try makeSilentFile(seconds: seconds); files.append(u); return u
    }

    func testPlaysGeneratedFileToTheEnd() async throws {
        let url = try silent(1)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        try await waitUntil(timeout: 10) { d.started && d.finished }
        XCTAssertTrue(d.has("started:1"), "\(d.events)")
        XCTAssertTrue(d.has("finished:1"), "\(d.events)")
        XCTAssertFalse(d.events.contains { $0.hasPrefix("paused:") || $0.hasPrefix("failed:") }, "\(d.events)")
        XCTAssertFalse(d.ticks.isEmpty)
        XCTAssertTrue(d.ticks.allSatisfy { $0.generation == 1 })
    }

    func testMissingFileReportsFailureNotNetwork() async throws {
        let url = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("missing-\(UUID().uuidString).caf")
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 4)
        try await waitUntil(timeout: 10) { d.events.contains { $0.hasPrefix("failed:") } }
        XCTAssertEqual(d.events.filter { $0.hasPrefix("failed:") }, ["failed:false:4"])
        XCTAssertFalse(d.started)
    }

    func testStartPositionAndDuration() async throws {
        let url = try silent(2)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(url), startMs: 1000, autoplay: true, rate: 1, gain: 1, generation: 1)
        XCTAssertEqual(b.positionMs, 1000)                      // before the item is ready, the position asked for
        try await waitUntil(timeout: 10) { !d.ticks.isEmpty && d.started }
        XCTAssertGreaterThanOrEqual(d.ticks.first!.ms, 900, "\(d.ticks)")
        XCTAssertEqual(b.durationMs ?? 0, 2000, accuracy: 50)
        try await waitUntil(timeout: 10) { d.finished }
    }

    /// A pause the engine asked for is never reported back as `backendPaused`.
    func testRequestedPauseIsNotReported() async throws {
        let url = try silent(3)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        try await waitUntil(timeout: 10) { d.started && b.isPlaying }
        b.pause()
        XCTAssertFalse(b.isPlaying)
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertFalse(d.events.contains { $0.hasPrefix("paused:") }, "\(d.events)")
        let at = b.positionMs
        b.seek(ms: 2000)
        try await waitUntil(timeout: 5) { b.positionMs >= 1900 }
        XCTAssertGreaterThan(b.positionMs, at)
        b.play()
        try await waitUntil(timeout: 10) { d.finished }
    }

    /// A replaced item never reports again: only the new load's generation is heard.
    func testReplacedItemIsSilent() async throws {
        let a = try silent(1), c = try silent(1)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        let mark = d.events.count, tickMark = d.ticks.count
        b.load(.file(c), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 2)
        try await waitUntil(timeout: 10) { d.has("finished:2") }
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertFalse(d.events[mark...].contains { $0.hasSuffix(":1") }, "\(d.events)")
        XCTAssertTrue(d.ticks[tickMark...].allSatisfy { $0.generation == 2 })
    }

    /// Gapless: the queue player moves to the preloaded item by itself; the engine's load of that same source
    /// (from `backendFinished`) takes it over instead of loading it again.
    func testPreloadedItemIsAdoptedNotReloaded() async throws {
        let a = try silent(1), c = try silent(1)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        d.onFinished = { g in if g == 1 { b.load(.file(c), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 2); b.preload(nil, gain: 1) } }
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        b.preload(.file(c), gain: 1)
        try await waitUntil(timeout: 10) { d.has("finished:2") }
        XCTAssertEqual(b.adoptedPreloads, 1)
        XCTAssertEqual(d.events.filter { $0.hasPrefix("finished:") }, ["finished:1", "finished:2"])
    }

    /// The engine chose something else at the end: the preloaded item must not play on by itself.
    func testUnadoptedPreloadDoesNotPlay() async throws {
        let a = try silent(1), c = try silent(3)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        b.preload(.file(c), gain: 1)
        try await waitUntil(timeout: 10) { d.has("finished:1") }
        try await Task.sleep(nanoseconds: 500_000_000)
        XCTAssertFalse(b.isPlaying)
        XCTAssertEqual(b.player.rate, 0)
        XCTAssertTrue(b.player.items().isEmpty)
        XCTAssertEqual(b.adoptedPreloads, 0)
    }

    /// Taking over a preloaded episode that resumes mid-way: paused until the seek lands, so its start never sounds.
    func testAdoptWithAPositionWaitsForTheSeek() async throws {
        let a = try silent(1), c = try silent(3)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        var rateAfterLoad: Float = -1
        d.onFinished = { g in
            guard g == 1 else { return }
            b.load(.file(c), startMs: 1500, autoplay: true, rate: 1, gain: 1, generation: 2)
            rateAfterLoad = b.player.rate
        }
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        b.preload(.file(c), gain: 1)
        try await waitUntil(timeout: 10, state: { b.debugState(d) }) { d.ticks.contains { $0.generation == 2 } }
        XCTAssertEqual(b.adoptedPreloads, 1)
        XCTAssertEqual(rateAfterLoad, 0)
        XCTAssertGreaterThanOrEqual(d.ticks.first { $0.generation == 2 }!.ms, 1400)
        XCTAssertFalse(d.events.contains { $0.hasPrefix("paused:") }, "\(d.events)")
        try await waitUntil(timeout: 10, state: { b.debugState(d) }) { d.has("finished:2") }
    }

    /// The same seek asked for twice (an item's ready status arriving after an adopt already started its seek):
    /// the second cancels the first, and only the second's completion ends the pending seek. Before, the
    /// cancelled one cleared it and started playback from the old position, before the second had landed.
    func testARepeatedSeekWaitsForTheNewestOne() async throws {
        let url = try silent(3)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        let item = try XCTUnwrap(b.player.currentItem as? LarkPlayerItem)
        try await waitUntil(timeout: 10) { item.status == .readyToPlay && b.player.rate != 0 }
        // Stopped under the backend, as an adopt does before its seek: the seek's completion is what plays it.
        b.player.pause()
        b.seek(ms: 2000)
        b.seek(ms: 2000)
        XCTAssertEqual(item.pendingSeekMs, 2000)
        XCTAssertEqual(b.positionMs, 2000)
        // Playback resumes only once the newest seek has landed: never from the old position.
        try await waitUntil(timeout: 10) { b.player.rate != 0 }
        XCTAssertGreaterThanOrEqual(item.currentTime().seconds, 1.95)
        XCTAssertNil(item.pendingSeekMs)
    }

    /// A media services reset: a new player, nothing of the old one reports again, and the next load plays.
    func testRebuildGivesAFreshPlayer() async throws {
        let a = try silent(3), c = try silent(1)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        try await waitUntil(timeout: 10) { d.started }
        let old = b.player
        b.rebuild()
        XCTAssertFalse(b.player === old)
        XCTAssertFalse(b.isPlaying)
        XCTAssertNil(b.player.currentItem)
        let mark = d.events.count, tickMark = d.ticks.count
        b.load(.file(c), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 2)
        try await waitUntil(timeout: 10) { d.has("finished:2") }
        XCTAssertFalse(d.events[mark...].contains { $0.hasSuffix(":1") }, "\(d.events)")
        XCTAssertTrue(d.ticks[tickMark...].allSatisfy { $0.generation == 2 })
        XCTAssertEqual(old.rate, 0)
    }

    func testRemoteAssetCarriesTheTokenAsAHeader() {
        let item = AVPlayerBackend.makeItem(.remote(URL(string: "https://lark.test/api/v1/tracks/7/stream?quality=high")!, token: "SECRET"))
        let asset = item.asset as! AVURLAsset
        XCTAssertEqual(asset.url.absoluteString, "https://lark.test/api/v1/tracks/7/stream?quality=high")
        XCTAssertFalse(asset.url.absoluteString.contains("SECRET"))
        XCTAssertEqual(AVPlayerBackend.headers(for: .remote(asset.url, token: "SECRET")), ["Authorization": "Bearer SECRET"])
        XCTAssertNil(AVPlayerBackend.headers(for: .file(URL(fileURLWithPath: "/tmp/x.caf"))))
    }
}

// MARK: - Loudness gain and the master volume

extension AVPlayerBackendTests {
    /// The volume an item's audio mix gives its audio track; nil without a mix.
    func mixVolume(_ item: AVPlayerItem?) -> Float? {
        guard let p = item?.audioMix?.inputParameters.first else { return nil }
        var start: Float = -1, end: Float = -1
        var range = CMTimeRange.zero
        guard p.getVolumeRamp(for: .zero, startVolume: &start, endVolume: &end, timeRange: &range) else { return nil }
        return start
    }

    func testAGainBelowOneSetsTheItemsAudioMix() async throws {
        let url = try silent(2)
        let b = AVPlayerBackend(); backend = b
        b.load(.file(url), startMs: 0, autoplay: false, rate: 1, gain: 0.5, generation: 1)
        let item = try XCTUnwrap(b.player.currentItem)
        try await waitUntil(timeout: 10) { item.audioMix != nil }
        XCTAssertEqual(try XCTUnwrap(mixVolume(item)), 0.5, accuracy: 0.0001)
        XCTAssertEqual(b.player.volume, 1, "an item's gain is not the master volume")
    }

    func testUnityGainLeavesNoAudioMix() async throws {
        let url = try silent(2)
        let b = AVPlayerBackend(); backend = b
        b.load(.file(url), startMs: 0, autoplay: false, rate: 1, gain: 1, generation: 1)
        let item = try XCTUnwrap(b.player.currentItem as? LarkPlayerItem)
        try await waitUntil(timeout: 10) { item.status == .readyToPlay }
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertNil(item.audioMix)
    }

    /// The loudness switch, live: the playing item's mix changes (and goes away at unity), and a gain asked
    /// for before an older one finished loading wins.
    func testSetGainChangesTheCurrentItem() async throws {
        let url = try silent(2)
        let b = AVPlayerBackend(); backend = b
        b.load(.file(url), startMs: 0, autoplay: false, rate: 1, gain: 0.5, generation: 1)
        b.setGain(1, next: 1)
        let item = try XCTUnwrap(b.player.currentItem as? LarkPlayerItem)
        try await waitUntil(timeout: 10) { item.status == .readyToPlay }
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertNil(item.audioMix, "the older 0.5 must not land after the newer 1")
        b.setGain(0.25, next: 1)
        try await waitUntil(timeout: 10) { item.audioMix != nil }
        XCTAssertEqual(try XCTUnwrap(mixVolume(item)), 0.25, accuracy: 0.0001)
    }

    /// Gapless at two levels: the preloaded item has its own mix before it starts, and keeps it when adopted.
    func testThePreloadedItemHasItsOwnMix() async throws {
        let a = try silent(2), c = try silent(1)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        d.onFinished = { g in if g == 1 { b.load(.file(c), startMs: 0, autoplay: true, rate: 1, gain: 0.25, generation: 2); b.preload(nil, gain: 1) } }
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 0.5, generation: 1)
        b.preload(.file(c), gain: 0.25)
        let next = try XCTUnwrap(b.player.items().last)
        try await waitUntil(timeout: 10) { next.audioMix != nil || d.has("finished:1") }
        XCTAssertFalse(d.has("finished:1"), "the mix is there while the item is only preloaded, before it plays")
        XCTAssertEqual(try XCTUnwrap(mixVolume(next)), 0.25, accuracy: 0.0001)
        try await waitUntil(timeout: 10) { d.has("started:2") || d.has("finished:2") }
        XCTAssertEqual(b.adoptedPreloads, 1)
        XCTAssertTrue(b.player.currentItem === next || d.has("finished:2"))
        XCTAssertEqual(try XCTUnwrap(mixVolume(next)), 0.25, accuracy: 0.0001)
    }

    func testSetVolumeIsTheMasterVolumeAndSurvivesARebuild() throws {
        let b = AVPlayerBackend(); backend = b
        b.setVolume(0.3)
        XCTAssertEqual(b.player.volume, 0.3, accuracy: 0.0001)
        b.rebuild()
        XCTAssertEqual(b.player.volume, 0.3, accuracy: 0.0001)
        b.setVolume(1)
        XCTAssertEqual(b.player.volume, 1)
    }
}

// MARK: - No sound before the gain is set

extension AVPlayerBackendTests {
    /// Polls until `item` has its mix; true if the player ran at any moment before that.
    func ranBeforeTheMix(_ b: AVPlayerBackend, _ item: AVPlayerItem) async throws -> Bool {
        var ran = false
        let deadline = Date().addingTimeInterval(10)
        while item.audioMix == nil && Date() < deadline {
            if b.player.rate != 0 { ran = true }
            try await Task.sleep(nanoseconds: 2_000_000)
        }
        XCTAssertNotNil(item.audioMix)
        return ran
    }

    func testAnAttenuatedLoadWaitsForItsMix() async throws {
        let url = try silent(2)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 0.5, generation: 1)
        XCTAssertEqual(b.player.rate, 0, "not playing at full level while the mix loads")
        let item = try XCTUnwrap(b.player.currentItem)
        let ran = try await ranBeforeTheMix(b, item)
        XCTAssertFalse(ran)
        try await waitUntil(timeout: 10) { d.started }
        XCTAssertFalse(d.events.contains { $0.hasPrefix("paused:") }, "waiting for the mix is not a pause: \(d.events)")
    }

    func testAPlayCallWaitsForTheMixToo() async throws {
        let url = try silent(2)
        let b = AVPlayerBackend(); backend = b
        b.load(.file(url), startMs: 0, autoplay: false, rate: 1, gain: 0.5, generation: 1)
        b.play()
        let item = try XCTUnwrap(b.player.currentItem)
        let ran = try await ranBeforeTheMix(b, item)
        XCTAssertFalse(ran)
        try await waitUntil(timeout: 10) { b.player.rate != 0 }
    }

    /// A fast skip takes a preloaded item over before its mix has landed: it waits too.
    func testAnAdoptedPreloadWaitsForItsMix() async throws {
        let a = try silent(3), c = try silent(2)
        let d = RecordingDelegate(); let b = AVPlayerBackend(); b.delegate = d; backend = b
        b.load(.file(a), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        b.preload(.file(c), gain: 0.25)
        let next = try XCTUnwrap(b.player.items().last)
        b.load(.file(c), startMs: 0, autoplay: true, rate: 1, gain: 0.25, generation: 2)
        XCTAssertEqual(b.adoptedPreloads, 1)
        XCTAssertTrue(b.player.currentItem === next)
        let ran = try await ranBeforeTheMix(b, next)
        XCTAssertFalse(ran)
        try await waitUntil(timeout: 10) { d.has("started:2") }
    }

    func testUnityPlaysAtOnce() throws {
        let url = try silent(2)
        let b = AVPlayerBackend(); backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 1, generation: 1)
        XCTAssertNotEqual(b.player.rate, 0)
    }

    /// The loudness switch changes the preloaded item in place: the same item, nothing preloaded again.
    func testSetGainChangesThePreloadedItemInPlace() async throws {
        let a = try silent(3), c = try silent(2)
        let b = AVPlayerBackend(); backend = b
        b.load(.file(a), startMs: 0, autoplay: false, rate: 1, gain: 1, generation: 1)
        b.preload(.file(c), gain: 1)
        let next = try XCTUnwrap(b.player.items().last)
        b.setGain(0.5, next: 0.25)
        try await waitUntil(timeout: 10) { next.audioMix != nil }
        XCTAssertTrue(b.player.items().last === next)
        XCTAssertEqual(try XCTUnwrap(mixVolume(next)), 0.25, accuracy: 0.0001)
    }
}

// MARK: - A mix that never comes

/// Loads an asset's audio track only once `release` is set (until then it hangs, as a stuck loadTracks would).
@MainActor final class HeldTrackLoader {
    var release = false
    private(set) var calls = 0
    func load(_ asset: AVAsset) async -> AVAssetTrack? {
        calls += 1
        while !release { try? await Task.sleep(nanoseconds: 10_000_000) }
        return try? await asset.loadTracks(withMediaType: .audio).first
    }
}

extension AVPlayerBackendTests {
    /// The mix's track load hangs: after `mixWaitS` the item plays unmixed (never held for good, so never-stop
    /// never stalls on it), and the mix still goes on if the track turns up later.
    func testAHangingTrackLoadPlaysUnmixedAfterTheWait() async throws {
        let url = try silent(3)
        let scheduler = FakeScheduler(), loader = HeldTrackLoader()
        let d = RecordingDelegate()
        let b = AVPlayerBackend(scheduler: scheduler, loadAudioTrack: { await loader.load($0) }); b.delegate = d; backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 0.5, generation: 1)
        let item = try XCTUnwrap(b.player.currentItem as? LarkPlayerItem)
        try await Task.sleep(nanoseconds: 300_000_000)
        XCTAssertEqual(b.player.rate, 0, "still waiting for the mix")
        XCTAssertTrue(item.mixPending)
        scheduler.advance(AVPlayerBackend.mixWaitS - 0.1)
        XCTAssertEqual(b.player.rate, 0)
        scheduler.advance(0.1)
        XCTAssertFalse(item.mixPending)
        XCTAssertNotEqual(b.player.rate, 0, "played unmixed after the wait")
        try await waitUntil(timeout: 10) { d.started }
        XCTAssertNil(item.audioMix)
        loader.release = true
        try await waitUntil(timeout: 10) { item.audioMix != nil }
        XCTAssertEqual(try XCTUnwrap(mixVolume(item)), 0.5, accuracy: 0.0001)
    }

    /// A mix that lands in time cancels the wait: nothing happens when it would have run out.
    func testAMixInTimeCancelsTheWait() async throws {
        let url = try silent(3)
        let scheduler = FakeScheduler(), loader = HeldTrackLoader()
        loader.release = true
        let b = AVPlayerBackend(scheduler: scheduler, loadAudioTrack: { await loader.load($0) }); backend = b
        b.load(.file(url), startMs: 0, autoplay: true, rate: 1, gain: 0.5, generation: 1)
        let item = try XCTUnwrap(b.player.currentItem)
        try await waitUntil(timeout: 10) { item.audioMix != nil && b.player.rate != 0 }
        XCTAssertEqual(scheduler.pending, 0)
    }
}
