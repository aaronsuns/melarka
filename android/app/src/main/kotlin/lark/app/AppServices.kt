package lark.app

import android.content.SharedPreferences
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import lark.api.LarkApi
import lark.auth.AuthStore
import lark.auth.SecretStore
import lark.auth.WebData
import lark.bridge.BridgeController
import lark.bridge.NativeEvent
import lark.bridge.NativePrefs
import lark.bridge.PageLoad
import lark.bridge.WebMessage
import lark.cache.CacheLayout
import lark.cache.CacheProviding
import lark.cache.CacheStore
import lark.cache.FavoritesSync
import lark.cache.FreeSpace
import lark.cache.SyncRun
import lark.cache.UserCache
import lark.car.EngineIntentPlayer
import lark.car.IntentPlayer
import lark.car.IntentRun
import lark.core.Cancellable
import lark.core.Clock
import lark.core.NetworkStatus
import lark.core.Scheduler
import lark.core.SwitchableNetworkStatus
import lark.core.TaskBag
import lark.player.EngineTexts
import lark.player.MediaBackend
import lark.player.NowPlayingSink
import lark.player.PlayEventQueue
import lark.player.PlayerEngine
import lark.player.QueueStore
import okhttp3.OkHttpClient
import java.io.File
import java.time.Duration
import java.time.Instant
import kotlin.coroutines.ContinuationInterceptor

/** Whether the app's UI is on screen (iOS `ScenePhase`): the Activity started, or stopped. */
enum class ScenePhase { active, background }

/** The page's web view as the services drive it (the Activity's `WebShell`). */
fun interface PageControl {
    fun load(url: String)
}

/** The background favorites sync (WorkManager in the app, `FavoritesRefresh`): on after a sign-in, off after a sign-out. */
interface RefreshScheduler {
    fun schedule()
    fun cancel()

    companion object {
        val none = object : RefreshScheduler {
            override fun schedule() {}
            override fun cancel() {}
        }
    }
}

/**
 * The composition root (port of iOS `App/AppServices.swift`): application-scoped (`LarkApplication`), so playback, the
 * car and lock-screen controls and the background sync work with no Activity. It owns the auth store, the API, the bridge
 * of the current server, the player engine (shown by the process's `PlaybackHost`) and the signed-in user's offline
 * cache, and routes web messages between them. Everything here runs on the main thread ([scope]'s dispatcher); tests
 * build their own graph with fakes.
 *
 * The token is only in [auth] and the API's `Authorization` header: never on the bridge, in a log or in a URL.
 */
class AppServices(
    private val prefs: SharedPreferences,
    secrets: SecretStore,
    private val webData: WebData,
    private val http: OkHttpClient,
    dataDirectory: File,
    private val backend: MediaBackend,
    network: NetworkStatus,
    private val cacheRoot: File,
    private val scope: CoroutineScope,
    private val scheduler: Scheduler,
    private val clock: Clock,
    /** Null: the signed-in user's [CacheStore] under [cacheRoot] (tests may inject their own). */
    cache: CacheProviding? = null,
    private val texts: EngineTexts = EngineTexts.english,
    private val nowPlaying: NowPlayingSink? = null,
    /** The engine was built or dropped: the app gives it to the playback host (`PlaybackHost.engine`). */
    var onEngine: (PlayerEngine?) -> Unit = {},
    var refresh: RefreshScheduler = RefreshScheduler.none,
    private val freeSpace: (File) -> FreeSpace = { FreeSpace { null } },
    private val random: () -> Double = { Math.random() },
) {
    val auth = AuthStore(secrets, webData, http)

    /** The player's per-user files (queues, pending play events). */
    val queueStore = QueueStore(dataDirectory)

    /** The signed-in user's cache; null when a test injected its own [cache]. */
    private val userCache: UserCache? = if (cache == null) UserCache() else null

    /** What the engine reads: the signed-in user's cache, or the one a test injected. */
    val cache: CacheProviding = cache ?: userCache!!

    /** The signed-in user's cache store (Settings); null while signed out or when a test injected [cache]. */
    val cacheStore: CacheStore? get() = userCache?.store

    private val network = SwitchableNetworkStatus(network, stayOffline = prefs.getBoolean(STAY_OFFLINE_KEY, false))
    private val tasks = TaskBag(scope)
    /** The main thread, for the API's 401 hook, which runs on a background thread. */
    private val main = scope.coroutineContext[ContinuationInterceptor] ?: Dispatchers.Main

    var serverUrl: String? = prefs.getString(SERVER_URL_KEY, null)?.let { ServerUrl.normalize(it)?.toString() }
        private set

    private var apiClient: LarkApi? = null
    private var engineStorage: PlayerEngine? = null
    private var ticker: Cancellable? = null
    private var bridgeStorage: BridgeController? = null
    private var signingOut = false

    /** How long sign-out waits for its play-event POST (the web's logout waits 3 s). */
    var signOutFlushTimeoutS = 5.0

    /** How long a sync waits for the network monitor's first answer (a cold background launch). */
    var networkAnswerTimeoutS = 3.0

    /** Tests: receives what would go to the page. Production sends to the bridge. */
    var eventSink: ((NativeEvent) -> Unit)? = null

    /** The media service exists (`PlaybackService.running`): a start in the background is allowed then. */
    var serviceRunning: () -> Boolean = { false }

    /** The Activity is started (`scenePhaseChanged`). */
    private var foreground = false

    private val listeners = mutableListOf<() -> Unit>()

    // MARK: - State the UI shows (iOS @Published)

    /** [fn] runs after any change the UI shows: the server, the page, Stay Offline, the offline player, settings. */
    fun observe(fn: () -> Unit) { listeners.add(fn) }
    fun removeObserver(fn: () -> Unit) { listeners.remove(fn) }
    private fun changed() { listeners.toList().forEach { it() } }

    var showSettings = false
        private set(v) { field = v; changed() }

    /** A server change is running (Settings is disabled meanwhile). */
    var changingServer = false
        private set(v) { field = v; changed() }

    // MARK: - The engine

    /** The player, built on first use (it needs a server for its API). Null before a server is set. */
    val engine: PlayerEngine?
        get() {
            engineStorage?.let { return it }
            val api = api() ?: return null
            val e = PlayerEngine(backend, api, cache, queueStore, PlayEventQueue(api, queueStore, scope, clock), network, scheduler,
                clock, random, nowPlaying, scope, texts)
            e.onEvent = { send(it) }
            savedPrefs?.let { e.prefs = it }
            e.canAutoStart = { mayStartPlayback(foreground, serviceRunning()) }
            engineStorage = e
            // Saves the position while playing, sends what is due (`PlayerEngine.tick`).
            ticker = scheduler.every(TICK_SECONDS) { engineStorage?.tick() }
            onEngine(e)     // the playback host: the session, the remote commands, and its `willStartPlayback`
            return e
        }

    private var intentOverride: IntentPlayer? = null
    private var engineIntentPlayer: EngineIntentPlayer? = null

    /**
     * What the shortcuts and car autoplay drive (iOS `AppServices.intentPlayer`): the engine (null before a server is
     * set), unless a test replaced it. Setting null goes back to the engine.
     */
    var intentPlayer: IntentPlayer?
        get() {
            intentOverride?.let { return it }
            val e = engine ?: return null
            return engineIntentPlayer?.takeIf { it.engine === e } ?: EngineIntentPlayer(e).also { engineIntentPlayer = it }
        }
        set(value) { intentOverride = value }

    /**
     * Runs a shortcut's or car autoplay's [IntentRun] on the main thread. The scope's dispatcher is immediate: [body] runs
     * now up to its first suspension, so playback is prepared before this returns (while a shortcut is still visible).
     */
    fun runIntent(body: suspend () -> Unit): Job = scope.launch { body() }

    /**
     * At launch: builds the player when a server is known, so the car's play finds its engine even when Android
     * started the app in the background. Without a server it is built on first use.
     */
    fun start() {
        engine
        if (serverUrl != null && auth.token != null) refresh.schedule()
        lastActivationSync = clock.now()
        syncFavorites()
        if (stayOffline) openOfflinePlayer()
    }

    /** An event for the page: through the bridge (dropped while the Activity is stopped; re-sent when it starts). */
    fun send(event: NativeEvent) {
        offlinePlayer?.receive(event)
        eventSink?.let { it(event); return }
        bridgeStorage?.send(event)
    }

    /** The API client for the current server. A 401 runs [signOut] (token and session cookie), the token cleared first. */
    fun api(): LarkApi? {
        val server = serverUrl ?: return null
        apiClient?.let { return it }
        val api = LarkApi(server, http, auth)
        // The token goes at once (on the API's thread); then the rest of sign-out, on the main thread. No flush: the
        // token is already dead, and a flush would only meet another 401.
        api.onUnauthorized = {
            auth.clear()
            withContext(main) { if (apiClient === api) signOut(flush = false) }
        }
        apiClient = api
        return api
    }

    /** The bridge of [server]'s page, created on first use; the Activity attaches its web view. */
    fun bridge(server: String): BridgeController {
        bridgeStorage?.takeIf { it.serverUrl == server }?.let { return it }
        return BridgeController(server) { handle(it) }.also {
            it.isActive = foreground
            bridgeStorage = it
        }
    }

    /** The current server's bridge, if the page has one. */
    val bridge: BridgeController? get() = bridgeStorage

    // MARK: - Messages

    fun handle(message: WebMessage) {
        when (message) {
            is WebMessage.Auth -> if (message.signedIn) {
                message.userId?.let { switchUser(it) }
                tasks.run {
                    harvestCookies(message.userId)
                    if (auth.token != null) refresh.schedule()
                    syncFavorites()          // after the harvest: a fresh sign-in has no token before it
                }
            } else {
                tasks.run { signOut() }
            }
            is WebMessage.OpenSettings -> openSettings()
            is WebMessage.FavoriteChanged -> {
                userCache?.store?.markFavorite(message.trackId, message.on)
                syncFavorites()
            }
            is WebMessage.SetPrefs -> {
                prefs.edit()
                    .putString(PREFS_QUALITY_KEY, message.prefs.quality)
                    .putBoolean(PREFS_CAR_LYRICS_KEY, message.prefs.carLyrics)
                    .putBoolean(PREFS_LOUDNESS_KEY, message.prefs.loudness)
                    .apply()
                engine?.handle(message)
            }
            else -> engine?.handle(message)
        }
    }

    /** Waits for the services' own async work (harvests, sign-outs started by messages). */
    suspend fun idle() = tasks.idle()

    /** Another user signed in on this device: the old user's playback, queues and pending events go first. */
    fun switchUser(userId: Long) {
        if (queueStore.user == userId) return
        if (queueStore.user != null) {
            engineStorage?.reset() ?: queueStore.wipe()
        }
        queueStore.user = userId
        updateCache()           // the old user's cache stays on disk for their next sign-in
        engineStorage?.restore()
        engineStorage?.emitAll()
    }

    /**
     * A page finished loading: the session cookie may have changed (Android has no cookie observer; an in-page sign-in
     * that sets the cookie without a load is covered by the page's `auth{signedIn}`). A new token starts the background sync.
     */
    fun pageCookiesChanged() {
        tasks.run { if (harvestCookies()) refresh.schedule() }
    }

    /**
     * Reads `lark_token` for the server from the web view's cookie jar (verified by the server before it is kept).
     * True when a new token was stored.
     */
    suspend fun harvestCookies(userId: Long? = null): Boolean {
        val server = serverUrl ?: return false
        return auth.harvest(server, userId)
    }

    /**
     * Logout, a user switch or a 401 (the API's `onUnauthorized` calls this after `auth.clear()`):
     * 1. with [flush], playback stops and the listen in progress plus every pending play event get one POST, while the
     *    token still works, for at most [signOutFlushTimeoutS];
     * 2. the engine resets: playback stopped, both queues and the pending events of this user wiped (disk too);
     * 3. the cache is set aside (kept on disk for that user), the token is cleared, the background sync is cancelled
     *    and the page is told to sign in again (`authRequired`);
     * 4. the session cookie is deleted.
     * Re-entry while signing out (a 401 on the flush itself) returns at once. If another user signed in during the flush
     * (`auth` → [switchUser], which already wiped the old user's data), sign-out stops there and leaves the new user's
     * queue, token and cookie alone.
     */
    suspend fun signOut(flush: Boolean = true) {
        if (signingOut) return
        signingOut = true
        try {
            val user = queueStore.user
            val e = engineStorage
            if (flush && e != null) {
                flushBounded(e)
                if (queueStore.user != user) return
            }
            engineStorage?.reset() ?: queueStore.wipe()
            queueStore.user = null           // nothing more is written for the old user; the next login sets it
            updateCache()
            auth.clear()
            refresh.cancel()
            send(NativeEvent.AuthRequired)
            serverUrl?.let { webData.deleteSessionCookie(it) }
        } finally {
            signingOut = false
        }
    }

    /** The engine's sign-out flush, given [signOutFlushTimeoutS] of real time (a hanging server can't hold it up). */
    private suspend fun flushBounded(e: PlayerEngine) {
        val job = scope.launch(start = CoroutineStart.LAZY) { e.flushBeforeSignOut() }
        job.start()
        val done = withContext(Dispatchers.Default) {
            withTimeoutOrNull((signOutFlushTimeoutS * 1000).toLong()) { job.join() }
        }
        if (done == null) job.cancel()
    }

    // MARK: - Servers

    /**
     * The first server, or another one. Leaving a server stops its playback, wipes its queues and forgets its token, so
     * the old token is never sent to the new server; the new server gets a new engine with its own API.
     */
    suspend fun setServerUrl(url: String) {
        val old = serverUrl
        if (old != null && old != url) {
            changeServer()                       // signs out of the old server first (events flushed)
            if (serverUrl != null) return        // another change won meanwhile
        }
        prefs.edit().putString(SERVER_URL_KEY, url).apply()
        serverUrl = url
        updateCache()
        changed()
    }

    /**
     * Settings → change server: sign out of this server (pending play events sent while the token works; playback
     * stopped, queues wiped, the token and the session cookie gone), drop the engine and the bridge, remove the server's
     * web data, and forget the server, so the server screen shows again. Re-entry returns.
     */
    suspend fun changeServer() {
        val old = serverUrl ?: return
        if (changingServer) return
        changingServer = true
        try {
            if (engineStorage == null && auth.token != null) engine   // so the pending events on disk are sent too
            signOut()
            leaveServer()
            prefs.edit().remove(SERVER_URL_KEY).apply()
            serverUrl = null
            updateCache()
            showSettings = false
            webData.removeWebsiteData(old)
        } finally {
            changingServer = false
        }
    }

    /** Stops and drops everything bound to the current server: playback and the engine, the queues and token of its user, the bridge. */
    private fun leaveServer() {
        offlinePlayer = null
        engineStorage?.reset() ?: queueStore.wipe()
        queueStore.user = null
        auth.clear()
        dropEngine()
        apiClient = null
        bridgeStorage?.attach(null)
        bridgeStorage = null
    }

    private fun dropEngine() {
        val e = engineStorage ?: return
        ticker?.cancel(); ticker = null
        e.onEvent = null
        e.canAutoStart = null
        onEngine(null)       // the playback host lets it go (its `willStartPlayback`, the remote commands)
        e.detach()
        engineStorage = null
    }

    // MARK: - Settings

    /** The cache cap in GB: the saved choice, else 2. */
    val cacheCapGB: Int
        get() = prefs.getInt(CACHE_CAP_KEY, DEFAULT_CAP_GB).takeIf { it in CacheStore.CAP_CHOICES_GB } ?: DEFAULT_CAP_GB

    /** Saves the cap and applies it to the signed-in user's cache, which evicts at once. Only the choices are taken. */
    fun setCacheCap(gb: Int) {
        if (gb !in CacheStore.CAP_CHOICES_GB) return
        prefs.edit().putInt(CACHE_CAP_KEY, gb).apply()
        cacheStore?.capBytes = gb.toLong() shl 30
    }

    /** The web's last `setPrefs`, so a cold start (the car) streams at the chosen quality, with car lyrics and gain as set. */
    private val savedPrefs: NativePrefs?
        get() {
            val quality = prefs.getString(PREFS_QUALITY_KEY, null) ?: return null
            return NativePrefs(quality, prefs.getBoolean(PREFS_CAR_LYRICS_KEY, true), prefs.getBoolean(PREFS_LOUDNESS_KEY, true))
        }

    // MARK: - The Activity

    private var lastActivationSync: Instant? = null

    /**
     * The Activity started or stopped. Stopped: no events go to the page and the cache index is written. Started: the
     * page gets both queues and states again, a resume held back in the background runs, and the favorites sync runs
     * at most once an hour.
     */
    fun scenePhaseChanged(phase: ScenePhase) {
        val active = phase == ScenePhase.active
        foreground = active
        bridgeStorage?.isActive = active
        if (!active) { flushCache(); return }
        engineStorage?.emitAll()        // the page missed events while it was stopped
        engineStorage?.resumeDeferred()
        val last = lastActivationSync
        if (last == null || Duration.between(last, clock.now()) >= ACTIVATION_SYNC_INTERVAL) {
            lastActivationSync = clock.now()
            syncFavorites()
        }
    }

    // MARK: - Offline cache

    private var syncJob: Job? = null
    /** Bumped when a sync is dropped (another user, sign-out), so its late end touches nothing. */
    private var syncGeneration = 0
    /** A trigger came while a sync was running: one more runs after it. */
    private var syncAgain = false
    /** A trigger came while off Wi-Fi: the sync runs when Wi-Fi is back. */
    private var syncWanted = false
    /** Waiting for the network monitor's first answer ([syncFavoritesNow]). */
    private val answerWaiters = mutableListOf<CompletableDeferred<Unit>>()

    /**
     * Points the engine's cache at the signed-in user's [CacheStore] (`cacheRoot/<server key>/<userId>`), or at nothing
     * while signed out or before a server is set. A store is kept while its user and API stay the same. Only the current
     * server's folder is kept: other servers' folders, and the pre-server `<userId>` ones, go.
     */
    private fun updateCache() {
        val uc = userCache ?: return
        val server = serverUrl
        if (server == null) {
            if (uc.store != null) dropSync()
            uc.set(null)
            return
        }
        val serverDir = File(cacheRoot, CacheLayout.serverKey(server))
        try {
            val user = queueStore.user
            val api = if (user != null) api() else null
            if (user == null || api == null) {
                if (uc.store != null) dropSync()
                uc.set(null)
                return
            }
            val root = File(serverDir, user.toString())
            val current = uc.store
            if (current != null && current.root == root && current.api === api) return
            dropSync()
            uc.set(CacheStore(root, api, network, clock, scope, scheduler, freeSpace(root), capBytes = cacheCapGB.toLong() shl 30))
        } finally {
            CacheLayout.pruneOtherServers(cacheRoot, serverDir)
        }
    }

    /** Writes the cache index now (the Activity stopping, the end of a background run). */
    fun flushCache() { userCache?.store?.flush() }

    /**
     * Starts the favorites sync (launch, activation, a favorite change, sign-in, Wi-Fi back). Off Wi-Fi it is remembered
     * for when Wi-Fi is back; during a sync, one more runs after it.
     */
    fun syncFavorites() {
        val store = userCache?.store ?: return
        if (store.api.token == null) return
        // Not answered yet is unknown, not Wi-Fi: the first answer runs it (`networkChanged`).
        if (!onWiFi) { syncWanted = true; return }
        syncWanted = false
        if (syncJob != null) { syncAgain = true; return }
        val gen = syncGeneration
        // Lazily started: a sync that ends at once must find itself in `syncJob` to clear it.
        val job = scope.launch(start = CoroutineStart.LAZY) {
            var cancelled = false
            try {
                FavoritesSync(store.api, store, network).run()
            } catch (_: CancellationException) {
                cancelled = true
            }
            if (syncGeneration != gen) return@launch
            syncJob = null
            if (cancelled) syncAgain = false     // stopped: nothing is owed to a later sync
            if (syncAgain) {
                syncAgain = false
                syncFavorites()
            }
        }
        syncJob = job
        job.start()
    }

    /** A favorites sync is running. */
    val isSyncing: Boolean get() = syncJob != null

    private val onWiFi: Boolean get() = network.answered && network.isOnline && !network.isExpensive

    private fun dropSync() {
        syncJob?.cancel(); syncJob = null
        syncAgain = false; syncWanted = false
        syncGeneration += 1
    }

    /**
     * Runs (or joins) the sync and waits for it; cancelling the caller (the background work stopping) cancels the sync.
     * After a cold launch it first waits (bounded) for the network monitor's first answer, so the sync runs inside the
     * caller's background time instead of after it.
     */
    suspend fun syncFavoritesNow() {
        if (!network.answered) {
            val answered = CompletableDeferred<Unit>()
            answerWaiters += answered
            try {
                // Real time, like the sign-out flush's bound (the answer comes from the system, not from this scope).
                withContext(Dispatchers.Default) { withTimeoutOrNull((networkAnswerTimeoutS * 1000).toLong()) { answered.await() } }
            } finally {
                answerWaiters -= answered
            }
        }
        syncFavorites()
        val t = syncJob ?: return
        try {
            t.join()
        } catch (e: CancellationException) {
            t.cancel()
            throw e
        }
    }

    /** What the background work runs (`FavoritesRefresh.provider`): the signed-in user's sync, null while signed out. */
    fun syncRun(): SyncRun? {
        if (userCache?.store == null || auth.token == null) return null
        return SyncRun {
            syncFavoritesNow()
            flushCache()         // before the work ends and the process may go
        }
    }

    private fun networkChanged() {
        if (network.answered) answerWaiters.toList().forEach { it.complete(Unit) }
        if (syncWanted && onWiFi) syncFavorites()
        if (pageFailed && network.isOnline) retryPage()        // the page failed offline: try again now
    }

    // MARK: - The page could not load

    /**
     * The server's page failed to load (offline, server down, a 5xx): the Activity covers the web view with a native
     * screen (retry, settings, offline favorites). It retries by itself when the network is back.
     */
    var pageFailed: Boolean = prefs.getBoolean(STAY_OFFLINE_KEY, false)     // with Stay Offline on, the page is not loaded
        private set(v) { field = v; changed() }

    /** Loads a URL into the web view; tests replace it. Null: [page]. */
    var loadPage: ((String) -> Unit)? = null

    /** The Activity's web view, while it has one. */
    var page: PageControl? = null

    /** A main-frame load error (`WebViewClient.onReceivedError`). */
    fun pageDidFail(errorCode: Int, description: String?) {
        if (PageLoad.isFailure(errorCode, description)) pageDidFail()
    }

    /** A main-frame HTTP status (`WebViewClient.onReceivedHttpError`): a 5xx is a failed page. */
    fun pageDidFail(status: Int) {
        if (PageLoad.isFailure(status)) pageDidFail()
    }

    private fun pageDidFail() {
        pageFailed = true
        offlinePlayer?.retryFailed()
    }

    /** The page is back: the offline player closes and the web UI shows what the engine is playing. */
    fun pageDidLoad() {
        if (stayOffline) return         // the empty page of Stay Offline
        pageFailed = false
        offlinePlayer = null
    }

    fun retryPage() {
        val server = serverUrl ?: return
        if (stayOffline) return
        pageFailed = false
        offlinePlayer?.retryStarted()
        loadPage?.let { it(server); return }
        page?.load(server)
    }

    // MARK: - Stay Offline

    /** Stay Offline (a flight): nothing tries the server until it is turned off, whatever the phone's network says. */
    val stayOffline: Boolean get() = stayOfflineOn
    private var stayOfflineOn: Boolean = this.network.stayOffline
        set(v) { field = v; changed() }

    /**
     * On: the page stops loading and the offline player opens. Off: everything waiting for the network goes
     * (`networkChanged`: the page loads again, the engine resumes, a skipped sync runs).
     */
    fun setStayOffline(on: Boolean) {
        if (on == stayOffline) return
        stayOfflineOn = on
        prefs.edit().putBoolean(STAY_OFFLINE_KEY, on).apply()
        if (on) {
            // The page's own requests stop too: an empty page until Stay Offline is off (`retryPage` loads it then).
            page?.load("about:blank")
            pageFailed = true
            openOfflinePlayer()
        }
        network.stayOffline = on
    }

    /** The native settings (change server, cache, Stay Offline), also without a page. */
    fun openSettings() { showSettings = true }
    fun closeSettings() { showSettings = false }

    /** Cached favorites exist: the error screen offers to play them. */
    val hasOfflineFavorites: Boolean get() = cache.cachedFavorites().isNotEmpty()

    /**
     * Play Offline Favorites: opens the offline player and, unless something is already playing, shuffles the cached
     * favorites (the Shuffle Favorites shortcut, [IntentRun]).
     */
    suspend fun playOfflineFavorites() {
        openOfflinePlayer()
        if (engine == null) return
        IntentRun.shuffleFavorites(intentPlayer)
    }

    // MARK: - The offline player

    /** Builds the offline player on the engine (the native screen, Task 12). */
    var makeOfflinePlayer: (PlayerEngine) -> OfflinePlayer = { OfflinePlayer.Placeholder }

    /**
     * The native offline player over the failed page; null when it is closed. It stays open through failed retries and
     * closes when the page loads ([pageDidLoad]); the engine plays on either way.
     */
    var offlinePlayer: OfflinePlayer? = null
        private set(v) { field = v; changed() }

    /** Opens the offline player on the engine (no network: the cache index and the engine's own events). */
    fun openOfflinePlayer() {
        if (offlinePlayer != null) return
        val e = engine ?: return
        offlinePlayer = makeOfflinePlayer(e)
        e.emitAll()           // seeds it with the queue and state (the page, if any, gets them again: harmless)
    }

    /** Back to the can't-connect screen (retry, settings); playback goes on. */
    fun closeOfflinePlayer() { offlinePlayer = null }

    /** A cached track's cover on the phone, for the offline player (null: none cached). */
    fun cachedArtwork(trackId: Long): File? = cacheStore?.artworkFile(trackId)

    // Last: every property above is initialised before the first cache and network work.
    init {
        queueStore.user = auth.userId
        this.network.observe { networkChanged() }
        updateCache()
    }

    companion object {
        const val SERVER_URL_KEY = "serverURL"
        /** Settings stores the cap here, in GB (`CacheStore.CAP_CHOICES_GB`). */
        const val CACHE_CAP_KEY = "lark.cacheCapGB"
        const val DEFAULT_CAP_GB = 2
        /** The Stay Offline switch, kept until the user turns it off. */
        const val STAY_OFFLINE_KEY = "lark.stayOffline"
        const val PREFS_QUALITY_KEY = "lark.prefs.quality"
        const val PREFS_CAR_LYRICS_KEY = "lark.prefs.carLyrics"
        const val PREFS_LOUDNESS_KEY = "lark.prefs.loudness"
        /** The foreground's sync runs at most this often. */
        val ACTIVATION_SYNC_INTERVAL: Duration = Duration.ofHours(1)
        const val TICK_SECONDS = 10.0

        /**
         * Whether the engine may start playback on its own (the network came back): in the foreground, or while the
         * media service runs (its notification is up). In the background with no service, Android refuses to start
         * the service (API 26+ background start limits), so the player waits, paused by the system, for the user.
         */
        fun mayStartPlayback(foreground: Boolean, serviceRunning: Boolean): Boolean = foreground || serviceRunning
    }
}
