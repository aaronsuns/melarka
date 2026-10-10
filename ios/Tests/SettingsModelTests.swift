import XCTest
import WebKit
@testable import Lark

/// The settings sheet's model, and what it drives in `AppServices`: the cache cap, the cache buttons, the
/// server change, and the guide.
@MainActor final class SettingsModelTests: XCTestCase {
    var dir: URL!
    var network: FakeNetwork!
    var backend: FakeBackend!
    var defaults: UserDefaults!
    var secrets: MemorySecretStore!
    static let server = URL(string: "https://settings.lark.test/")!

    override func setUp() async throws {
        StubURLProtocol.reset()
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("settings-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        network = FakeNetwork(); backend = FakeBackend()
        defaults = try XCTUnwrap(UserDefaults(suiteName: "lark.tests.\(UUID().uuidString)"))
        secrets = MemorySecretStore()
        secrets.set("token", "T1"); secrets.set("userId", "5")
        defaults.set(Self.server.absoluteString, forKey: AppServices.serverURLKey)
    }

    override func tearDown() async throws {
        StubURLProtocol.reset()
        try? FileManager.default.removeItem(at: dir)
    }

    /// A launch of the app: the real per-user cache under `dir/caches`, everything else fake.
    func makeServices() -> AppServices {
        AppServices(defaults: defaults, secrets: secrets, session: stubSession(), dataDirectory: dir.appendingPathComponent("data"),
                    backend: backend, network: network, cacheRoot: dir.appendingPathComponent("caches"))
    }

    func testCapChoicePersistsAndAppliesToCache() throws {
        XCTAssertEqual(SettingsModel.capChoices, [1, 2, 5, 10])
        let s = makeServices()
        let m = SettingsModel(services: s)
        XCTAssertEqual(m.capGB, 2)                                     // the default
        XCTAssertEqual(s.cacheStore?.capBytes, 2 << 30)
        m.setCap(5)
        XCTAssertEqual(m.capGB, 5)
        XCTAssertEqual(defaults.integer(forKey: "lark.cacheCapGB"), 5)
        XCTAssertEqual(s.cacheStore?.capBytes, 5 << 30)
        m.setCap(3)                                                    // not a choice: ignored
        XCTAssertEqual(m.capGB, 5)
        XCTAssertEqual(defaults.integer(forKey: "lark.cacheCapGB"), 5)
        // A relaunch reads it back.
        let again = makeServices()
        XCTAssertEqual(again.cacheStore?.capBytes, 5 << 30)
        XCTAssertEqual(SettingsModel(services: again).capGB, 5)
    }

    func testCapWithNobodySignedInIsStillSaved() throws {
        secrets.set("token", nil); secrets.set("userId", nil)
        let s = makeServices()
        XCTAssertNil(s.cacheStore)
        s.setCacheCap(gb: 10)
        XCTAssertEqual(s.cacheCapGB, 10)
        XCTAssertEqual(defaults.integer(forKey: AppServices.cacheCapKey), 10)
    }

    func testUsageShowsUsedAndCap() throws {
        let s = makeServices()
        let m = SettingsModel(services: s)
        let store = try XCTUnwrap(s.cacheStore)
        let file = dir.appendingPathComponent("in.m4a")
        try Data(repeating: 1, count: 1000).write(to: file)
        let (t, json) = trackModel(9)
        try store.store(trackID: 9, file: file, track: t, meta: json, favorite: true)
        m.refresh()
        XCTAssertEqual(m.usedBytes, 1000)
        XCTAssertEqual(m.capBytes, 2 << 30)
        XCTAssertTrue(m.usageText.contains("2 GB"), m.usageText)
    }

    func testClearCacheGoesThroughTheStore() throws {
        let s = makeServices()
        let m = SettingsModel(services: s)
        let store = try XCTUnwrap(s.cacheStore)
        let file = dir.appendingPathComponent("in.m4a")
        try Data(repeating: 1, count: 1000).write(to: file)
        let (t, json) = trackModel(9)
        try store.store(trackID: 9, file: file, track: t, meta: json, favorite: true)
        XCTAssertNotNil(s.cache.localURL(trackID: 9))
        m.clearCache()
        XCTAssertNil(s.cache.localURL(trackID: 9))
        XCTAssertEqual(m.usedBytes, 0)
    }

    func testSyncNowRunsTheFavoritesSync() async throws {
        let page = Data(#"{"items":[{"id":2,"title":"T2","artist":"A","album":"B","duration_ms":1000,"favorite":true}],"next_cursor":""}"#.utf8)
        StubURLProtocol.handler = { r in
            let path = r.url?.path ?? ""
            if path == "/api/v1/tracks" { return (.ok(), page) }
            if path.hasSuffix("/stream") { return (.ok(["Content-Type": "audio/mp4"]), Data(repeating: 1, count: 50)) }
            return (.status(404), Data())
        }
        let s = makeServices()
        let m = SettingsModel(services: s)
        m.syncNow()
        try await waitUntil { s.cache.localURL(trackID: 2) != nil }
    }

    func testChangeServerSignsOutStopsPlaybackAndForgetsTheServer() async throws {
        let s = makeServices()
        let cookies = s.web(for: Self.server).configuration.websiteDataStore.httpCookieStore
        let props: [HTTPCookiePropertyKey: Any] = [.name: "lark_token", .value: "T1", .domain: "settings.lark.test", .path: "/",
                                                   HTTPCookiePropertyKey("HttpOnly"): "TRUE"]
        try await cookies.setCookieAndWait(try XCTUnwrap(HTTPCookie(properties: props)))
        try await cookies.setCookieAndWait(try XCTUnwrap(HTTPCookie(properties: [.name: "pref", .value: "x", .domain: "settings.lark.test", .path: "/"])))
        let engine = try XCTUnwrap(s.engine)
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        XCTAssertTrue(engine.playing)
        s.showSettings = true

        await SettingsModel(services: s).changeServer()

        XCTAssertFalse(engine.playing)
        XCTAssertTrue(backend.calls.contains("stop"))
        XCTAssertNil(s.serverURL)                                      // back to ServerURLView
        XCTAssertNil(defaults.string(forKey: AppServices.serverURLKey))
        XCTAssertNil(s.auth.token)
        XCTAssertNil(secrets.get("token"))
        XCTAssertFalse(s.showSettings)
        XCTAssertNil(s.engine)                                         // no server, no engine
        XCTAssertNil(s.intentPlayer)
        var left: [HTTPCookie] = []                                    // cookies and token gone
        try await waitUntil(timeout: 30, state: { "left: \(left.map(\.name))" }) {
            left = await cookies.allCookies().filter { $0.domain == "settings.lark.test" }
            return left.isEmpty
        }
        XCTAssertTrue(engine.music.items.isEmpty)                      // the old server's queue is wiped
    }

    func testANewServerGetsAFreshEngineWithItsOwnAPI() async throws {
        let s = makeServices()
        let old = try XCTUnwrap(s.engine)
        XCTAssertEqual((old.client as? LarkAPI)?.base, Self.server)
        old.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        let other = URL(string: "https://other.lark.test/")!
        await s.setServerURL(other)
        let fresh = try XCTUnwrap(s.engine)
        XCTAssertFalse(fresh === old)
        XCTAssertEqual((fresh.client as? LarkAPI)?.base, other)
        XCTAssertFalse(old.playing)                                    // the old server's playback stopped
        XCTAssertNil(s.auth.token)                                     // the old server's token never goes to the new one
        XCTAssertTrue(fresh.music.items.isEmpty)
        XCTAssertTrue(backend.delegate === fresh)
    }

    /// The old origin's web state (local storage here) goes with the server.
    func testChangeServerRemovesTheOldServersWebsiteData() async throws {
        try await ColdStart.webKit()
        let s = makeServices()
        let web = s.web(for: Self.server)
        let loaded = expectation(description: "loaded")
        let nav = OneShotNavDelegate { loaded.fulfill() }
        web.navigationDelegate = nav
        web.loadHTMLString("<html><body>x</body></html>", baseURL: Self.server)   // no request: the origin only
        await fulfillment(of: [loaded], timeout: 10)
        _ = try await web.evaluateJavaScript("localStorage.setItem('k', 'v'); 1")
        let types: Set<String> = [WKWebsiteDataTypeLocalStorage]
        func records() async -> [String] {
            await WKWebsiteDataStore.default().dataRecords(ofTypes: types).map(\.displayName)
                .filter { AppServices.record($0, covers: "settings.lark.test") }
        }
        try await waitUntil { await !records().isEmpty }
        await s.changeServer()
        let left = await records()
        XCTAssertTrue(left.isEmpty, "\(left)")
    }

    /// The change disables the sheet while it signs out, and a second one does nothing.
    func testChangeServerIsMarkedWhileItRunsAndDoesNotReenter() async throws {
        StubURLProtocol.handler = { _ in Thread.sleep(forTimeInterval: 0.4); return (.ok(), Data(#"{"accepted":1}"#.utf8)) }
        let s = makeServices()
        let engine = try XCTUnwrap(s.engine)
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.start()
        backend.play(to: 40_000)                                        // a listen worth posting on the way out
        XCTAssertFalse(s.changingServer)
        let first = Task { await s.changeServer() }
        try await waitUntil { s.changingServer }
        let t0 = Date()
        await s.changeServer()                                          // re-entry: returns at once
        XCTAssertLessThan(Date().timeIntervalSince(t0), 0.3)
        XCTAssertTrue(s.changingServer)
        await first.value
        XCTAssertFalse(s.changingServer)
        XCTAssertNil(s.serverURL)
        XCTAssertEqual(StubURLProtocol.requests.filter { $0.url?.path == "/api/v1/events/play" }.count, 1)   // flushed once
    }

    /// Switching straight to another server flushes the old server's pending events first.
    func testSwitchingServersFlushesThePendingEventsFirst() async throws {
        StubURLProtocol.handler = { _ in (.ok(), Data(#"{"accepted":1}"#.utf8)) }
        let s = makeServices()
        let engine = try XCTUnwrap(s.engine)
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true)))
        backend.start()
        backend.play(to: 40_000)
        await s.setServerURL(URL(string: "https://next.lark.test/")!)
        let posts = StubURLProtocol.requests.filter { $0.url?.path == "/api/v1/events/play" }
        XCTAssertEqual(posts.map { $0.url?.host }, ["settings.lark.test"])
        XCTAssertEqual(posts.first?.value(forHTTPHeaderField: "Authorization"), "Bearer T1")
    }

    func testTheTestButtonShowsWhyNothingPlays() async throws {
        network.isOnline = false
        let s = makeServices()
        let m = SettingsModel(services: s)
        await m.testShuffle()
        XCTAssertEqual(m.message, LarkIntentError.offlineNothingCached.text)
        secrets.set("token", nil); secrets.set("userId", nil)
        let out = SettingsModel(services: makeServices())
        await out.testShuffle()
        XCTAssertEqual(out.message, LarkIntentError.notSignedIn.text)
    }

    func testPrefsSurviveARelaunch() throws {
        let s = makeServices()
        s.handle(.setPrefs(NativePrefs(quality: "saver", carLyrics: false)))
        XCTAssertEqual(s.engine?.prefs, NativePrefs(quality: "saver", carLyrics: false))
        let again = makeServices()
        XCTAssertEqual(again.engine?.prefs, NativePrefs(quality: "saver", carLyrics: false))
    }

    /// The loudness switch too, so a cold start from the car plays at the level the user chose.
    func testLoudnessPrefSurvivesARelaunch() throws {
        let s = makeServices()
        s.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: false)))
        XCTAssertEqual(makeServices().engine?.prefs, NativePrefs(quality: "high", carLyrics: true, loudness: false))
        s.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: true)))
        XCTAssertEqual(makeServices().engine?.prefs.loudness, true)
    }

    func testVersionTextHasVersionAndBuild() {
        let info = Bundle.main.infoDictionary ?? [:]
        let v = info["CFBundleShortVersionString"] as? String ?? "?"
        let b = info["CFBundleVersion"] as? String ?? "?"
        XCTAssertEqual(SettingsModel.versionText, "\(v) (\(b))")
    }

    func testGuideTextNamesTheSteps() throws {
        let zh = try XCTUnwrap(localized("shortcutsGuide.text", "zh-Hans"))
        for s in ["快捷指令", "自动化", "蓝牙", "随机播放收藏", "立即运行"] { XCTAssertTrue(zh.contains(s), s) }
        let en = try XCTUnwrap(localized("shortcutsGuide.text", "en"))
        for s in ["Shortcuts", "Automation", "Bluetooth", "Shuffle Favorites", "Run Immediately"] { XCTAssertTrue(en.contains(s), s) }
    }

    /// The Chinese text is the agreed one, unchanged; the English one says the same.
    func testGuideTextIsTheAgreedText() throws {
        let zh = try XCTUnwrap(localized("shortcutsGuide.text", "zh-Hans"))
        XCTAssertTrue(zh.hasPrefix("连上车载蓝牙就自动播放 Melarka 收藏\n\n方式一（大多数车不用设置）"))
        XCTAssertTrue(zh.contains("5. 点\"下一步\"（如果先看到\"新建空白自动化\"，点它，再点\"添加操作\"）→ 搜索\"Melarka\" → 选择\"随机播放收藏\"（想接着上次听，就选\"继续播放\"）。"))
        XCTAssertTrue(zh.hasSuffix("缓存大小在\"设置 → 离线缓存\"里调整。"))
        let en = try XCTUnwrap(localized("shortcutsGuide.text", "en"))
        XCTAssertTrue(en.hasPrefix("Start Melarka favorites automatically when the car's Bluetooth connects\n\nOption 1 (most cars need no setup)"))
        XCTAssertTrue(en.contains("→ search for \"Melarka\" → choose \"Shuffle Favorites\" (to continue where you left off, choose \"Resume\")."))
        XCTAssertTrue(en.hasSuffix("set the cache size in \"Settings → Offline Cache\"."))
        // The guide's "Settings → Offline Cache" is the settings sheet's own section name, in each language.
        XCTAssertEqual(localized("Offline Cache", "en"), "Offline Cache")
        XCTAssertEqual(localized("Offline Cache", "zh-Hans"), "离线缓存")
    }

    /// The settings texts that SettingsModel builds, per language.
    func testUsageTextPerLanguage() {
        XCTAssertEqual(localized("Used %@ / limit %@", "zh-Hans"), "已用 %@ / 上限 %@")
        XCTAssertEqual(localized("Not signed in · limit %@", "zh-Hans"), "未登录 · 上限 %@")
        XCTAssertEqual(localized("Not signed in · limit %@", "en"), "Not signed in · limit %@")
        XCTAssertEqual(localized("Cache cleared", "zh-Hans"), "缓存已清空")
        XCTAssertEqual(localized("Syncing favorites (downloads only on Wi‑Fi)", "zh-Hans"), "正在同步收藏（仅在 Wi‑Fi 下下载）")
    }
}
