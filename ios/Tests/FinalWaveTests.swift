import XCTest
@testable import Lark

/// The final review's engine and intent findings (5–8).
@MainActor final class FinalWaveEngineTests: EngineTestCase {
    func remote(_ id: Int) -> MediaSource { .remote(api.streamURL(track(id), quality: "high"), token: "T1") }
    /// The shuffle's own server call (a refill asks for 20, the shuffle for `shuffleSize`).
    var shuffleCalls: Int { api.randomFavoritesCalls.filter { $0.n == PlayerEngine.shuffleSize }.count }
    var refillCalls: Int { api.randomFavoritesCalls.count + api.randomTracksCalls.count + api.radioCalls.count }

    // MARK: 5. `greeted`

    /// After a sign-out (or a user switch) the remounted page's `hello` is honoured again.
    func testHelloIsHonouredAgainAfterASignOut() async {
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(api.queueCalls, 1)
        engine.handle(.hello(onOpen: "resume"))          // a web reload: state only
        await engine.idle()
        XCTAssertEqual(api.queueCalls, 1)
        engine.reset()                                    // sign-out, or another user
        engine.handle(.hello(onOpen: "resume"))
        await engine.idle()
        XCTAssertEqual(api.queueCalls, 2)
    }

    // MARK: 6. No replay after reconnect

    func testAQueueThatEndsOfflineDoesNotReplayOnReconnect() {
        cache.local = [5: tmp("5.m4a")]
        engine.handle(.setQueue(q([5], index: 0, pos: 0, play: true)))
        backend.start()
        network.isOnline = false
        backend.finish()
        XCTAssertFalse(engine.playing)
        let loads = backend.loads.count
        network.isOnline = true
        XCTAssertEqual(backend.loads.count, loads)        // the finished track is not played again
        XCTAssertFalse(engine.playing)
    }

    func testAnEndOfflineWithOnlyStreamsLeftGoesOnToTheNextOnReconnect() {
        cache.local = [5: tmp("5.m4a")]
        engine.handle(.setQueue(q([5, 6], index: 0, pos: 0, play: true)))
        backend.start()
        network.isOnline = false
        backend.finish()                                  // 6 is not on the phone: stopped
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.loads.count, 1)
        network.isOnline = true
        XCTAssertEqual(backend.loads.last?.0, remote(6))  // the next one, not 5 again
        XCTAssertEqual(engine.current?.id, "6")
        XCTAssertTrue(engine.playing)
    }

    func testAnInterruptedStreamStillResumesOnReconnect() {
        engine.handle(.setQueue(q([5], index: 0, pos: 0, play: true)))
        backend.start()
        backend.play(to: 10_000)
        network.isOnline = false
        backend.fail(network: true)                       // the stream broke: nothing local
        XCTAssertFalse(engine.playing)
        network.isOnline = true
        XCTAssertEqual(backend.loads.last?.0, remote(5))
        XCTAssertTrue(engine.playing)
    }

    // MARK: 7. Cold offline start, nothing on the phone

    func testColdStartBeforeTheNetworkAnswersWaitsAndTakesNoSession() async throws {
        network.answered = false
        makeEngine()
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        await engine.idle()
        XCTAssertEqual(activations, 0)                    // the car's own source keeps playing
        XCTAssertEqual(api.randomFavoritesCalls.count, 0)
        XCTAssertTrue(backend.loads.isEmpty)
        api.randomFavoritesAnswer = ("favorites", [trackModel(4), trackModel(5)])
        network.answer(online: true)                      // the network turns out to be there
        await engine.idle()
        XCTAssertEqual(shuffleCalls, 1)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(activations, 1)
    }

    func testColdStartOfflineWithNothingCachedResumesWhenTheNetworkReturns() async throws {
        network.isOnline = false
        makeEngine()
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        await engine.idle()
        XCTAssertEqual(activations, 0)
        XCTAssertTrue(notices.contains(PlayerEngine.nothingCachedNotice))
        XCTAssertEqual(api.randomFavoritesCalls.count, 0)
        api.randomFavoritesAnswer = ("favorites", [trackModel(4)])
        network.isOnline = true
        await engine.idle()
        XCTAssertEqual(shuffleCalls, 1)
        XCTAssertTrue(engine.playing)
    }

    func testAWaitingColdStartIsDroppedByAnythingElse() async throws {
        network.isOnline = false
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))   // the user chose something meanwhile
        engine.pause()
        network.isOnline = true
        await engine.idle()
        XCTAssertEqual(api.randomFavoritesCalls.count, 0)
    }

    // MARK: 8. The shuffle shortcut does not cut the car's resume

    func testShuffleIntentIsANoOpWhilePlaying() async throws {
        cache.favorites = [trackModel(1)]; cache.local = [1: tmp("1.m4a"), 5: tmp("5.m4a")]
        engine.handle(.setQueue(q([5], index: 0, pos: 0, play: true)))
        let loads = backend.loads.count
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        let outcome = try await IntentRun.shuffleFavorites(engine)
        XCTAssertEqual(outcome, .alreadyPlaying)
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(activations, 0)
        XCTAssertEqual(engine.current?.id, "5")
        engine.pause()
        let started = try await IntentRun.shuffleFavorites(engine)
        XCTAssertEqual(started, .started)
        XCTAssertEqual(engine.current?.id, "1")
    }
}

/// Final review finding 1: the page could not load (offline, server down).
@MainActor final class PageLoadFailureTests: XCTestCase {
    func testWhatCountsAsAFailedPage() {
        XCTAssertTrue(PageLoad.isFailure(URLError(.notConnectedToInternet)))
        XCTAssertTrue(PageLoad.isFailure(URLError(.cannotFindHost)))
        XCTAssertTrue(PageLoad.isFailure(URLError(.timedOut)))
        XCTAssertFalse(PageLoad.isFailure(URLError(.cancelled)))             // a new navigation replaced it
        XCTAssertFalse(PageLoad.isFailure(NSError(domain: "WebKitErrorDomain", code: 102)))   // frame load interrupted
        XCTAssertTrue(PageLoad.isFailure(status: 502))
        XCTAssertTrue(PageLoad.isFailure(status: 503))
        XCTAssertFalse(PageLoad.isFailure(status: 200))
        XCTAssertFalse(PageLoad.isFailure(status: 404))
    }

    func makeServices(network: FakeNetwork, cache: FakeCache? = nil) throws -> AppServices {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        defaults.set("https://page.lark.test/", forKey: AppServices.serverURLKey)
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "3")
        return AppServices(defaults: defaults, secrets: secrets, session: stubSession(),
                           dataDirectory: FileManager.default.temporaryDirectory.appendingPathComponent("page-\(UUID().uuidString)"),
                           backend: FakeBackend(), cache: cache ?? FakeCache(), network: network)
    }

    func testAFailedPageShowsTheOverlayAndRetriesWhenTheNetworkReturns() throws {
        let network = FakeNetwork()
        let s = try makeServices(network: network)
        var loads: [URL] = []
        s.loadPage = { loads.append($0) }
        network.isOnline = false
        s.pageDidFail(URLError(.notConnectedToInternet))
        XCTAssertTrue(s.pageFailed)
        XCTAssertEqual(loads, [])                                            // still offline: nothing to retry
        network.isOnline = true
        XCTAssertFalse(s.pageFailed)
        XCTAssertEqual(loads, [URL(string: "https://page.lark.test/")!])
        network.isOnline = false; network.isOnline = true                   // loaded fine meanwhile: no more retries
        XCTAssertEqual(loads.count, 1)
    }

    func testACancelledLoadIsNotAFailure() throws {
        let s = try makeServices(network: FakeNetwork())
        s.pageDidFail(URLError(.cancelled))
        XCTAssertFalse(s.pageFailed)
    }

    func testRetryAndLoadedClearTheOverlay() throws {
        let s = try makeServices(network: FakeNetwork())
        var loads = 0
        s.loadPage = { _ in loads += 1 }
        s.pageDidFail(URLError(.cannotConnectToHost))
        s.retryPage()
        XCTAssertEqual(loads, 1)
        XCTAssertFalse(s.pageFailed)
        s.pageDidFail(URLError(.cannotConnectToHost))
        s.pageDidLoad()
        XCTAssertFalse(s.pageFailed)
    }

    func testOfflineFavoritesFromTheOverlay() async throws {
        let cache = FakeCache()
        let network = FakeNetwork(); network.isOnline = false
        let s = try makeServices(network: network, cache: cache)
        XCTAssertFalse(s.hasOfflineFavorites)
        cache.favorites = [trackModel(1)]; cache.local = [1: URL(fileURLWithPath: "/tmp/1.m4a")]
        XCTAssertTrue(s.hasOfflineFavorites)
        await s.playOfflineFavorites()
        XCTAssertTrue(try XCTUnwrap(s.engine).playing)
        XCTAssertEqual(s.engine?.current?.id, "1")
    }

    func testTheSettingsButtonOpensTheNativeSheet() throws {
        let s = try makeServices(network: FakeNetwork())
        s.pageDidFail(URLError(.cannotFindHost))
        s.openSettings()
        XCTAssertTrue(s.showSettings)
    }
}
