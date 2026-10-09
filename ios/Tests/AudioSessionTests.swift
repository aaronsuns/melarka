import XCTest
import AVFoundation
@testable import Lark

/// Interruptions and route changes, from fake notifications on a private center.
@MainActor final class AudioSessionTests: EngineTestCase {
    var center: NotificationCenter!
    var audio: AudioSessionController!
    /// Each activation, with the number of backend loads and calls made before it.
    var activations: [(loads: Int, calls: Int)] = []
    var configured = 0

    override func setUp() async throws {
        try await super.setUp()
        center = NotificationCenter()
        audio = AudioSessionController(engine: engine, center: center)
        // Never the real session from a test.
        audio.activateSession = { [unowned self] in self.activations.append((self.backend.loads.count, self.backend.calls.count)) }
        audio.configureSession = { [unowned self] in self.configured += 1 }
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        backend.start()
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(activations.count, 1)
        XCTAssertEqual(activations.first?.loads, 0)          // activated before the load
        activations = []
    }

    // MARK: Activation (R1): the category at launch, the session only when sound starts

    func testConfigureSetsTheCategoryAndNeverActivates() {
        let a = AudioSessionController(engine: engine, center: NotificationCenter())
        var activated = 0, configuredHere = 0
        a.activateSession = { activated += 1 }
        a.configureSession = { configuredHere += 1 }
        a.configure()
        XCTAssertEqual(configuredHere, 1)
        XCTAssertEqual(activated, 0)
    }

    func testRestoredQueueWithoutPlayDoesNotActivate() {
        makeEngine()
        audio = AudioSessionController(engine: engine, center: center)
        var activated = 0
        audio.activateSession = { activated += 1 }
        engine.handle(.hello(onOpen: "none"))
        engine.handle(.setQueue(q([3], index: 0, pos: 0, play: false)))
        XCTAssertEqual(activated, 0)
    }

    func testPlayActivatesOnceBeforeThePlayer() {
        engine.pause()
        let calls = backend.calls.count
        engine.handle(.play(.track))
        XCTAssertEqual(activations.count, 1)
        XCTAssertEqual(activations.first?.calls, calls)       // before backend.play
        XCTAssertEqual(backend.calls.last, "play")
    }

    func testNextAndNetworkRetryActivate() {
        engine.next()
        XCTAssertEqual(activations.count, 1)
        backend.fail(network: true)
        scheduler.advance(2)                                   // the retry's load
        XCTAssertEqual(activations.count, 2)
    }

    func interruption(_ type: AVAudioSession.InterruptionType, options: AVAudioSession.InterruptionOptions? = nil) {
        var info: [AnyHashable: Any] = [AVAudioSessionInterruptionTypeKey: type.rawValue]
        if let options { info[AVAudioSessionInterruptionOptionKey] = options.rawValue }
        center.post(name: AVAudioSession.interruptionNotification, object: nil, userInfo: info)
    }

    func routeChange(_ reason: AVAudioSession.RouteChangeReason) {
        center.post(name: AVAudioSession.routeChangeNotification, object: nil,
                    userInfo: [AVAudioSessionRouteChangeReasonKey: reason.rawValue])
    }

    func testInterruptionEndedWithShouldResumeResumes() {
        interruption(.began)
        XCTAssertFalse(engine.playing)
        XCTAssertTrue(engine.pausedBySystem)
        XCTAssertEqual(backend.calls.last, "pause")
        interruption(.ended, options: .shouldResume)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(backend.calls.last, "play")
        XCTAssertEqual(activations.count, 1)                   // the session again, before the player
    }

    func testInterruptionEndedWithoutShouldResumeStaysPaused() {
        interruption(.began)
        interruption(.ended, options: [])
        XCTAssertFalse(engine.playing)
        interruption(.began)
        interruption(.ended)                  // no options at all
        XCTAssertFalse(engine.playing)
        XCTAssertFalse(backend.calls.contains("play"))
    }

    /// AVPlayer itself reports the pause first (its own interruption handling): it still resumes.
    func testPlayerPausedFirstStillResumes() {
        backend.interrupt()
        XCTAssertFalse(engine.playing)
        interruption(.began)
        interruption(.ended, options: .shouldResume)
        XCTAssertTrue(engine.playing)
    }

    /// The user paused during the interruption (the lock screen): the end of it must not start playback.
    func testUserPauseDuringInterruptionIsNotUndone() {
        interruption(.began)
        engine.pause()
        interruption(.ended, options: .shouldResume)
        XCTAssertFalse(engine.playing)
    }

    /// Not playing when the interruption began (paused by the user): nothing resumes.
    func testInterruptionWhilePausedDoesNotResume() {
        engine.pause()
        interruption(.began)
        interruption(.ended, options: .shouldResume)
        XCTAssertFalse(engine.playing)
    }

    func testHeadphonesOutPauses() {
        routeChange(.oldDeviceUnavailable)
        XCTAssertFalse(engine.playing)
        XCTAssertFalse(engine.pausedBySystem)          // a later interruption end does not bring it back
        interruption(.ended, options: .shouldResume)
        XCTAssertFalse(engine.playing)
    }

    func testOtherRouteChangesKeepPlaying() {
        routeChange(.newDeviceAvailable)
        routeChange(.categoryChange)
        XCTAssertTrue(engine.playing)
    }

    /// AVAudioSession posts its notifications on its own thread.
    func testNotificationFromAnotherThreadIsHandledOnMain() async throws {
        let c = center!
        await Task.detached {
            c.post(name: AVAudioSession.routeChangeNotification, object: nil,
                   userInfo: [AVAudioSessionRouteChangeReasonKey: AVAudioSession.RouteChangeReason.oldDeviceUnavailable.rawValue])
        }.value
        try await waitUntil { !self.engine.playing }
    }

    // MARK: Media services reset (R2)

    func mediaReset() { center.post(name: AVAudioSession.mediaServicesWereResetNotification, object: nil) }

    func testMediaServicesResetReloadsAtThePositionAndPlays() {
        backend.positionMs = 42_000
        let loads = backend.loads.count
        mediaReset()
        XCTAssertEqual(configured, 1)
        XCTAssertEqual(backend.rebuilds, 1)
        XCTAssertEqual(backend.loads.count, loads + 1)
        XCTAssertEqual(backend.loads.last?.1, 42_000)
        XCTAssertEqual(backend.loads.last?.2, true)
        XCTAssertEqual(activations.count, 1)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(engine.current?.id, "1")
        // The old player's late callbacks are dropped: the reload is a new generation.
        backend.delegate?.backendFinished(generation: backend.generation - 1)
        XCTAssertEqual(engine.current?.id, "1")
    }

    func testMediaServicesResetWhilePausedReloadsPaused() {
        backend.positionMs = 9_000
        engine.pause()
        mediaReset()
        XCTAssertEqual(backend.rebuilds, 1)
        XCTAssertEqual(backend.loads.last?.1, 9_000)
        XCTAssertEqual(backend.loads.last?.2, false)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(activations.count, 0)
    }

    func testMediaServicesResetKeepsTheListen() async {
        backend.play(to: 20_000)
        mediaReset()
        backend.start()
        backend.play(to: 40_000)
        engine.next()
        await engine.idle()
        engine.handle(.flushEvents(id: "x"))
        await engine.idle()
        let played = api.postedEvents.flatMap { $0 }.filter { $0.track_id == 1 }.map(\.played_seconds)
        XCTAssertEqual(played, [40])                          // one listen, not two
    }

    func testMediaServicesResetWithNothingLoadedOnlyRebuilds() {
        engine.handle(.stop(.track))
        let loads = backend.loads.count
        mediaReset()
        XCTAssertEqual(backend.rebuilds, 1)
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(configured, 1)
    }
}
