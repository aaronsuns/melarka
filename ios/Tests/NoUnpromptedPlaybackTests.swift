import XCTest
import AVFoundation
import SwiftUI
@testable import Lark

/// Music never starts on a phone nobody touched. Each test is one way it could (and some did): the phone lies
/// locked on a desk in a meeting, iOS wakes Lark in the background, the Wi-Fi comes back, a call ends long after
/// the music stopped. Sound may start by itself only right after the user's own playback was cut off
/// (`PlayerEngine.autoResumeWindow`); everything else waits for the user (the app opened, a tap, the car's play).
@MainActor final class NoUnpromptedPlaybackTests: EngineTestCase {
    /// Well past `autoResumeWindow`.
    let anHourLater: TimeInterval = 3600

    func assertSilent(_ what: String, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertFalse(engine.playing, "playing after \(what)", file: file, line: line)
        XCTAssertFalse(backend.loads.contains { $0.2 }, "a load with autoplay after \(what)", file: file, line: line)
        XCTAssertFalse(backend.calls.contains("play"), "backend.play after \(what)", file: file, line: line)
    }

    // MARK: The page's open preference in a background launch

    /// iOS launched Lark in the background (the cache refresh) on a locked phone and the page said hello: no
    /// open preference starts music, whatever the scene does short of becoming active, and however the network
    /// comes and goes meanwhile.
    func testABackgroundLaunchOnALockedPhoneNeverPlays() async {
        for onOpen in ["shuffle_favorites", "resume", "nothing"] {
            cache.favorites = [trackModel(1), trackModel(2)]
            cache.local = [1: tmp("1.m4a"), 2: tmp("2.m4a")]
            api.randomFavoritesAnswer = ("favorites", [trackModel(3)])
            makeEngine()
            engine.isOnScreen = { false }
            engine.handle(.hello(onOpen: onOpen))
            await engine.idle()
            network.isOnline = false; network.isOnline = true
            advanceClock(anHourLater); scheduler.advance(anHourLater)
            engine.tick()
            await engine.idle()
            assertSilent("a background hello with onOpen \(onOpen)")
        }
    }

    /// The same through `AppServices`: background and inactive (launching, the lock screen) are not opened.
    func testScenePhasesShortOfActiveNeverRunTheOpenPreference() async throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        defaults.set("https://desk.lark.test/", forKey: AppServices.serverURLKey)
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "3")
        let cache = FakeCache()
        cache.favorites = [trackModel(1)]; cache.local = [1: tmp("1.m4a")]
        let backend = FakeBackend()
        let s = AppServices(defaults: defaults, secrets: secrets, session: stubSession(), dataDirectory: dir.appendingPathComponent("data"),
                            backend: backend, cache: cache, network: FakeNetwork())
        s.isOnScreen = { false }
        let e = try XCTUnwrap(s.engine)
        e.handle(.hello(onOpen: "shuffle_favorites"))
        for phase in [ScenePhase.background, .inactive, .background, .inactive] { s.scenePhaseChanged(phase) }
        await e.idle()
        XCTAssertFalse(e.playing)
        XCTAssertTrue(backend.loads.isEmpty)
    }

    // MARK: The network coming back long after a stop

    /// A stream broke offline and the user walked away: the Wi-Fi coming back an hour later leaves it stopped.
    func testAnInterruptedStreamDoesNotResumeWhenTheNetworkReturnsMuchLater() {
        engine.handle(.setQueue(q([5], index: 0, pos: 0, play: true)))
        backend.start()
        network.isOnline = false
        backend.fail(network: true)
        XCTAssertFalse(engine.playing)
        backend.loads = []; backend.calls = []
        advanceClock(anHourLater)
        network.isOnline = true
        assertSilent("the network returning an hour after a broken stream")
        engine.handle(.play(.track))                      // the user's play still works
        XCTAssertTrue(engine.playing)
    }

    /// The queue ended offline with only streams next: an hour later the network does not start the next one.
    func testTheNextStreamDoesNotStartWhenTheNetworkReturnsMuchLater() {
        cache.local = [5: tmp("5.m4a")]
        engine.handle(.setQueue(q([5, 6], index: 0, pos: 0, play: true)))
        backend.start()
        network.isOnline = false
        backend.finish()
        backend.loads = []; backend.calls = []
        advanceClock(anHourLater)
        network.isOnline = true
        assertSilent("the network returning an hour after the queue stopped")
    }

    /// A cold start offline with nothing on the phone: an hour later the network does not shuffle favorites.
    func testAColdStartWithNothingCachedDoesNotShuffleWhenTheNetworkReturnsMuchLater() async {
        network.isOnline = false
        api.randomFavoritesAnswer = ("favorites", [trackModel(3)])
        _ = RemoteCommands.handlePlay(engine)
        await engine.idle()
        assertSilent("a cold start offline")
        advanceClock(anHourLater)
        network.isOnline = true
        await engine.idle()
        assertSilent("the network returning an hour after a cold start offline")
        XCTAssertTrue(api.randomFavoritesCalls.isEmpty)
    }

    // MARK: The end of a long interruption

    /// The music was cut off by a call or a meeting app that lasted an hour: its end does not resume it.
    func testTheEndOfALongInterruptionDoesNotResume() {
        let center = NotificationCenter()
        let audio = AudioSessionController(engine: engine, center: center)
        audio.activateSession = {}; audio.configureSession = {}
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        backend.start()
        post(center, .began)
        XCTAssertFalse(engine.playing)
        backend.loads = []; backend.calls = []
        advanceClock(anHourLater)
        post(center, .ended, options: .shouldResume)
        assertSilent("an interruption that ended an hour later")
        withExtendedLifetime(audio) {}
    }

    /// AVPlayer paused itself (no `began` seen), and an unrelated interruption ends an hour later.
    func testAnOldSystemPauseIsNotResumedByALaterInterruptionEnd() {
        let center = NotificationCenter()
        let audio = AudioSessionController(engine: engine, center: center)
        audio.activateSession = {}; audio.configureSession = {}
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.start()
        backend.interrupt()
        backend.loads = []; backend.calls = []
        advanceClock(anHourLater)
        post(center, .began)
        post(center, .ended, options: .shouldResume)
        assertSilent("an interruption ending an hour after the player paused itself")
        withExtendedLifetime(audio) {}
    }

    /// A short call still resumes, as on every music app.
    func testTheEndOfAShortInterruptionStillResumes() {
        let center = NotificationCenter()
        let audio = AudioSessionController(engine: engine, center: center)
        audio.activateSession = {}; audio.configureSession = {}
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.start()
        post(center, .began)
        advanceClock(PlayerEngine.autoResumeWindow - 1)
        post(center, .ended, options: .shouldResume)
        XCTAssertTrue(engine.playing)
        withExtendedLifetime(audio) {}
    }

    func post(_ center: NotificationCenter, _ type: AVAudioSession.InterruptionType, options: AVAudioSession.InterruptionOptions? = nil) {
        var info: [AnyHashable: Any] = [AVAudioSessionInterruptionTypeKey: type.rawValue]
        if let options { info[AVAudioSessionInterruptionOptionKey] = options.rawValue }
        center.post(name: AVAudioSession.interruptionNotification, object: nil, userInfo: info)
    }
}
