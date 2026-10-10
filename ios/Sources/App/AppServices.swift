import SwiftUI
import WebKit
import MediaPlayer
import UIKit
import os
import CryptoKit

/// The composition root. The intents need it, so it is the one singleton; tests build their own graph.
/// It owns the auth store, the API, the bridge and the player engine, and routes web messages between them.
@MainActor final class AppServices: ObservableObject {
    /// The app's graph: AVFoundation playback, the audio session, the lock screen and the car's buttons.
    /// Built on first access (`LarkApp.init`, or an intent or remote command in a background launch with no
    /// scene) from `UserDefaults` (server, cache cap, prefs) and the Keychain (token, user); it needs no UI.
    static let shared = AppServices(backend: AVPlayerBackend(), systemIntegration: true)
    static let serverURLKey = "serverURL"
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "app")

    @Published private(set) var serverURL: URL?
    @Published var showSettings = false
    let auth: AuthStore
    private(set) var bridge: BridgeController?
    private(set) var webView: WKWebView?
    private let defaults: UserDefaults
    private var apiClient: LarkAPI?
    private let session: URLSession
    /// The player's per-user files (queues, pending play events).
    let queueStore: QueueStore
    private let backend: MediaBackend
    /// What the engine reads: the signed-in user's cache (`userCache`), or the one a test injected.
    let cache: CacheProviding
    /// The per-user offline cache under `cacheRoot/<userId>`; nil when a test injected its own `cache`.
    private let userCache: UserCache?
    private let cacheRoot: URL
    private let network: NetworkStatus
    private var syncTask: Task<Void, Never>?
    /// Bumped when a sync is dropped (another user, sign-out), so its late end touches nothing.
    private var syncGeneration = 0
    /// A trigger came while a sync was running: one more runs after it.
    private var syncAgain = false
    /// A trigger came while off Wi-Fi: the sync runs when Wi-Fi is back.
    private var syncWanted = false
    private var lastActivationSync: Date?
    private var backgroundRefresh: BackgroundRefresh?
    /// The foreground's sync runs at most this often.
    static let activationSyncInterval: TimeInterval = 3600
    /// Settings stores the cap here, in GB (`SettingsModel.capChoices`).
    static let cacheCapKey = "lark.cacheCapGB"
    /// The web's last `setPrefs`, so a cold start (the car) streams at the chosen quality, shows car lyrics and
    /// applies the loudness gain as set.
    static let prefsQualityKey = "lark.prefs.quality"
    static let prefsCarLyricsKey = "lark.prefs.carLyrics"
    static let prefsLoudnessKey = "lark.prefs.loudness"
    var now: () -> Date = Date.init
    /// How long a sync waits for the network monitor's first answer (a cold background launch).
    var networkAnswerTimeout: TimeInterval = 3
    private var terminateObserver: NSObjectProtocol?
    /// The signed-in user's cache (Settings); nil while signed out or when a test injected `cache`.
    var cacheStore: CacheStore? { userCache?.store }
    private var engineStorage: PlayerEngine?
    /// The audio session, Now Playing and the remote commands are wired to the engine (the app; never in tests,
    /// which must not touch the process-wide command center or audio session).
    private let systemIntegration: Bool
    private var remoteCommands: RemoteCommands?
    private var audioSession: AudioSessionController?
    private var ticker: Timer?
    private var signingOut = false
    /// How long sign-out waits for its play-event POST (the web's logout waits 3 s).
    var signOutFlushTimeout: Double = 5
    /// Tests: receives what would go to the page. Production sends to the bridge.
    var eventSink: ((NativeEvent) -> Void)?
    /// The seam for the intents: tests put a fake here. Nil: the engine.
    private var engineForIntents: IntentPlayer?

    init(defaults: UserDefaults = .standard, secrets: SecretStore = KeychainSecretStore(service: BuildInfo.bundleID),
         session: URLSession = .shared, dataDirectory: URL? = nil,
         backend: MediaBackend? = nil,          // nil: SilentBackend (tests); the app passes AVPlayerBackend
         cache: CacheProviding? = nil,          // nil: the per-user CacheStore under `cacheRoot`
         network: NetworkStatus? = nil,         // nil: NWPathMonitor
         cacheRoot: URL? = nil,                 // nil: Library/Caches/lark
         systemIntegration: Bool = false) {
        self.defaults = defaults
        self.systemIntegration = systemIntegration
        self.session = session
        auth = AuthStore(secrets: secrets)
        serverURL = defaults.string(forKey: Self.serverURLKey).flatMap(ServerURLValidator.normalize)
        queueStore = QueueStore(directory: dataDirectory ?? QueueStore.defaultDirectory())
        queueStore.user = auth.userId
        self.backend = backend ?? SilentBackend()
        let userCache = cache == nil ? UserCache() : nil
        self.userCache = userCache
        self.cache = cache ?? userCache!
        self.cacheRoot = cacheRoot ?? CacheStore.defaultRoot()
        let network = network ?? PathNetworkStatus()
        self.network = network
        network.observe { [weak self] in self?.networkChanged() }
        updateCache()
        if userCache != nil {
            terminateObserver = NotificationCenter.default.addObserver(forName: UIApplication.willTerminateNotification, object: nil,
                                                                       queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.flushCache() }
            }
        }
    }

    /// The player, built on first use (it needs a server for its API). Nil before a server is set.
    var engine: PlayerEngine? {
        if let engineStorage { return engineStorage }
        guard let api = api(session: session) else { return nil }
        let nowPlaying = systemIntegration ? NowPlayingController(sink: MPNowPlayingInfoCenter.default(), artwork: { [weak api] item in
            // nil: no cover (remembered); a thrown error (offline, a 5xx) is asked again later.
            guard let api else { return nil }
            do {
                return UIImage(data: try await api.artwork(item))
            } catch LarkError.http(status: 404, _) {
                return nil
            }
        }) : nil
        let e = PlayerEngine(backend: backend, api: api, cache: cache, store: queueStore,
                             events: PlayEventQueue(api: api, store: queueStore), network: network, nowPlaying: nowPlaying)
        e.onEvent = { [weak self] in self?.send($0) }
        if let prefs = savedPrefs { e.prefs = prefs }
        engineStorage = e
        if systemIntegration {
            // Before any playback: the session category only. It is activated when sound starts, so opening
            // Lark to browse never stops another app's audio. Then the lock screen and car buttons.
            let audio = AudioSessionController(engine: e)
            audio.configure()
            audioSession = audio
        }
        remoteCommands?.attach(e)      // installed once in `start()`; the skip buttons follow this engine
        // Saves the position while playing, sends what is due (`PlayerEngine.tick`).
        ticker = Timer.scheduledTimer(withTimeInterval: 10, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.engineStorage?.tick() }
        }
        return e
    }

    /// At launch: builds the player when a server is known, so the car's play command finds its handlers
    /// even when iOS launched the app in the background. Without a server it is built on first use.
    func start() {
        // The lock screen and car buttons, once for the process: each command resolves the engine when it
        // arrives, so they work before the first engine, and across a server change, with no page loaded.
        if systemIntegration && remoteCommands == nil { installRemoteCommands() }
        _ = engine
        if systemIntegration {
            let refresh = BackgroundRefresh(run: { [weak self] in
                await self?.syncFavoritesNow()
                self?.flushCache()       // before the task completes and iOS suspends the app
            })
            refresh.register()
            refresh.schedule()
            backgroundRefresh = refresh
        }
        lastActivationSync = now()
        syncFavorites()
    }

    /// An event for the page: through the bridge (dropped while the page is frozen; re-sent on activation).
    func send(_ event: NativeEvent) {
        offlinePlayer?.receive(event)
        if let eventSink { eventSink(event) } else { bridge?.send(event) }
    }

    /// What the Shortcuts intents (and the settings' test button) drive: the engine, nil before a server is set.
    var intentPlayer: IntentPlayer? {
        get { engineForIntents ?? engine }
        set { engineForIntents = newValue }
    }

    /// The first server, or (programmatically) another one. Leaving a server stops its playback, wipes its
    /// queues and forgets its token, so the old token is never sent to the new server; the new server gets a
    /// new engine with its own API. `changeServer()` is the settings' path, which signs out properly first.
    func setServerURL(_ url: URL) async {
        if let old = serverURL, old != url {
            await changeServer()                       // signs out of the old server first (events flushed)
            guard serverURL == nil else { return }    // another change won meanwhile
        }
        defaults.set(url.absoluteString, forKey: Self.serverURLKey)
        serverURL = url
        updateCache()
        // The engine is built on first use (a remote command, an intent, or the page's `hello`).
    }

    /// The remote commands, against a resolver: the engine of the moment (built on first use), nil before a server.
    @discardableResult
    func installRemoteCommands(center: MPRemoteCommandCenter = .shared()) -> RemoteCommands {
        let rc = RemoteCommands(center: center, resolve: { [weak self] in self?.engine })
        remoteCommands = rc
        if let engineStorage { rc.attach(engineStorage) }
        return rc
    }

    /// A server change is running (the settings sheet is disabled meanwhile).
    @Published private(set) var changingServer = false

    /// Settings → 更换服务器: sign out of this server (the pending play events are sent while the token works,
    /// at most `signOutFlushTimeout`; playback stops, the queues are wiped, the token and the session cookie
    /// go), drop the engine and the web view, remove the server's website data (cookies, local storage, caches,
    /// service workers), and forget the server, so `RootView` shows `ServerURLView` again. Re-entry returns.
    func changeServer() async {
        guard let old = serverURL, !changingServer else { return }
        changingServer = true
        defer { changingServer = false }
        if engineStorage == nil, auth.token != nil { _ = engine }   // so the pending events on disk are sent too
        await signOut()
        leaveServer()
        defaults.removeObject(forKey: Self.serverURLKey)
        serverURL = nil
        updateCache()
        showSettings = false
        await removeWebsiteData(of: old)
    }

    /// Stops and drops everything bound to the current server: playback and the engine (whose API is that
    /// server's), the queues and the token of its user, the web view. The cache folder of another server is
    /// removed by `updateCache()` once the next server is set.
    private func leaveServer() {
        offlinePlayer = nil
        if let engineStorage { engineStorage.reset() } else { queueStore.wipe() }
        queueStore.user = nil
        auth.clear()
        dropEngine()
        apiClient = nil
        bridge = nil; webView = nil
    }

    private func dropEngine() {
        guard let e = engineStorage else { return }
        e.onActiveChange = nil         // the remote commands stay installed; they resolve the next engine
        e.onModesChange = nil
        audioSession = nil
        ticker?.invalidate(); ticker = nil
        e.onEvent = nil
        e.willStartPlayback = nil
        engineStorage = nil
        if systemIntegration { MPNowPlayingInfoCenter.default().nowPlayingInfo = nil }
    }

    /// Every website data record of the server's host: cookies (the session cookie included), local storage,
    /// IndexedDB, service workers and the HTTP cache. WebKit groups records by registrable domain, so the
    /// record of the host's domain goes (the app's store holds only its own servers' data).
    private func removeWebsiteData(of server: URL) async {
        guard let host = server.host?.lowercased() else { return }
        let store = WKWebsiteDataStore.default()
        let types = WKWebsiteDataStore.allWebsiteDataTypes()
        let records = await store.dataRecords(ofTypes: types).filter { Self.record($0.displayName, covers: host) }
        if !records.isEmpty { await store.removeData(ofTypes: types, for: records) }
        let cookies = store.httpCookieStore            // host-only cookies a record match could miss
        for c in await cookies.allCookies() where Self.record(c.domain.trimmingCharacters(in: CharacterSet(charactersIn: ".")), covers: host) {
            await cookies.deleteCookie(c)
        }
    }

    nonisolated static func record(_ name: String, covers host: String) -> Bool {
        let n = name.lowercased()
        return !n.isEmpty && (n == host || host.hasSuffix("." + n))
    }

    // MARK: - Settings

    /// The cache cap in GB: the saved choice, else 2.
    var cacheCapGB: Int {
        let gb = defaults.integer(forKey: Self.cacheCapKey)
        return SettingsModel.capChoices.contains(gb) ? gb : SettingsModel.defaultCapGB
    }

    /// Saves the cap (`lark.cacheCapGB`) and applies it to the signed-in user's cache, which evicts at once.
    /// Only `SettingsModel.capChoices` are taken.
    func setCacheCap(gb: Int) {
        guard SettingsModel.capChoices.contains(gb) else { return }
        defaults.set(gb, forKey: Self.cacheCapKey)
        cacheStore?.capBytes = Int64(gb) << 30
    }

    private var savedPrefs: NativePrefs? {
        guard let quality = defaults.string(forKey: Self.prefsQualityKey) else { return nil }
        return NativePrefs(quality: quality, carLyrics: defaults.object(forKey: Self.prefsCarLyricsKey) as? Bool ?? true,
                           loudness: defaults.object(forKey: Self.prefsLoudnessKey) as? Bool ?? true)
    }

    /// The API client for the current server. A 401 runs `signOut()` (token and session cookie), never `auth.clear()` alone.
    func api(session: URLSession = .shared) -> LarkAPI? {
        guard let serverURL else { return nil }
        if let apiClient, apiClient.base == serverURL { return apiClient }
        let api = LarkAPI(base: serverURL, session: session, auth: auth)
        // No flush: the token is already dead, and a flush would only meet another 401.
        api.onUnauthorized = { [weak self] in await self?.signOut(flush: false) }
        apiClient = api
        return api
    }

    /// The single web view and bridge for the current server, created on first use.
    func web(for server: URL) -> WKWebView {
        if let webView, bridge?.serverURL == server { return webView }
        let bridge = BridgeController(serverURL: server) { [weak self] in self?.handle($0) }
        let web = WKWebView(frame: .zero, configuration: bridge.makeConfiguration())
        bridge.attach(web)
        self.bridge = bridge; self.webView = web
        return web
    }

    func handle(_ message: WebMessage) {
        switch message {
        case .auth(signedIn: true, let userId):
            if let userId { switchUser(to: userId) }
            Task {
                await harvestCookies(userId: userId)
                syncFavorites()        // after the harvest: a fresh sign-in has no token before it
            }
        case .auth(signedIn: false, _):
            Task { await signOut() }
        case .openSettings:
            openSettings()
        case .favoriteChanged(let trackId, let on):
            userCache?.store?.markFavorite(trackId, on)
            syncFavorites()
        case .setPrefs(let p):
            defaults.set(p.quality, forKey: Self.prefsQualityKey)
            defaults.set(p.carLyrics, forKey: Self.prefsCarLyricsKey)
            defaults.set(p.loudness, forKey: Self.prefsLoudnessKey)
            engine?.handle(message)
        default:
            engine?.handle(message)
        }
    }

    /// Another user signed in on this device: the old user's playback, queues and pending events go first.
    private func switchUser(to userId: Int) {
        guard queueStore.user != userId else { return }
        if queueStore.user != nil {
            if let engineStorage { engineStorage.reset() } else { queueStore.wipe() }
        }
        queueStore.user = userId
        updateCache()
        engineStorage?.restore()
        engineStorage?.emitAll()
    }

    /// Reads `lark_token` for the server host from the web view's cookie store into the Keychain.
    func harvestCookies(userId: Int? = nil) async {
        guard let webView, let host = serverURL?.host else { return }
        let cookies = await webView.configuration.websiteDataStore.httpCookieStore.allCookies()
        auth.harvest(from: cookies, serverHost: host, userId: userId)
    }

    /// Logout, a user switch or a 401 (`LarkAPI.onUnauthorized` calls this, not `auth.clear()` alone):
    /// 1. with `flush`, playback stops and the listen in progress plus every pending play event get one POST,
    ///    while the token still works;
    /// 2. the engine resets: playback stopped, both queues and the pending events of this user wiped (disk too);
    /// 3. the token is cleared and the page is told to sign in again (`authRequired`);
    /// 4. the session cookie is deleted.
    /// Re-entry while signing out (a 401 on the flush itself) returns at once. The flush gets at most
    /// `signOutFlushTimeout` seconds; if another user signed in meanwhile (`auth` → `switchUser`, which already
    /// wiped the old user's data), sign-out stops there and leaves the new user's queue, token and cookie alone.
    func signOut(flush: Bool = true) async {
        guard !signingOut else { return }
        signingOut = true
        defer { signingOut = false }
        let user = queueStore.user
        if flush, let engineStorage {
            await withTimeout(signOutFlushTimeout) { await engineStorage.flushBeforeSignOut() }
            guard queueStore.user == user else { return }
        }
        engineStorage?.reset()
        if engineStorage == nil { queueStore.wipe() }
        queueStore.user = nil              // nothing more is written for the old user; the next login sets it
        updateCache()                      // the user's cache stays on disk for their next sign-in, unused till then
        auth.clear()
        send(.authRequired)
        // Delete the session cookie too, so only a fresh login can hand native a token again.
        guard let webView, let host = serverURL?.host?.lowercased() else { return }
        let store = webView.configuration.websiteDataStore.httpCookieStore
        for c in await store.allCookies() where c.name == AuthStore.cookieName
            && (c.domain.lowercased() == host || c.domain.lowercased() == "." + host) {
            await store.deleteCookie(c)
        }
    }

    func scenePhaseChanged(_ phase: ScenePhase) {
        bridge?.isActive = phase == .active
        if phase != .active { flushCache() }
        if phase == .active {
            engineStorage?.emitAll()   // the page missed events while it was frozen
            if lastActivationSync.map({ now().timeIntervalSince($0) >= Self.activationSyncInterval }) ?? true {
                lastActivationSync = now()
                syncFavorites()
            }
        }
    }

    // MARK: - Offline cache

    /// The cache folder of a server: a hex prefix of the SHA-256 of its normalised base URL (scheme, lowercased
    /// host, port, path without a trailing `/`). Track ids are another server's rows, so each server has its own.
    nonisolated static func serverKey(_ base: URL) -> String {
        let c = URLComponents(url: base, resolvingAgainstBaseURL: false)
        var path = c?.path ?? ""
        while path.hasSuffix("/") { path.removeLast() }
        let norm = "\(c?.scheme?.lowercased() ?? "")://\(c?.host?.lowercased() ?? "")\(c?.port.map { ":\($0)" } ?? "")\(path)"
        return SHA256.hash(data: Data(norm.utf8)).prefix(8).map { String(format: "%02x", $0) }.joined()
    }

    /// Points the engine's cache at the signed-in user's `CacheStore` (`cacheRoot/<server-key>/<userId>`), or at
    /// nothing while signed out or before a server is set. A store is kept while its user and API stay the same.
    /// Only the current server's folder is kept: other servers' folders, and the pre-server `<userId>` ones, go.
    private func updateCache() {
        guard let userCache else { return }
        guard let serverURL else {
            if userCache.store != nil { dropSync() }
            return userCache.set(nil)
        }
        let serverDir = cacheRoot.appendingPathComponent(Self.serverKey(serverURL), isDirectory: true)
        defer { pruneOtherServers(keeping: serverDir) }
        // The API only once a user is signed in: building it here must not fix its session early.
        guard let user = queueStore.user, let api = api(session: session) else {
            if userCache.store != nil { dropSync() }
            return userCache.set(nil)
        }
        let root = serverDir.appendingPathComponent(String(user), isDirectory: true)
        if let s = userCache.store, s.root == root, s.api === api { return }
        dropSync()
        userCache.set(CacheStore(root: root, api: api, network: network, capBytes: Int64(cacheCapGB) << 30,
                                 now: { [weak self] in self?.now() ?? Date() }))
    }

    private func pruneOtherServers(keeping dir: URL) {
        let fm = FileManager.default
        guard let names = try? fm.contentsOfDirectory(atPath: cacheRoot.path) else { return }
        for n in names where n != dir.lastPathComponent { try? fm.removeItem(at: cacheRoot.appendingPathComponent(n)) }
    }

    /// Writes the cache index now (backgrounding, termination, the end of a background run).
    func flushCache() { userCache?.store?.flush() }

    /// Starts the favorites sync (launch, activation, a favorite change, sign-in, Wi-Fi back). Off Wi-Fi it is
    /// remembered for when Wi-Fi is back; during a sync, one more runs after it.
    func syncFavorites() {
        guard let store = userCache?.store, store.api.token != nil else { return }
        // Not answered yet is unknown, not Wi-Fi: the first answer runs it (`networkChanged`).
        guard network.answered, network.isOnline, !network.isExpensive else { syncWanted = true; return }
        syncWanted = false
        if syncTask != nil { syncAgain = true; return }
        let network = self.network, gen = syncGeneration
        syncTask = Task { [weak self] in
            await FavoritesSync(api: store.api, cache: store, network: network).run()
            guard let self, self.syncGeneration == gen else { return }
            self.syncTask = nil
            if Task.isCancelled { self.syncAgain = false }   // expired: nothing is owed to a later sync
            if self.syncAgain {
                self.syncAgain = false
                self.syncFavorites()
            }
        }
    }

    /// A favorites sync is running (or queued to run again right after).
    var isSyncing: Bool { syncTask != nil }

    private func dropSync() {
        syncTask?.cancel(); syncTask = nil
        syncAgain = false; syncWanted = false
        syncGeneration += 1
    }

    /// Runs (or joins) the sync and waits for it; cancelling the caller (the background task expiring)
    /// cancels the sync. After a cold launch it first waits (bounded) for the network monitor's first answer,
    /// so the sync runs inside the caller's background time instead of after it.
    func syncFavoritesNow() async {
        await network.firstAnswer(timeout: networkAnswerTimeout)
        syncFavorites()
        guard let t = syncTask else { return }
        await withTaskCancellationHandler { await t.value } onCancel: { t.cancel() }
    }

    private func networkChanged() {
        if syncWanted && network.answered && network.isOnline && !network.isExpensive { syncFavorites() }
        if pageFailed && network.isOnline { retryPage() }        // the page failed offline: try again now
    }

    // MARK: - The page could not load

    /// The server's page failed to load (offline, server down, a 5xx): `RootView` covers the blank web view
    /// with a native screen (retry, settings, offline favorites). It retries by itself when the network is back.
    @Published private(set) var pageFailed = false
    /// Loads a URL into the web view; tests replace it.
    var loadPage: ((URL) -> Void)?

    func pageDidFail(_ error: Error) {
        guard PageLoad.isFailure(error) else { return }
        pageFailed = true
        offlinePlayer?.retryFailed()
    }

    /// The page is back: the offline player closes and the web UI shows what the engine is playing.
    func pageDidLoad() {
        pageFailed = false
        offlinePlayer = nil
    }

    func retryPage() {
        guard let serverURL else { return }
        pageFailed = false
        offlinePlayer?.retryStarted()
        if let loadPage { loadPage(serverURL) } else { webView?.load(URLRequest(url: serverURL)) }
    }

    /// The native settings sheet (更换服务器, cache, the test button), also without a page.
    func openSettings() { showSettings = true }

    /// Cached favorites exist: the error screen offers to play them.
    var hasOfflineFavorites: Bool { !cache.cachedFavorites().isEmpty }

    /// 播放离线收藏: opens the offline player and, unless something is already playing, shuffles the cached favorites.
    func playOfflineFavorites() async {
        openOfflinePlayer()
        _ = try? await IntentRun.shuffleFavorites(intentPlayer)
    }

    // MARK: - The offline player

    /// The native offline player over the failed page (`RootView`); nil when it is closed. It stays open through
    /// failed retries and closes when the page loads (`pageDidLoad`); the engine plays on either way.
    @Published private(set) var offlinePlayer: OfflinePlayerModel?

    /// Opens the offline player on the engine (no network: the cache index and the engine's own events).
    func openOfflinePlayer() {
        guard offlinePlayer == nil, let engine else { return }
        let cache = self.cache
        let model = OfflinePlayerModel(player: engine, favorites: { cache.cachedFavorites() },
                                       retry: { [weak self] in self?.retryPage() },
                                       artwork: { [weak self] id in self?.cachedArtwork(trackID: id) })
        offlinePlayer = model
        engine.emitAll()           // seeds it with the queue and state (the page, if any, gets them again: harmless)
    }

    /// Back to the 无法连接服务器 screen (重试, 设置); playback goes on.
    func closeOfflinePlayer() { offlinePlayer = nil }

    /// A cached track's cover on the phone, for the offline player (nil: none cached, it shows a generated tile).
    func cachedArtwork(trackID: Int) -> URL? { cacheStore?.artworkURL(trackID: trackID) }
}
