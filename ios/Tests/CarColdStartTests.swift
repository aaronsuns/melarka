import XCTest
@testable import Lark

/// Review Focus #1 (§19.4): Lark suspended or killed by iOS, no network, the car sends play. The engine is
/// built, the music queue comes back from disk, and the current item plays from the cache before any network
/// call. With no queue, cached favorites are shuffled.
@MainActor final class CarColdStartTests: EngineTestCase {
    /// Every request the engine could make through the fake API.
    var apiCalls: Int {
        api.randomFavoritesCalls.count + api.randomTracksCalls.count + api.radioCalls.count + api.queueCalls
            + api.saveCalls.count + api.postedEvents.count + api.progressCalls.count + api.lyricsCalls.count
            + api.favoritesCalls + api.downloadCalls.count
    }

    func testPlayCommandAfterColdStartPlaysPersistedQueueFromDisk() throws {
        let store = QueueStore(directory: dir); store.user = 3
        store.save(.init(music: .init(items: [track(7), track(8)], index: 1, positionMs: 42_000, source: .favorites),
                         episodes: .empty, active: .track, savedAt: 0))
        self.store = store
        cache.local = [8: tmp("8.m4a")]; network.isOnline = false
        makeEngine()                                                   // a fresh process: restore happens in init
        var activated = 0
        engine.willStartPlayback = { activated += 1 }
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)    // what MPRemoteCommandCenter's playCommand runs
        // Synchronously, inside the handler: nothing waited on the network.
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("8.m4a")))
        XCTAssertEqual(backend.loads.last?.1, 42_000)
        XCTAssertTrue(try XCTUnwrap(backend.loads.last).2)
        XCTAssertEqual(apiCalls, 0)
        XCTAssertEqual(activated, 1)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(engine.music.source, .favorites)
    }

    func testPlayCommandWithEmptyQueueShufflesCachedFavoritesOffline() async throws {
        cache.favorites = [trackModel(1), trackModel(2), trackModel(3)]
        cache.local = [1: tmp("1.m4a"), 2: tmp("2.m4a"), 3: tmp("3.m4a")]
        network.isOnline = false
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        // Cached favorites start inside the handler too: no Task, no await.
        XCTAssertEqual(backend.loads.count, 1)
        let played = try XCTUnwrap(backend.loads.first)
        XCTAssertTrue([tmp("1.m4a"), tmp("2.m4a"), tmp("3.m4a")].map(MediaSource.file).contains(played.0))
        XCTAssertTrue(played.2)
        XCTAssertEqual(engine.music.source, .favorites)
        XCTAssertEqual(Set(engine.music.items.map(\.id)), ["1", "2", "3"])
        XCTAssertTrue(notices.contains(PlayerEngine.offlineNotice))
        XCTAssertEqual(apiCalls, 0)                                    // nothing was awaited before the sound
        // Review R2: the short favorites queue wants a refill, but offline it is not asked for at all.
        await engine.idle()
        scheduler.advance(120)
        await engine.idle()
        XCTAssertEqual(api.randomFavoritesCalls.count + api.randomTracksCalls.count + api.radioCalls.count, 0)
        // The network is back: exactly one refill, appended to the queue.
        api.randomFavoritesAnswer = ("favorites", [trackModel(20), trackModel(21)])
        network.isOnline = true
        await engine.idle()
        XCTAssertEqual(api.randomFavoritesCalls.count, 1)
        XCTAssertTrue(engine.music.items.map(\.id).contains("20"))
        XCTAssertEqual(api.randomTracksCalls.count + api.radioCalls.count, 0)
    }

    /// Review finding 5: the saved current track was streamed (not cached), and there is no network. The car's
    /// play goes straight to a cached item of the queue, never a stream first; the streamed one stays queued.
    func testPlayCommandOfflineWithUncachedCurrentPlaysACachedItemFirst() throws {
        let store = QueueStore(directory: dir); store.user = 3
        store.save(.init(music: .init(items: [track(7), track(8), track(9)], index: 0, positionMs: 30_000, source: .radio),
                         episodes: .empty, active: .track, savedAt: 0))
        self.store = store
        cache.local = [9: tmp("9.m4a")]; network.isOnline = false
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        XCTAssertEqual(backend.loads.count, 1)
        XCTAssertEqual(backend.loads.first?.0, .file(tmp("9.m4a")))
        XCTAssertTrue(backend.loads.allSatisfy { if case .file = $0.0 { return true }; return false })
        XCTAssertEqual(engine.current?.id, "9")
        let after = engine.music.items[(engine.music.index + 1)...].map(\.id)
        XCTAssertEqual(after.first, "7")                               // the streamed track, right after
        XCTAssertEqual(apiCalls, 0)
    }

    /// The same before the network monitor's first answer (a cold launch: `isOnline` is only a guess).
    func testPlayCommandBeforeTheNetworkAnswersPlaysACachedItemFirst() throws {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        cache.favorites = [trackModel(3)]; cache.local = [3: tmp("3.m4a")]
        network.answered = false
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        XCTAssertEqual(backend.loads.map(\.0), [.file(tmp("3.m4a"))])  // a cached favorite, not the stream of 7
        XCTAssertEqual(engine.current?.id, "3")
        XCTAssertTrue(engine.music.items.map(\.id).contains("7"))
    }

    /// Online and answered, an uncached current track streams as before.
    func testOnlineTheCurrentTrackStreams() throws {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        cache.local = [8: tmp("8.m4a")]
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        guard case .remote = try XCTUnwrap(backend.loads.last).0 else { return XCTFail("streams the current track") }
        XCTAssertEqual(engine.current?.id, "7")
    }

    /// Review R3: the commands are installed once and resolve the engine when a command arrives.
    func testCommandsWithNoEngineYetHaveNothingToDo() {
        let rc = RemoteCommands(center: .shared(), resolve: { nil })
        defer { rc.detach() }
        XCTAssertEqual(rc.playNow(), .noActionableNowPlayingItem)
        XCTAssertEqual(rc.pauseNow(), .noActionableNowPlayingItem)
        XCTAssertEqual(rc.toggleNow(), .noActionableNowPlayingItem)
        XCTAssertEqual(rc.nextNow(), .noActionableNowPlayingItem)
    }

    func testTheSameCommandsPlayOnTheNewServersEngine() async throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "3")
        let fakeCache = FakeCache(); fakeCache.local = [1: tmp("1.m4a"), 2: tmp("2.m4a")]
        let backend = FakeBackend()
        let s = AppServices(defaults: defaults, secrets: secrets, session: stubSession(), dataDirectory: dir.appendingPathComponent("svc"),
                            backend: backend, cache: fakeCache, network: FakeNetwork())
        let rc = s.installRemoteCommands()
        defer { rc.detach() }
        XCTAssertEqual(rc.playNow(), .noActionableNowPlayingItem)      // no server yet
        await s.setServerURL(URL(string: "https://first.lark.test/")!)
        XCTAssertEqual(rc.playNow(), .success)                         // the engine is built by the command itself
        let first = try XCTUnwrap(s.engine)
        await s.setServerURL(URL(string: "https://second.lark.test/")!)
        let second = try XCTUnwrap(s.engine)
        XCTAssertFalse(first === second)
        second.handle(.setQueue(q([2], index: 0, pos: 0, play: false)))
        XCTAssertEqual(rc.playNow(), .success)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("2.m4a")))
        XCTAssertTrue(second.playing)
        XCTAssertEqual(rc.pauseNow(), .success)
        XCTAssertFalse(second.playing)
    }

    func testShuffleFavoritesOnlineUsesAPIAndCachedFirstWhenOffline() async throws {
        // Online, nothing cached: the server's random favorites, streamed.
        api.randomFavoritesAnswer = ("favorites", [trackModel(4), trackModel(5)])
        await engine.shuffleFavorites()
        XCTAssertEqual(api.randomFavoritesCalls.count, 1)
        guard case .remote = try XCTUnwrap(backend.loads.last).0 else { return XCTFail("streams what the server chose") }
        XCTAssertEqual(engine.music.source, .favorites)

        // Cached favorites come first: offline, and online too (they start at once).
        cache.favorites = [trackModel(1)]; cache.local = [1: tmp("1.m4a")]
        for online in [false, true] {
            network.isOnline = online
            await engine.shuffleFavorites()
            XCTAssertEqual(api.randomFavoritesCalls.count, 1, "no server call when favorites are cached (online: \(online))")
            XCTAssertEqual(backend.loads.last?.0, .file(tmp("1.m4a")))
        }
    }

    func testEpisodeActiveOfflinePlaysTheMusicQueueFromTheCache() throws {
        let store = QueueStore(directory: dir); store.user = 3
        store.save(.init(music: .init(items: [track(7)], index: 0, positionMs: 5_000, source: .list),
                         episodes: .init(items: [episode("abcdefghijk")], index: 0, positionMs: 0, source: .list),
                         active: .episode, savedAt: 0))
        self.store = store
        cache.local = [7: tmp("7.m4a")]; network.isOnline = false
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("7.m4a")))
        XCTAssertEqual(backend.loads.last?.1, 5_000)
        XCTAssertEqual(engine.active, .track)
        XCTAssertEqual(apiCalls, 0)
    }

    func testNothingQueuedOrCachedOfflineSaysWhyAndAsksNoServer() async throws {
        network.isOnline = false
        makeEngine()
        XCTAssertEqual(RemoteCommands.handlePlay(engine), .success)
        await engine.idle()
        try await waitUntil { self.notices.contains(PlayerEngine.nothingCachedNotice) }
        XCTAssertTrue(backend.loads.isEmpty)
        XCTAssertEqual(apiCalls, 0)
    }

    /// The whole app graph, as after iOS launched Lark in the background for the car: `AppServices` from
    /// `UserDefaults` and the Keychain, the engine built, the queue from disk, the file from the cache, and no request.
    func testColdLaunchThroughAppServicesPlaysFromDiskWithNoRequest() throws {
        StubURLProtocol.reset()
        StubURLProtocol.handler = { _ in (.status(500), Data()) }
        defer { StubURLProtocol.reset() }
        let data = dir.appendingPathComponent("data")
        let saved = QueueStore(directory: data); saved.user = 3
        saved.save(.init(music: .init(items: [track(7), track(8)], index: 1, positionMs: 42_000, source: .favorites),
                         episodes: .empty, active: .track, savedAt: 0))
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        defaults.set("https://car.lark.test/", forKey: AppServices.serverURLKey)
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "3")
        let offline = FakeNetwork(); offline.isOnline = false
        let fakeCache = FakeCache(); fakeCache.local = [8: tmp("8.m4a")]
        let backend = FakeBackend()
        let s = AppServices(defaults: defaults, secrets: secrets, session: stubSession(), dataDirectory: data,
                            backend: backend, cache: fakeCache, network: offline)
        s.start()
        XCTAssertEqual(RemoteCommands.handlePlay(try XCTUnwrap(s.engine)), .success)
        XCTAssertEqual(backend.loads.last?.0, .file(tmp("8.m4a")))
        XCTAssertEqual(backend.loads.last?.1, 42_000)
        XCTAssertTrue(try XCTUnwrap(backend.loads.last).2)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }
}
