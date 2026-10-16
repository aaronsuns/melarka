import XCTest
@testable import Lark

/// Stay Offline (a flight): offline whatever the phone's network says, nothing tried until it is turned off.
@MainActor final class StayOfflineTests: EngineTestCase {
    func testTheSwitchIsOfflineWhateverThePathSays() {
        let path = FakeNetwork(); path.answered = false
        let net = SwitchableNetworkStatus(path)
        var changes = 0
        net.observe { changes += 1 }
        XCTAssertTrue(net.isOnline)
        XCTAssertFalse(net.answered)
        net.stayOffline = true
        XCTAssertFalse(net.isOnline)
        XCTAssertTrue(net.answered)                       // a cold launch waits for nothing
        XCTAssertEqual(changes, 1)
        path.isOnline = false; path.isOnline = true       // the plane's Wi-Fi comes and goes: still offline
        XCTAssertFalse(net.isOnline)
        XCTAssertEqual(changes, 3)                        // the path's changes are passed on too
        net.stayOffline = false
        XCTAssertTrue(net.isOnline)
        XCTAssertEqual(changes, 4)
    }

    // MARK: The engine sends nothing offline

    func testTheQueueIsNotSentOfflineAndGoesOutWhenTheNetworkIsBack() async {
        cache.local = [7: tmp("7.m4a"), 8: tmp("8.m4a")]
        network.isOnline = false
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: true)))
        backend.start()
        scheduler.advance(1); await engine.idle()
        advanceClock(60); backend.play(to: 60_000); engine.tick(); await engine.idle()
        XCTAssertEqual(api.saveCalls, [])
        network.isOnline = true
        await engine.idle()
        XCTAssertEqual(api.saveCalls.count, 1)            // once, the queue as it is now
        XCTAssertEqual(api.saveCalls.last?.trackIDs, [7, 8])
    }

    func testPlayEventsWaitOfflineAndAreSentOnline() async {
        network.isOnline = false
        events.add(trackId: 7, startedAt: 1, playedSeconds: 60, skipped: false, quality: "high")
        await engine.idle()
        advanceClock(60); engine.tick(); await engine.idle()
        advanceClock(60); engine.tick(); await engine.idle()
        XCTAssertEqual(api.postedEvents.count, 0)
        network.isOnline = true
        advanceClock(60); engine.tick(); await engine.idle()
        XCTAssertEqual(api.postedEvents.count, 1)
    }

    // MARK: The page

    func makeServices(defaults: UserDefaults, network: FakeNetwork) -> AppServices {
        defaults.set("https://page.lark.test/", forKey: AppServices.serverURLKey)
        let secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "3")
        return AppServices(defaults: defaults, secrets: secrets, session: stubSession(),
                           dataDirectory: dir.appendingPathComponent("page-\(UUID().uuidString)"),
                           backend: FakeBackend(), cache: FakeCache(), network: network)
    }

    func testThePageIsNotLoadedUntilStayOfflineIsTurnedOff() throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        let network = FakeNetwork()
        let s = makeServices(defaults: defaults, network: network)
        var loads: [URL] = []
        s.loadPage = { loads.append($0) }
        s.setStayOffline(true)
        XCTAssertTrue(s.pageFailed)
        XCTAssertNotNil(s.offlinePlayer)
        s.retryPage()
        network.isOnline = false; network.isOnline = true  // the plane's Wi-Fi comes and goes
        XCTAssertEqual(loads, [])
        s.setStayOffline(false)
        XCTAssertEqual(loads, [URL(string: "https://page.lark.test/")!])
    }

    func testStayOfflineIsKeptAcrossLaunchesUntilTurnedOff() throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        makeServices(defaults: defaults, network: FakeNetwork()).setStayOffline(true)
        let relaunched = makeServices(defaults: defaults, network: FakeNetwork())
        XCTAssertTrue(relaunched.stayOffline)
        XCTAssertTrue(relaunched.pageFailed)
        relaunched.start()
        XCTAssertNotNil(relaunched.offlinePlayer)        // straight to the offline player
        relaunched.setStayOffline(false)
        XCTAssertFalse(makeServices(defaults: defaults, network: FakeNetwork()).stayOffline)
    }
}
