import XCTest
import SwiftUI
@testable import Lark

/// A cached favorite with its own title and artist (the list is in title order).
func cachedFavorite(_ id: Int, _ title: String, artist: String = "A", durationMs: Int = 200_000) -> (Track, JSONValue) {
    let t = Track(id: id, title: title, artist: artist, album: "B", duration_ms: durationMs, favorite: true)
    let json: JSONValue = .object(["id": .int(id), "title": .string(title), "artist": .string(artist), "album": .string("B"),
                                   "duration_ms": .int(durationMs), "favorite": .bool(true)])
    return (t, json)
}

/// The native offline player's model: the cached favorites as a list, and the engine driven through the same
/// messages the web sends, its state read back from the same events. No network anywhere.
final class OfflinePlayerModelTests: EngineTestCase {
    var model: OfflinePlayerModel!
    var retries = 0

    override func setUp() async throws {
        try await super.setUp()
        network.isOnline = false
        retries = 0
    }

    /// The model as `AppServices.openOfflinePlayer` wires it: the engine's events reach it, then it is seeded.
    func makeModel() {
        model = OfflinePlayerModel(player: engine, favorites: { [unowned self] in self.cache.cachedFavorites() },
                                   retry: { [unowned self] in self.retries += 1 })
        engine.onEvent = { [unowned self] e in
            self.sent.append(e)
            self.model.receive(e)
        }
        engine.emitAll()
    }

    func useFavorites(_ favs: [(Track, JSONValue)]) {
        cache.favorites = favs
        for (t, _) in favs { cache.local[t.id] = tmp("\(t.id).m4a") }
    }

    func testTheListIsTheCachedFavoritesInTitleOrderWithNoNetwork() {
        useFavorites([cachedFavorite(3, "cello"), cachedFavorite(1, "Banana"), cachedFavorite(2, "apple")])
        makeModel()
        XCTAssertEqual(model.favorites.map(\.id), ["2", "1", "3"])
        XCTAssertEqual(model.favorites.map(\.title), ["apple", "Banana", "cello"])
        XCTAssertEqual(api.favoritesCalls, 0)
        XCTAssertTrue(api.randomFavoritesCalls.isEmpty)
        XCTAssertTrue(backend.loads.isEmpty)                  // opening it plays nothing by itself
        XCTAssertNil(model.current)
    }

    func testReloadPicksUpFavoritesCachedSince() {
        useFavorites([cachedFavorite(1, "A")])
        makeModel()
        useFavorites([cachedFavorite(1, "A"), cachedFavorite(2, "B")])
        model.reload()
        XCTAssertEqual(model.favorites.map(\.id), ["1", "2"])
    }

    func testTappingASongPlaysItWithTheListAsTheQueue() {
        useFavorites([cachedFavorite(1, "A"), cachedFavorite(2, "B"), cachedFavorite(3, "C")])
        makeModel()
        model.play(model.favorites[1])
        XCTAssertEqual(engine.music.items.map(\.id), ["1", "2", "3"])
        XCTAssertEqual(engine.music.index, 1)
        XCTAssertEqual(engine.music.source, .favorites)
        XCTAssertEqual(backend.loads.count, 1)
        XCTAssertEqual(backend.loads.last?.2, true)           // autoplay
        XCTAssertEqual(model.current?.id, "2")
        XCTAssertEqual(model.queue.map(\.id), ["1", "2", "3"])
        XCTAssertEqual(model.upNext.map(\.item.id), ["3"])
        XCTAssertEqual(model.upNext.map(\.index), [2])
    }

    /// The engine's (and the web's) rule for a new list with shuffle on: what follows the chosen song is shuffled.
    func testTappingWithShuffleOnStartsTheChosenSongAndShufflesWhatFollows() {
        useFavorites((1...6).map { cachedFavorite($0, "T\($0)") })
        engine.setModes(shuffle: true, repeatMode: .off)
        makeModel()
        model.play(model.favorites[2])
        XCTAssertEqual(engine.current?.id, "3")
        XCTAssertEqual(engine.music.index, 2)
        XCTAssertEqual(Set(engine.music.upcoming.map(\.id)), ["4", "5", "6"])
        XCTAssertEqual(engine.modes.original, ["4", "5", "6"])
        XCTAssertEqual(model.upNext.map(\.item.id), engine.music.upcoming.map(\.id))   // the engine's order, not the list's
    }

    func testTappingAnUpNextEntryJumpsWithoutRebuildingTheQueue() {
        useFavorites([cachedFavorite(1, "A"), cachedFavorite(2, "B"), cachedFavorite(3, "C")])
        makeModel()
        model.play(model.favorites[0])
        let before = engine.music.items
        model.jump(to: 2)
        XCTAssertEqual(engine.music.items, before)
        XCTAssertEqual(engine.music.index, 2)
        XCTAssertEqual(model.current?.id, "3")
        XCTAssertEqual(backend.loads.count, 2)
        XCTAssertTrue(model.upNext.isEmpty)
    }

    func testModesReflectTheEngine() {
        engine.setModes(shuffle: true, repeatMode: .one)        // set elsewhere (the web, the lock screen) before opening
        makeModel()
        XCTAssertTrue(model.shuffle)
        XCTAssertEqual(model.repeatMode, .one)

        engine.setModes(shuffle: false, repeatMode: .all)       // changed by the car meanwhile
        XCTAssertFalse(model.shuffle)
        XCTAssertEqual(model.repeatMode, .all)

        model.toggleShuffle()
        XCTAssertTrue(engine.modes.shuffle)
        XCTAssertTrue(model.shuffle)
        XCTAssertEqual(engine.modes.repeatMode, .all)           // the other mode is left alone

        model.cycleRepeat()                                     // all → one → off → all, as the web's button
        XCTAssertEqual(engine.modes.repeatMode, .one)
        XCTAssertEqual(model.repeatMode, .one)
        model.cycleRepeat()
        XCTAssertEqual(engine.modes.repeatMode, .off)
        model.cycleRepeat()
        XCTAssertEqual(engine.modes.repeatMode, .all)
        XCTAssertTrue(engine.modes.shuffle)
    }

    func testTransportDrivesTheEngine() {
        useFavorites([cachedFavorite(1, "A"), cachedFavorite(2, "B"), cachedFavorite(3, "C")])
        makeModel()
        model.play(model.favorites[0])
        backend.start()
        XCTAssertTrue(model.playing)

        model.togglePlay()
        XCTAssertFalse(engine.playing)
        XCTAssertFalse(model.playing)
        model.togglePlay()
        XCTAssertTrue(engine.playing)
        XCTAssertTrue(model.playing)

        model.next()
        XCTAssertEqual(engine.music.index, 1)
        XCTAssertEqual(model.current?.id, "2")
        model.previous()                                        // near the start: the one before
        XCTAssertEqual(engine.music.index, 0)
        XCTAssertEqual(model.current?.id, "1")

        model.seek(to: 42_000)
        XCTAssertTrue(backend.calls.contains("seek:42000"))
        XCTAssertEqual(model.positionMs, 42_000)
    }

    func testProgressFollowsTheEngine() {
        useFavorites([cachedFavorite(1, "A", durationMs: 180_000)])
        makeModel()
        model.play(model.favorites[0])
        backend.durationMs = 181_000
        backend.start()
        backend.play(to: 5_000)
        XCTAssertEqual(model.positionMs, 5_000)
        XCTAssertEqual(model.durationMs, 181_000)
    }

    func testEpisodeStatesLeaveTheMusicAlone() {
        useFavorites([cachedFavorite(1, "A")])
        makeModel()
        model.play(model.favorites[0])
        backend.start()
        model.receive(.state(StateEvent(kind: .episode, itemId: "abcdefghijk", index: 0, playing: false, positionMs: 9,
                                        durationMs: 10, buffering: false, error: nil, rate: 1)))
        model.receive(.queue(kind: .episode, items: [episode("abcdefghijk")], index: 0, source: .list))
        XCTAssertEqual(model.current?.id, "1")
        XCTAssertTrue(model.playing)
        XCTAssertEqual(model.queue.map(\.id), ["1"])
    }

    func testArtworkIsTheCachedCoverOrNothing() {
        let cover = tmp("covers/1")
        model = OfflinePlayerModel(player: engine, favorites: { [] }, retry: {}, artwork: { $0 == 1 ? cover : nil })
        XCTAssertEqual(model.artworkURL(track(1)), cover)
        XCTAssertNil(model.artworkURL(track(2)))
        XCTAssertNil(model.artworkURL(episode("abcdefghijk")))
    }

    func testRetryAsksForTheServer() {
        makeModel()
        model.retry()
        XCTAssertEqual(retries, 1)
        XCTAssertTrue(model.connecting)
        model.retryFailed()
        XCTAssertFalse(model.connecting)
        XCTAssertTrue(model.stillOffline)
        model.retry()
        XCTAssertTrue(model.connecting)
        XCTAssertFalse(model.stillOffline)
    }

    func testTimeLabels() {
        XCTAssertEqual(OfflinePlayerModel.time(0), "0:00")
        XCTAssertEqual(OfflinePlayerModel.time(65_400), "1:05")
        XCTAssertEqual(OfflinePlayerModel.time(3_725_000), "1:02:05")
        XCTAssertEqual(OfflinePlayerModel.time(-5), "0:00")
    }
}

/// The offline player as the app opens it from the 无法连接服务器 overlay.
@MainActor final class OfflinePlayerAppTests: XCTestCase {
    let server = URL(string: "https://page.lark.test/")!

    func makeServices(network: FakeNetwork, cache: FakeCache, backend: FakeBackend) throws -> AppServices {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        defaults.set(server.absoluteString, forKey: AppServices.serverURLKey)
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "3")
        return AppServices(defaults: defaults, secrets: secrets, session: stubSession(),
                           dataDirectory: FileManager.default.temporaryDirectory.appendingPathComponent("offline-\(UUID().uuidString)"),
                           backend: backend, cache: cache, network: network)
    }

    func offlineServices() throws -> (AppServices, FakeBackend) {
        let cache = FakeCache()
        cache.favorites = [cachedFavorite(1, "A"), cachedFavorite(2, "B")]
        cache.local = [1: URL(fileURLWithPath: "/tmp/1.m4a"), 2: URL(fileURLWithPath: "/tmp/2.m4a")]
        let network = FakeNetwork(); network.isOnline = false
        let backend = FakeBackend()
        let s = try makeServices(network: network, cache: cache, backend: backend)
        s.eventSink = { _ in }
        s.pageDidFail(URLError(.notConnectedToInternet))
        return (s, backend)
    }

    func testPlayOfflineFavoritesOpensTheOfflinePlayerAndPlays() async throws {
        let (s, _) = try offlineServices()
        XCTAssertNil(s.offlinePlayer)
        await s.playOfflineFavorites()
        let model = try XCTUnwrap(s.offlinePlayer)
        XCTAssertTrue(try XCTUnwrap(s.engine).playing)
        XCTAssertEqual(model.favorites.map(\.id), ["1", "2"])
        XCTAssertEqual(model.current?.id, s.engine?.current?.id)
        XCTAssertNotNil(model.current)
    }

    func testOpeningWhilePlayingKeepsTheSongAndShowsIt() async throws {
        let (s, backend) = try offlineServices()
        let engine = try XCTUnwrap(s.engine)
        engine.handle(.setQueue(SetQueue(kind: .track, items: [Item(track: cachedFavorite(2, "B").0, json: cachedFavorite(2, "B").1)],
                                         index: 0, positionMs: 0, play: true, source: .list)))
        backend.start()
        let loads = backend.loads.count
        await s.playOfflineFavorites()
        XCTAssertEqual(backend.loads.count, loads)
        XCTAssertEqual(s.offlinePlayer?.current?.id, "2")
        XCTAssertEqual(s.offlinePlayer?.playing, true)
    }

    func testTheOfflinePlayerFollowsTheEngine() async throws {
        let (s, _) = try offlineServices()
        await s.playOfflineFavorites()
        let model = try XCTUnwrap(s.offlinePlayer)
        s.engine?.setModes(shuffle: true, repeatMode: .one)     // e.g. from the lock screen
        XCTAssertTrue(model.shuffle)
        XCTAssertEqual(model.repeatMode, .one)
    }

    func testRetryConnectionReturnsToTheWebAndPlaybackGoesOn() async throws {
        let (s, backend) = try offlineServices()
        var loads: [URL] = []
        s.loadPage = { loads.append($0) }
        await s.playOfflineFavorites()
        backend.start()
        let model = try XCTUnwrap(s.offlinePlayer)
        let callsBefore = backend.calls, loadsBefore = backend.loads.count

        model.retry()                                           // still down
        XCTAssertEqual(loads, [server])
        XCTAssertTrue(model.connecting)
        XCTAssertNotNil(s.offlinePlayer)
        s.pageDidFail(URLError(.cannotConnectToHost))
        XCTAssertTrue(s.pageFailed)
        XCTAssertFalse(model.connecting)
        XCTAssertTrue(model.stillOffline)
        XCTAssertTrue(s.offlinePlayer === model)

        model.retry()                                           // the server answers: back to the web UI
        XCTAssertEqual(loads.count, 2)
        s.pageDidLoad()
        XCTAssertNil(s.offlinePlayer)
        XCTAssertFalse(s.pageFailed)
        let engine = try XCTUnwrap(s.engine)
        XCTAssertTrue(engine.playing)                           // the engine never stopped
        XCTAssertEqual(backend.calls, callsBefore)
        XCTAssertEqual(backend.loads.count, loadsBefore)
    }

    func testCloseGoesBackToTheErrorScreen() async throws {
        let (s, _) = try offlineServices()
        await s.playOfflineFavorites()
        s.closeOfflinePlayer()
        XCTAssertNil(s.offlinePlayer)
        XCTAssertTrue(s.pageFailed)
        XCTAssertTrue(try XCTUnwrap(s.engine).playing)
    }

    /// Lays the screen out: nothing playing with no favorites, then playing with a queue, at the largest text size.
    func testTheViewLaysOutEmptyAndPlaying() async throws {
        let (s, _) = try offlineServices()
        let engine = try XCTUnwrap(s.engine)
        func layOut(_ model: OfflinePlayerModel) {
            let view = OfflinePlayerView(model: model, server: server, onClose: {})
                .environment(\.dynamicTypeSize, .accessibility3)
            let host = UIHostingController(rootView: view)
            host.view.frame = CGRect(x: 0, y: 0, width: 390, height: 844)
            host.view.layoutIfNeeded()                          // builds the body: no crash, no missing key path
        }
        layOut(OfflinePlayerModel(player: engine, favorites: { [] }, retry: {}))
        await s.playOfflineFavorites()
        layOut(try XCTUnwrap(s.offlinePlayer))
    }

    func testTheOfflineStringsAreTranslated() {
        let cases: [(String, String)] = [
            ("Offline Mode", "离线模式"),
            ("Retry Connection", "重试连接"),
            ("Up Next", "播放队列"),
            ("Offline Favorites", "离线收藏"),
            ("Shuffle", "随机播放"),
            ("Repeat One", "单曲循环"),
            ("Previous", "上一首"),
            ("Next", "下一首"),
        ]
        for (en, zh) in cases {
            XCTAssertEqual(localized(en, "en"), en, en)
            XCTAssertEqual(localized(en, "zh-Hans"), zh, en)
            XCTAssertEqual(localized(en, "zh-Hant"), zh, en)
        }
    }
}

/// Covers of cached tracks, kept beside the audio for the offline player (no network to ask then).
final class CacheCoverTests: CacheTestCase {
    let jpeg = Data([0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3])

    func testADownloadAlsoKeepsTheCover() async throws {
        api.artworkAnswers["1"] = jpeg
        let (t, json) = trackModel(1)
        cache.download(t, meta: json, favorite: true)
        try await settle(downloads: 1)
        let url = try XCTUnwrap(cache.artworkURL(trackID: 1))
        XCTAssertEqual(try Data(contentsOf: url), jpeg)
        XCTAssertEqual(api.artworkCalls, ["1"])
    }

    func testNoCoverIsAskedOnceAndShowsNothing() async throws {
        api.artworkErrors["1"] = LarkError.http(status: 404, code: nil)
        try put(1, bytes: 10, fav: true, at: t0)
        await cache.cacheCover(trackID: 1)
        await cache.cacheCover(trackID: 1)
        XCTAssertNil(cache.artworkURL(trackID: 1))
        XCTAssertEqual(api.artworkCalls, ["1"])                  // remembered: not asked on every sync
    }

    func testANetworkErrorIsAskedAgainLater() async throws {
        api.artworkErrors["1"] = URLError(.notConnectedToInternet)
        try put(1, bytes: 10, fav: true, at: t0)
        await cache.cacheCover(trackID: 1)
        XCTAssertNil(cache.artworkURL(trackID: 1))
        api.artworkErrors = [:]; api.artworkAnswers["1"] = jpeg
        await cache.cacheCover(trackID: 1)
        XCTAssertNotNil(cache.artworkURL(trackID: 1))
        XCTAssertEqual(api.artworkCalls, ["1", "1"])
    }

    func testAnUncachedTrackHasNoCoverAndFetchesNone() async throws {
        api.artworkAnswers["9"] = jpeg
        await cache.cacheCover(trackID: 9)
        XCTAssertNil(cache.artworkURL(trackID: 9))
        XCTAssertTrue(api.artworkCalls.isEmpty)
    }

    func testTheCoverGoesWithItsTrack() async throws {
        api.artworkAnswers = ["1": jpeg, "2": jpeg]
        try put(1, bytes: 100, fav: false, at: t0); try put(2, bytes: 100, fav: true, at: t0 + 1)
        await cache.cacheCover(trackID: 1); await cache.cacheCover(trackID: 2)
        let cover1 = try XCTUnwrap(cache.artworkURL(trackID: 1))
        cache.capBytes = 100                                     // evicts 1
        XCTAssertNil(cache.artworkURL(trackID: 1))
        XCTAssertFalse(FileManager.default.fileExists(atPath: cover1.path))
        XCTAssertNotNil(cache.artworkURL(trackID: 2))
        cache.clear()
        XCTAssertNil(cache.artworkURL(trackID: 2))
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.appendingPathComponent("covers").path))
    }

    func testAnOrphanCoverIsRemovedAtLaunchAndAKeptOneSurvives() async throws {
        api.artworkAnswers = ["1": jpeg]
        try put(1, bytes: 10, fav: true, at: t0)
        await cache.cacheCover(trackID: 1)
        let orphan = root.appendingPathComponent("covers/77")
        try jpeg.write(to: orphan)
        cache = makeCache()
        XCTAssertNotNil(cache.artworkURL(trackID: 1))
        XCTAssertFalse(FileManager.default.fileExists(atPath: orphan.path))
    }

    func testTheSyncFetchesMissingCoversOfCachedFavorites() async throws {
        api.artworkAnswers = ["1": jpeg, "2": jpeg]
        try put(1, bytes: 10, fav: true, at: t0)                 // cached before covers were kept
        api.favoritesAnswer = [trackModel(1), trackModel(2)]
        await FavoritesSync(api: api, cache: cache, network: network).run()
        XCTAssertNotNil(cache.artworkURL(trackID: 1))
        XCTAssertNotNil(cache.artworkURL(trackID: 2))
    }

    func testTheSyncFetchesNoCoversOffWiFi() async throws {
        api.artworkAnswers = ["1": jpeg]
        try put(1, bytes: 10, fav: true, at: t0)
        network.isExpensive = true
        api.favoritesAnswer = [trackModel(1)]
        await FavoritesSync(api: api, cache: cache, network: network).run()
        XCTAssertTrue(api.artworkCalls.isEmpty)
    }
}
