import XCTest
import AppIntents
@testable import Lark

/// Records what an intent asks of the player. `calls` holds the playing calls, `log` everything in order.
@MainActor final class FakeIntentPlayer: IntentPlayer {
    var calls: [String] = []
    var log: [String] = []
    var blocker: LarkIntentError?
    var startsPlaying = true
    var playing = false
    var failureText: String?
    func intentBlocker(resume: Bool) -> LarkIntentError? { log.append("intentBlocker"); return blocker }
    func prepareForPlayback() { log.append("prepareForPlayback") }
    func shuffleFavorites() async { calls.append("shuffleFavorites"); log.append("shuffleFavorites"); playing = startsPlaying }
    func resumeOrShuffleFavorites() async {
        calls.append("resumeOrShuffleFavorites"); log.append("resumeOrShuffleFavorites"); playing = startsPlaying
    }
}

@MainActor final class IntentTests: XCTestCase {
    override func tearDown() async throws {
        AppServices.shared.intentPlayer = nil
    }

    func testShuffleIntentCallsEngineWithoutUI() async throws {
        let fake = FakeIntentPlayer(); AppServices.shared.intentPlayer = fake
        _ = try await ShuffleFavoritesIntent().perform()
        XCTAssertEqual(fake.calls, ["shuffleFavorites"])
        // The session is activated synchronously, before the intent's first await.
        XCTAssertEqual(fake.log, ["intentBlocker", "prepareForPlayback", "shuffleFavorites"])
        XCTAssertFalse(ShuffleFavoritesIntent.openAppWhenRun)
    }

    func testResumeIntentResumesOrShufflesWithoutUI() async throws {
        let fake = FakeIntentPlayer(); AppServices.shared.intentPlayer = fake
        _ = try await ResumeIntent().perform()
        XCTAssertEqual(fake.calls, ["resumeOrShuffleFavorites"])
        XCTAssertEqual(fake.log, ["intentBlocker", "prepareForPlayback", "resumeOrShuffleFavorites"])
        XCTAssertFalse(ResumeIntent.openAppWhenRun)
    }

    func testIntentTextsAreEnglishAndChinese() {
        func text(_ r: LocalizedStringResource, _ lang: String) -> String {
            var r = r; r.locale = Locale(identifier: lang); return String(localized: r)
        }
        XCTAssertEqual(text(ShuffleFavoritesIntent.title, "zh-Hans"), "随机播放收藏")
        XCTAssertEqual(text(ResumeIntent.title, "zh-Hans"), "继续播放")
        XCTAssertEqual(text(ShuffleFavoritesIntent.title, "en"), "Shuffle Favorites")
        XCTAssertEqual(text(ResumeIntent.title, "en"), "Resume")
        XCTAssertEqual(text(LarkIntentError.notSignedIn.localizedStringResource, "zh-Hans"), "未登录")
        XCTAssertEqual(text(LarkIntentError.notSignedIn.localizedStringResource, "en"), "Not signed in")
        XCTAssertEqual(text(LarkIntentError.offlineNothingCached.localizedStringResource, "zh-Hans"), "离线且没有缓存的歌曲")
        XCTAssertEqual(text(LarkIntentError.offlineNothingCached.localizedStringResource, "en"), "Offline, and no songs are cached")
        XCTAssertEqual(text(IntentOutcome.started.dialog, "zh-Hans"), "正在随机播放收藏")
        XCTAssertEqual(text(IntentOutcome.started.dialog, "en"), "Shuffling favorites")
    }

    /// English is the development language; Chinese phones get the zh-Hans text (Siri in 中文 matches its phrases).
    func testTheAppIsLocalizedInEnglishAndChinese() throws {
        XCTAssertEqual(Bundle.main.developmentLocalization, "en")
        for lang in ["en", "zh-Hans", "zh-Hant"] {
            XCTAssertTrue(Bundle.main.localizations.contains(lang), "\(lang) not in \(Bundle.main.localizations)")
        }
        for lang in ["zh-Hans", "en"] {
            let lproj = try XCTUnwrap(Bundle.main.path(forResource: lang, ofType: "lproj"), lang)
            XCTAssertNotNil(Bundle(path: lproj)?.path(forResource: "AppShortcuts", ofType: "strings"), lang)
        }
    }

    /// Finding 4: what cannot play fails with its reason, and never activates the session.
    func testABlockedIntentFailsWithItsReasonAndDoesNotActivate() async {
        for blocker in [LarkIntentError.notSignedIn, .offlineNothingCached] {
            let fake = FakeIntentPlayer(); fake.blocker = blocker
            await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(fake)) { XCTAssertEqual($0 as? LarkIntentError, blocker) }
            await XCTAssertThrowsErrorAsync(try await IntentRun.resume(fake)) { XCTAssertEqual($0 as? LarkIntentError, blocker) }
            XCTAssertEqual(fake.log, ["intentBlocker", "intentBlocker"])
        }
    }

    func testShuffleIntentWhilePlayingIsANoOp() async throws {
        let fake = FakeIntentPlayer(); fake.playing = true
        let outcome = try await IntentRun.shuffleFavorites(fake)
        XCTAssertEqual(outcome, .alreadyPlaying)
        XCTAssertEqual(fake.log, [])                     // not even the session
        XCTAssertEqual(String(localized: outcome.dialog), IntentOutcome.alreadyPlaying.text)
    }

    func testAStartThatDidNotPlayFailsWithTheEnginesReason() async {
        let fake = FakeIntentPlayer(); fake.startsPlaying = false; fake.failureText = "无法获取收藏，请稍后再试"
        await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(fake)) {
            XCTAssertEqual($0 as? LarkIntentError, .nothingToPlay("无法获取收藏，请稍后再试"))
        }
        XCTAssertEqual((LarkIntentError.nothingToPlay("x")).text, "x")
    }

    func testShortcutsOfferBothIntents() {
        XCTAssertEqual(LarkShortcuts.appShortcuts.count, 2)
    }

    func testWithNoServerTheIntentSaysSoInsteadOfDoingNothing() async {
        await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(nil)) {
            XCTAssertEqual($0 as? LarkIntentError, .notSetUp)
        }
        await XCTAssertThrowsErrorAsync(try await IntentRun.resume(nil)) {
            XCTAssertEqual($0 as? LarkIntentError, .notSetUp)
        }
    }

    func testThePlayerTheIntentsDriveIsTheEngineUnlessReplaced() async throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let s = AppServices(defaults: defaults, secrets: MemorySecretStore(), session: stubSession(),
                            dataDirectory: FileManager.default.temporaryDirectory.appendingPathComponent("intents-\(UUID().uuidString)"),
                            backend: FakeBackend(), cache: FakeCache(), network: FakeNetwork())
        XCTAssertNil(s.intentPlayer)                                  // no server yet: nothing to drive
        await s.setServerURL(URL(string: "https://intents.lark.test/")!)
        XCTAssertTrue(s.intentPlayer === s.engine)
        let fake = FakeIntentPlayer()
        s.intentPlayer = fake
        XCTAssertTrue(s.intentPlayer === fake)
        s.intentPlayer = nil
        XCTAssertTrue(s.intentPlayer === s.engine)
    }
}

/// The intents with the real engine: the session is active before anything is awaited.
@MainActor final class IntentEngineTests: EngineTestCase {
    func testShuffleActivatesBeforeTheServerIsAsked() async throws {
        api.randomFavoritesAnswer = ("favorites", [trackModel(4), trackModel(5)])
        var activations: [Int] = []
        engine.willStartPlayback = { [unowned self] in activations.append(self.api.randomFavoritesCalls.count) }
        try await IntentRun.shuffleFavorites(engine)
        XCTAssertEqual(activations.first, 0)                          // before the network call
        XCTAssertEqual(api.randomFavoritesCalls.count, 1)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(engine.music.source, .favorites)
    }

    func testSignedOutFailsBeforeActivating() async throws {
        api.token = nil
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(engine)) { XCTAssertEqual($0 as? LarkIntentError, .notSignedIn) }
        await XCTAssertThrowsErrorAsync(try await IntentRun.resume(engine)) { XCTAssertEqual($0 as? LarkIntentError, .notSignedIn) }
        XCTAssertEqual(activations, 0)
        XCTAssertTrue(api.randomFavoritesCalls.isEmpty)
    }

    func testOfflineWithNothingCachedFailsBeforeActivating() async throws {
        network.isOnline = false
        engine.handle(.setQueue(q([5, 6], index: 0, pos: 0, play: false)))     // a queue, but nothing of it cached
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(engine)) {
            XCTAssertEqual($0 as? LarkIntentError, .offlineNothingCached)
        }
        await XCTAssertThrowsErrorAsync(try await IntentRun.resume(engine)) { XCTAssertEqual($0 as? LarkIntentError, .offlineNothingCached) }
        XCTAssertEqual(activations, 0)
        // Something cached in the queue: resume may play, shuffle still has no cached favorite.
        cache.local = [6: tmp("6.m4a")]
        try await IntentRun.resume(engine)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("6.m4a")))
        engine.pause()                                                          // a shuffle while playing is a no-op
        await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(engine)) {
            XCTAssertEqual($0 as? LarkIntentError, .offlineNothingCached)
        }
    }

    func testServerUnreachableFailsWithTheReason() async throws {
        api.refillError = URLError(.timedOut)
        await XCTAssertThrowsErrorAsync(try await IntentRun.shuffleFavorites(engine)) {
            XCTAssertEqual($0 as? LarkIntentError, .nothingToPlay(PlayerEngine.shuffleFailedNotice))
        }
    }

    func testResumeIntentPlaysTheRestoredQueue() async throws {
        engine.handle(.setQueue(q([5, 6], index: 1, pos: 7000, play: false)))
        makeEngine()
        try await IntentRun.resume(engine)
        XCTAssertEqual(backend.loads.last?.1, 7000)
        XCTAssertEqual(engine.current?.id, "6")
        XCTAssertTrue(engine.playing)
    }
}
