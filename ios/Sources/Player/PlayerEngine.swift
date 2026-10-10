import Foundation
import os

/// The native player: one `MediaBackend`, two queues (music and 频道 episodes) and one active kind.
///
/// - Never-stop (music): a failed item is skipped (and passed over for 30 minutes); a network error retries the
///   same item at the same position after 2, 5 and 10 s first. Offline, or after 3 failures in a row, only items
///   on the phone count, then cached favorites (`NextChooser`, the web's `nextTrack.ts`). A remote track
///   that buffers for 6 s gives way to one on the phone (the web's stall switch), and a player stopped for
///   the network starts again when it comes back. An episode that cannot play falls back to music.
/// - Backend callbacks carry the generation of their load; a callback about a replaced item is dropped.
/// - Radio, shuffle and favorites queues refill themselves when 2 or fewer items are left (the web's refill).
/// - Shuffle and repeat (`modes`, music only, the web's `modes.ts`): repeat off changes nothing above; repeat all
///   loops the queue with no refill (a new, reshuffled pass when shuffle is on); repeat one replays a track that
///   ended on its own, gaplessly. A cached favorite that stands in for a track under repeat all plays once and
///   leaves the queue again.
/// - The sleep timer (`sleepTimer`, music and episodes) runs here, on `scheduler`, so it keeps time with the
///   phone locked: after n minutes the volume fades to 0 over 10 s, playback pauses and the volume is back for
///   the next play. "End of this track" fades over the item's last 10 s and stops at its end (music: the next
///   track loaded, paused; an episode: stays on the finished one, as the web player does).
/// - Loudness: each music item loads with its own gain (`Gain`, the `loudness` pref), the preloaded one too, so
///   a gapless change keeps the right level. The master volume belongs to the sleep timer's fade.
/// - Both queues are saved per user on every change and every 10 s while playing (`QueueStore`); the music
///   queue goes to `PUT /queue` (`QueueSync`). Music creates play events (`PlayEventQueue`); episodes send
///   `PUT /episodes/{id}/progress` instead.
/// - The web drives it through `handle(_:)`; every change goes back as `onEvent` (`queue`, `state`, `notice`,
///   `flushed`).
@MainActor final class PlayerEngine: MediaBackendDelegate {
    static let skipThresholdS: Double = 30
    static let maxConsecutiveFailures = 3
    static let networkRetryDelays: [Double] = [2, 5, 10]
    static let failedTTL: TimeInterval = 30 * 60
    static let progressEvery: TimeInterval = 15
    static let refillRetry: Double = 30
    static let refillExcludeMax = 300
    static let shuffleSize = 50
    /// A previous press this far into an item restarts it instead.
    static let restartPrevMs = 3000
    /// An episode saved this close to its end starts from the beginning (the web's `resumeAt`).
    static let nearEndS = 30
    /// The notices and errors the web page shows, in the phone's language (Localizable.xcstrings).
    static let offlineNotice = String(localized: "Offline: playing cached favorites")
    static let noFavoritesNotice = String(localized: "No favorites yet: shuffling all songs")
    static let cannotPlay = String(localized: "Can't play")
    static let stallNotice = String(localized: "Network too slow: playing cached songs first")
    static let offlineStopped = String(localized: "Offline: no cached songs to play")
    /// A remote track buffering this long while playing gives way to one on the phone (the web's STALL_MS).
    static let stallSeconds: Double = 6
    static let episodeFallbackNotice = String(localized: "Episode can't play: playing music instead")
    static let nothingCachedNotice = String(localized: "Offline, and no favorites are cached")
    static let shuffleFailedNotice = String(localized: "Couldn't get favorites; try again later")
    /// Car lyrics that could not be fetched are asked again after this, then twice as long each time.
    static let lyricsRetryS: Double = 30
    static let lyricsRetryMaxS: Double = 600
    /// The sleep timer's fade: this long, in this many steps (the web's FADE_MS and FADE_STEP_MS).
    static let sleepFadeS: Double = 10
    static let sleepFadeSteps = 40
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "engine")

    private let backend: MediaBackend
    private let api: LarkAPIProtocol
    /// The API this engine talks to (a server change builds a new engine; tests check which server).
    var client: LarkAPIProtocol { api }
    private let cache: CacheProviding
    private let store: QueueStore
    let events: PlayEventQueue
    private let network: NetworkStatus
    private let now: () -> Date
    private let random: () -> Double
    private let scheduler: Scheduler
    private let sync: QueueSync
    private let tasks = TaskBag()

    private(set) var music = PlaybackQueue.empty
    private(set) var episodes = PlaybackQueue.empty
    private(set) var active: Item.Kind = .track {
        didSet { if active != oldValue { onActiveChange?(active) } }
    }
    /// Playing, or meant to be (loading, buffering, waiting for a network retry).
    private(set) var playing = false
    /// Paused by the system (an interruption, or the player on its own), not by the user: the end of an
    /// interruption may resume it. Any pause, play or load by the user clears it.
    private(set) var pausedBySystem = false
    var prefs = NativePrefs(quality: "high", carLyrics: true) {
        didSet {
            if prefs.loudness != oldValue.loudness { loudnessChanged() }
            guard prefs.carLyrics != oldValue.carLyrics else { return }
            lyricsID = nil; carLyrics = nil; lyricsRetry = nil
            if loaded?.kind == .track, let id = music.current?.trackID { fetchLyrics(id) }
            refreshNowPlaying()
        }
    }
    /// The episode playback rate; music always plays at 1.
    var rate: Double = 1 {
        didSet {
            guard rate != oldValue else { return }
            if loaded?.kind == .episode { backend.setRate(rate) }
            emitState(.episode)
        }
    }
    /// Shuffle and repeat for the music queue (episodes ignore them), saved with the queues.
    private(set) var modes = PlayModes() {
        didSet { if modes != oldValue { onModesChange?(modes) } }
    }
    /// The modes changed (`RemoteCommands`: the lock screen's and the car's repeat and shuffle state).
    var onModesChange: ((PlayModes) -> Void)?
    /// Repeat all: cached favorites put in for a track that could not play, and the interrupted track's id
    /// (requeued after it). Dropped when it ends or is skipped, and before a new pass (the web's `substitutes`).
    private struct Substitute { let id: String; var requeue: String? }
    private var substitutes: [Substitute] = []
    /// The sleep timer: a deadline (its fade starts 10 s before), or the end of the item playing.
    private enum Sleep { case at(Date), endOfTrack }
    private var sleep: Sleep?
    /// The fade's start, then its steps.
    private var sleepJobs: [Cancellable] = []
    /// The minutes timer's fade is running.
    private var sleepFading = false
    /// The master volume the fade set last (1: no fade).
    private var fadeLevel: Float = 1
    var onEvent: ((NativeEvent) -> Void)?
    /// The active kind changed (`RemoteCommands`: skip buttons for episodes).
    var onActiveChange: ((Item.Kind) -> Void)?
    /// Called synchronously right before sound starts (`AudioSessionController` activates the session there).
    var willStartPlayback: (() -> Void)?
    /// What was handed to the backend as the next item: loading that item again uses this same source, so the
    /// backend takes the preloaded item over (gapless) even when the track reached the cache meanwhile.
    private var preloadedNext: (kind: Item.Kind, id: String, source: MediaSource)?

    /// The lock screen and the car display; nil in engine tests that do not look at it.
    private let nowPlaying: NowPlayingController?
    /// What Now Playing shows now: it is written only when this changes (a car-lyrics line, play, pause), or on a seek.
    private struct NowPlayingKey: Equatable {
        let kind: Item.Kind; let id: String; let title: String; let artist: String; let album: String
        let playing: Bool; let rate: Double; let durationMs: Int?; let line: String?
    }
    private var nowPlayingKey: NowPlayingKey?
    private var forceNowPlaying = false
    /// The current track's lyrics for the car display (`prefs.carLyrics`), and the track they are for.
    private var carLyrics: CarLyrics?
    private var lyricsID: Int?
    /// The lyrics fetch for `lyricsID` threw: when to ask again, and the wait after the next failure.
    private var lyricsRetry: (at: Date, nextWait: Double)?
    private var lyricsInFlight: Int?

    /// What the backend holds: always the current item of `kind`'s queue.
    private struct Loaded { let kind: Item.Kind; var index: Int; let id: String }
    /// Bumped by every backend load (a retry too); callbacks with another generation are about a replaced item.
    private var generation = 0
    /// The last position the engine knows for the loaded item (load, ticks, seeks): a failed item can read 0.
    private var knownMs = 0
    private var stallTimer: Cancellable?
    /// Stopped because the network was gone: starts again when it comes back.
    private var stoppedOffline = false
    /// What the network coming back does for a player `stoppedOffline`: play the interrupted current item again
    /// (`.replay`), go on to the next one (a track that ended with only streams after it), or ask the server for
    /// favorites (a cold start with nothing on the phone).
    private enum OfflineResume { case replay, advance, shuffle }
    private var offlineResume = OfflineResume.replay
    private var loaded: Loaded?
    private var buffering = false
    private var error: String?

    /// The listen in progress, counted like the web's `PlayerProvider` (`finishListen`).
    private struct Listen { let trackId: Int; let startedAt: Int; var seconds: Double; var lastMs: Int }
    private var listen: Listen?
    private enum ListenEnd { case ended, skip, switched }

    private var failedAt: [String: Date] = [:]
    private var consecutiveFailures = 0
    private struct NetRetry { var attempt: Int; let positionMs: Int; var timer: Cancellable? }
    private var netRetry: NetRetry?
    private var lastProgress: Date?

    private var refilling = false
    private var refillExhausted = false
    private var refillTimer: Cancellable?
    /// A refill was due while offline.
    private var refillWanted = false
    /// Bumped whenever the music queue is replaced (or wiped): a refill for an older queue is dropped.
    private var musicGeneration = 0
    /// Bumped by `reset()`: async work for a signed-out user is dropped.
    private var epoch = 0

    private var savedAt = 0
    private var ownDevice: String?
    private var greeted = false

    init(backend: MediaBackend, api: LarkAPIProtocol, cache: CacheProviding, store: QueueStore, events: PlayEventQueue,
         network: NetworkStatus, now: @escaping () -> Date = Date.init, random: @escaping () -> Double = { .random(in: 0..<1) },
         scheduler: Scheduler? = nil, nowPlaying: NowPlayingController? = nil) {
        self.backend = backend; self.api = api; self.cache = cache; self.store = store; self.events = events
        self.nowPlaying = nowPlaying
        self.network = network; self.now = now; self.random = random
        let scheduler = scheduler ?? MainScheduler()
        self.scheduler = scheduler
        sync = QueueSync(api: api, scheduler: scheduler, now: now)
        backend.delegate = self
        sync.snapshot = { [unowned self] in
            let ids = music.items.compactMap(\.trackID)
            return .init(trackIDs: ids, index: ids.isEmpty ? 0 : min(music.index, ids.count - 1), positionMs: max(0, position(.track)))
        }
        sync.onOwnDevice = { [weak self] device in
            guard let self, self.ownDevice != device else { return }
            self.ownDevice = device
            self.persist(touch: false)
        }
        network.observe { [weak self] in self?.networkChanged() }
        restore()
    }

    // MARK: - Web messages

    /// Routes a bridge message. `auth`, `openSettings` and `favoriteChanged` belong to `AppServices`.
    func handle(_ m: WebMessage) {
        switch m {
        case .hello(let onOpen): hello(onOpen)
        case .setQueue(let sq): setQueue(sq)
        case .play(let k): play(k)
        case .pause(let k): if k == nil || k == active { pause() }
        case .next(let k): if k == active { next() } else { step(k, by: 1) }
        case .prev(let k): if k == active { prev() } else { step(k, by: -1) }
        case .seek(let k, let ms): if k == active { seek(ms: ms) } else { setPosition(k, ms) }
        case .skip(let k, let ms): if k == active { skip(ms: ms) } else { setPosition(k, queue(k).positionMs + ms) }
        case .setRate(let r): rate = r
        case .stop(let k): stop(k)
        case .setPrefs(let p): prefs = p
        case .setModes(let shuffle, let repeatMode): setModes(shuffle: shuffle, repeatMode: repeatMode)
        case .sleepTimer(let r): sleepTimer(r)
        case .pauseForWeb: pause()
        case .flushEvents(let id): flushEvents(id)
        case .auth, .favoriteChanged, .openSettings: break
        }
    }

    private func hello(_ onOpen: String) {
        emitAll()
        guard !greeted else { return }     // only the first hello after a launch; a web reload just gets the state
        greeted = true
        switch onOpen {
        case "resume": tasks.run { [weak self] in await self?.adoptServerQueue() }
        case "shuffle_favorites": if !playing { tasks.run { [weak self] in await self?.shuffleFavorites() } }
        default: break
        }
    }

    private func setQueue(_ sq: SetQueue) {
        let k = sq.kind
        guard !sq.items.isEmpty else { return stop(k) }
        let idx = min(max(0, sq.index), sq.items.count - 1)
        let item = sq.items[idx]

        // An edit (enqueue, remove, reorder, update): the current item stays, and so does playback.
        if sq.positionMs == nil, queue(k).current?.id == item.id {
            if k == .track { editedMusic(to: sq.items, index: idx) }
            modify(k) { $0.items = sq.items; $0.index = idx; $0.source = sq.source }
            if loaded?.kind == k { loaded?.index = idx }
            emitQueue(k); emitState(k); persist()
            if k == .track { sync.edited(); maybeRefill(); prefetchUpcoming() }
            return
        }

        let pos = max(0, sq.positionMs ?? 0)
        var new = PlaybackQueue(items: sq.items, index: idx, positionMs: pos, source: sq.source)
        if k == .track {
            musicGeneration += 1; refillExhausted = false; refillWanted = false; consecutiveFailures = 0
            refillTimer?.cancel(); refillTimer = nil
            substitutes = []
            new = shuffledIfOn(new)
        }
        if k != active && !sq.play {
            // The other kind, not to be played now: stored for later.
            modify(k) { $0 = new }
            emitQueue(k); emitState(k); persist()
            if k == .track { sync.edited(); maybeRefill() }
            return
        }
        let switched = k != active
        if switched { leaveActive(); active = k } else { saveOutgoingEpisode() }
        modify(k) { $0 = new }
        load(k, index: idx, startMs: pos, autoplay: sq.play)
        emitQueue(k)
        // The web mirrors each kind from its own state events: tell it the outgoing kind stopped (first, so
        // the new kind's state stays the latest).
        if switched { emitState(k == .track ? .episode : .track) }
        emitState(k); persist()
        if k == .track { sync.edited(); maybeRefill() }
    }

    private func play(_ k: Item.Kind) {
        guard k != active else { return play() }
        guard queue(k).current != nil else { return }
        leaveActive()
        active = k
        let q = queue(k)
        load(k, index: q.index, startMs: q.positionMs, autoplay: true)
        emitState(.track); emitState(.episode); persist()
    }

    private func stop(_ k: Item.Kind) {
        if loaded?.kind == k {
            syncPosition()
            if k == .episode { saveOutgoingEpisode() } else { finishListen(.switched) }
            cancelRetry()
            backend.stop()
            loaded = nil
            preloadedNext = nil
        }
        modify(k) { $0 = .empty }
        if k == .track { musicGeneration += 1 }
        if k == active { playing = false; buffering = false; error = nil }
        if k == .episode { active = .track }
        emitQueue(k); emitState(.track); emitState(.episode); persist()
        if k == .track { sync.edited() }
    }

    /// `flushEvents {id}`: the listen in progress is recorded and ended (the web's `finishListen("switch")`, no
    /// restart, so one play is never two events; the web sends it only on logout), everything pending is
    /// posted, then `flushed {id}` is sent whether the POST worked or not (the web waits at most 3 s anyway).
    private func flushEvents(_ id: String) {
        finishListen(.switched)
        tasks.run { [weak self] in
            guard let self else { return }
            await self.events.flush()
            self.onEvent?(.flushed(id: id))
        }
    }

    // MARK: - Transport (on the active kind)

    func play() {
        let q = queue(active)
        guard q.current != nil else { return }
        catchUpSleep(playing: false)
        pausedBySystem = false
        if loaded?.kind == active {
            willStartPlayback?()
            backend.play()
            playing = true
        } else {
            load(active, index: q.index, startMs: q.positionMs, autoplay: true)
        }
        emitState(active)
    }

    func pause() {
        pausedBySystem = false
        syncPosition()                                       // a waiting retry's position, not the failed item's
        disarmStall()
        stoppedOffline = false
        if loaded != nil { backend.pause() }
        if netRetry != nil { cancelRetry(); loaded = nil }    // a retry waiting: play() starts afresh
        let was = playing
        playing = false; buffering = false
        if active == .episode { saveOutgoingEpisode() } else if was { sync.edited() }
        persist(); emitState(active)
    }

    func toggle() { playing ? pause() : play() }

    /// An interruption began: paused, and marked so its end may resume it.
    func systemPause() {
        guard playing else { return }
        pause()
        pausedBySystem = true
    }

    /// The remote play's cold start: the session is activated now, before the async resume or shuffle.
    func prepareForPlayback() { willStartPlayback?() }

    /// A media services reset (`AudioSessionController`): the player is rebuilt, and the current item is
    /// loaded again where it was, playing only if it was. The reload is a new generation, so every late
    /// callback from the old player is dropped. The listen in progress goes on (one play, not two).
    func mediaServicesReset() {
        let l = loaded
        let wasPlaying = playing, wasSystemPaused = pausedBySystem
        syncPosition()
        cancelRetry(); disarmStall()
        preloadedNext = nil
        backend.rebuild()
        guard let l, queue(l.kind).current != nil else {
            loaded = nil
            if l != nil { playing = false; buffering = false; emitState(active) }
            return
        }
        let keep = listen
        listen = nil
        let q = queue(l.kind)
        load(l.kind, index: q.index, startMs: q.positionMs, autoplay: wasPlaying)
        if var k = keep, k.trackId == listen?.trackId { k.lastMs = q.positionMs; listen = k }
        if !wasPlaying { pausedBySystem = wasSystemPaused }
        forceNowPlaying = true
        emitState(active); persist()
    }

    /// The backend holds an item (playing or paused); false after a launch until something is played.
    var isLoaded: Bool { loaded != nil }
    /// The active queue's current item.
    var current: Item? { queue(active).current }

    func next() {
        cancelRetry()
        if active == .episode {
            guard episodes.index + 1 < episodes.items.count else { return }
            saveOutgoingEpisode()
            let i = episodes.index + 1
            load(.episode, index: i, startMs: Self.resumeAt(episodes.items[i]), autoplay: true)
            emitQueue(.episode); emitState(.episode); persist()
        } else {
            finishListen(.skip)
            dropSubstitutes()
            advanceMusic(user: true)
        }
    }

    func prev() {
        let q = queue(active)
        if position(active) > Self.restartPrevMs || q.index == 0 { return seek(ms: 0) }
        cancelRetry()
        let i = q.index - 1
        if active == .episode {
            saveOutgoingEpisode()
            load(.episode, index: i, startMs: Self.resumeAt(q.items[i]), autoplay: true)
        } else {
            finishListen(.skip)
            load(.track, index: i, startMs: 0, autoplay: true)
            sync.edited()
        }
        emitQueue(active); emitState(active); persist()
    }

    func seek(ms: Int) {
        let target = max(0, ms)
        guard loaded?.kind == active else { return setPosition(active, target) }
        disarmStall()
        backend.seek(ms: target)
        listen?.lastMs = target
        knownMs = target
        forceNowPlaying = true
        emitState(active)
    }

    func skip(ms: Int) {
        var target = position(active) + ms
        if let d = backend.durationMs ?? queue(active).current?.durationMs, d > 0 { target = min(target, d) }
        seek(ms: target)
    }

    /// The car's play with nothing loaded, and `ResumeIntent`: the saved queue from disk (the current item
    /// from the cache when it is there, before any network call); with no music queue, shuffled favorites.
    func resumeOrShuffleFavorites() async {
        if resumeFromDisk() { return }
        await shuffleFavorites()
    }

    /// The part of `resumeOrShuffleFavorites` that needs no network and no await: the saved queue, else cached
    /// favorites. True when it started playback; false when only the server's favorites are left to try.
    /// The car's play command runs this inside its handler, so sound starts before iOS can suspend the app.
    func resumeFromDisk() -> Bool {
        // Episodes always stream: with no network the car gets music (from the cache) instead of silence.
        if active == .episode && network.isOnline && episodes.current != nil { play(); return true }
        if music.current != nil {
            if let started = playLocalInsteadOfStream() { return started }
            if active == .track { play() } else { play(.track) }
            return true
        }
        if startCachedFavorites() || offlineWithNothingCached() { return true }
        if !network.answered {
            // A cold start before the network monitor's first answer, with nothing on the phone: no session is
            // taken (the car's own source keeps playing) and the server is asked once the network is known.
            stoppedOffline = true; offlineResume = .shuffle
            return true
        }
        return false
    }

    /// Offline, or before the network monitor's first answer (a cold launch), a saved current track that is not
    /// on the phone is not tried as a stream first: the next cached item in the queue plays, else a cached
    /// favorite, and the streamed track is queued right after it for later. Nil: play the current item as usual
    /// (it is cached, the network is known to be up, or nothing local exists and the network may be up).
    private func playLocalInsteadOfStream() -> Bool? {
        guard let cur = music.current, !network.isOnline || !network.answered, !isLocal(cur) else { return nil }
        let choice = chooseNext(mustBeLocal: true)
        guard choice != .none else { return network.isOnline ? nil : offlineWithNothingCached() }
        if active != .track { leaveActive(); active = .track; emitState(.episode) }
        if case .insert = choice, !network.isOnline { onEvent?(.notice(Self.offlineNotice)) }
        _ = apply(choice, requeue: cur)
        afterMusicMove()
        return true
    }

    /// The intents ask before they activate the session: why nothing can start (`resume`: the saved queue
    /// counts too), or nil when something may play. Signed out there is no token and no cache; offline, only
    /// what is on the phone can play.
    func intentBlocker(resume: Bool) -> LarkIntentError? {
        guard api.token != nil else { return .notSignedIn }
        guard !network.isOnline else { return nil }
        if !cache.cachedFavorites().isEmpty { return nil }
        if resume && music.items.contains(where: isLocal) { return nil }
        return .offlineNothingCached
    }

    /// Why the last attempt to play did not start (the state's `error`), for the intents' failure dialog.
    var failureText: String? { error }

    /// `ShuffleFavoritesIntent`, `onOpen: shuffle_favorites`: a shuffled favorites queue that refills itself.
    /// Cached favorites first (they start at once, with or without a network); with none, the server's
    /// random favorites; with no favorites at all, a shuffle of the whole library, and a notice says so.
    func shuffleFavorites() async {
        if startCachedFavorites() || offlineWithNothingCached() { return }
        let e = epoch
        var items: [Item] = []
        var source = QueueSource.favorites
        var notice: String?
        do {
            let r = try await api.randomFavorites(n: Self.shuffleSize, exclude: [])
            if r.source == "favorites" && !r.tracks.isEmpty {
                items = r.tracks.map { Item(track: $0.0, json: $0.1) }
            } else {
                let ts = r.tracks.isEmpty ? try await api.randomTracks(n: Self.shuffleSize, exclude: []) : r.tracks
                items = ts.map { Item(track: $0.0, json: $0.1) }
                source = .shuffle
                notice = Self.noFavoritesNotice
            }
        } catch {
            Self.log.info("shuffle favorites: the server could not be reached")
            if epoch == e { shuffleFailed(Self.shuffleFailedNotice) }
            return
        }
        guard epoch == e else { return }
        guard !items.isEmpty else { return shuffleFailed(Self.shuffleFailedNotice) }
        setQueue(SetQueue(kind: .track, items: items, index: 0, positionMs: 0, play: true, source: source))
        if let notice { onEvent?(.notice(notice)) }
    }

    /// Cached favorites, shuffled, playing now (no await). False when none are cached.
    private func startCachedFavorites() -> Bool {
        let cached = cache.cachedFavorites().map { Item(track: $0.0, json: $0.1) }
        guard !cached.isEmpty else { return false }
        let items = Array(NextChooser.shuffled(cached, random: random).prefix(Self.shuffleSize))
        setQueue(SetQueue(kind: .track, items: items, index: 0, positionMs: 0, play: true, source: .favorites))
        if !network.isOnline { onEvent?(.notice(Self.offlineNotice)) }
        return true
    }

    /// Offline with no cached favorite: nothing can play, and the state and a notice say why. True when offline.
    private func offlineWithNothingCached() -> Bool {
        guard !network.isOnline else { return false }
        shuffleFailed(Self.nothingCachedNotice)
        stoppedOffline = true; offlineResume = .shuffle          // the server's favorites once the network is back
        return true
    }

    /// Nothing to shuffle: the state carries why (Now Playing, the page) and a notice says it.
    private func shuffleFailed(_ text: String) {
        if !playing && active == .track { error = text }
        emitState(.track)
        onEvent?(.notice(text))
    }

    /// Every 10 s (a timer in `AppServices`): saves the position while playing, sends what is due.
    func tick() {
        if playing, loaded?.kind == active {
            persist()
            periodic()
        }
        events.flushIfDue()
    }

    /// Sign-out or a user switch: stop, forget both queues and every pending event, on disk too.
    func reset() {
        epoch += 1; musicGeneration += 1
        tasks.cancelAll(); sync.cancel()
        refillTimer?.cancel(); refillTimer = nil; refillWanted = false
        cancelRetry(); disarmStall(); stoppedOffline = false
        clearSleep()
        backend.stop()
        loaded = nil; listen = nil; playing = false; buffering = false; error = nil; pausedBySystem = false
        lyricsID = nil; carLyrics = nil; lyricsRetry = nil; preloadedNext = nil
        music = .empty; episodes = .empty; active = .track
        failedAt = [:]; consecutiveFailures = 0; refilling = false; refillExhausted = false
        modes = PlayModes(); substitutes = []
        savedAt = 0; ownDevice = nil; lastProgress = nil
        greeted = false               // the next user's page gets its `hello{onOpen}` honoured
        events.clear()
        store.wipe()
        emitAll()
    }

    /// Reads the current user's saved queues (a launch, or after `QueueStore.user` changed). Paused; nothing loads.
    func restore() {
        let s = store.load()
        music = s?.music ?? .empty
        episodes = s?.episodes ?? .empty
        active = s?.active ?? .track
        savedAt = s?.savedAt ?? 0
        ownDevice = s?.ownDevice
        modes = s?.modes ?? PlayModes()        // a file from before the modes: both off
        substitutes = []
        loaded = nil; listen = nil; playing = false; buffering = false; error = nil
    }

    /// Sign-out, while the token still works: playback stops, the listen in progress is recorded and every
    /// pending event gets one POST.
    func flushBeforeSignOut() async {
        syncPosition()
        if loaded != nil { backend.pause() }
        playing = false
        finishListen(.switched)
        await events.flush()
    }

    /// Re-sends both queues and states (a web reload, or the scene coming back: the page missed events).
    func emitAll() {
        emitQueue(.track); emitQueue(.episode); emitState(.track); emitState(.episode)
    }

    /// Waits for every async piece of work started so far (tests).
    func idle() async {
        repeat {
            await tasks.idle(); await events.tasks.idle(); await sync.tasks.idle()
        } while !(tasks.isEmpty && events.tasks.isEmpty && sync.tasks.isEmpty)
    }

    // MARK: - Shuffle and repeat

    /// `setModes`, the lock screen and the car. Shuffle reorders only what follows the current item, which plays
    /// on: nothing is loaded. Music only; episodes keep their own order.
    func setModes(shuffle: Bool, repeatMode: RepeatMode) {
        var m = modes
        let reorder = shuffle != m.shuffle
        if reorder {
            if shuffle {
                let (q, original) = PlayModes.shuffleUpcoming(music, random: random)
                music = q; m.original = original
            } else {
                music = PlayModes.unshuffle(music, original: m.original ?? [])
                m.original = nil
            }
            m.shuffle = shuffle
        }
        m.repeatMode = repeatMode
        guard m != modes else { return emitState(.track) }
        modes = m
        persist()
        if reorder { emitQueue(.track) }
        emitState(.track)
        if reorder { sync.edited() }
        prefetchUpcoming()
        if loaded?.kind == .track { preloadNext(.track) }
        maybeRefill()                         // repeat all turned off at the end: never-stop again
    }

    /// Shuffle on: a new list plays shuffled after its chosen item (the web's `playList`); the same queue again
    /// (a jump) is not reshuffled.
    private func shuffledIfOn(_ q: PlaybackQueue) -> PlaybackQueue {
        guard modes.shuffle, q.items.map(\.id) != music.items.map(\.id) else { return q }
        let (s, original) = PlayModes.shuffleUpcoming(q, random: random)
        modes.original = original
        return s
    }

    /// A user's edit of the music queue (it is about to replace `music`): the pre-shuffle order follows it, and
    /// a substitute's requeued copy taken out keeps the interrupted entry in the loop.
    private func editedMusic(to items: [Item], index: Int) {
        let new = PlaybackQueue(items: items, index: index, positionMs: 0, source: music.source)
        if modes.shuffle, let o = modes.original {
            modes.original = PlayModes.edited(original: o, from: music, to: new)
        }
        guard !substitutes.isEmpty, items.count + 1 == music.items.count else { return }
        let old = music.items
        let r = items.indices.first(where: { old[$0].id != items[$0].id }) ?? items.count
        guard (old[..<r] + old[(r + 1)...]).map(\.id) == items.map(\.id) else { return }   // one entry taken out
        for (n, sub) in substitutes.enumerated() where sub.requeue == old[r].id {
            if let i = Self.locate(sub.id, in: old, near: music.index), r > i { substitutes[n].requeue = nil }
        }
    }

    /// Where a substitute is: its id at or before the current item (it was played from there), else after it.
    private static func locate(_ id: String, in items: [Item], near index: Int) -> Int? {
        guard !items.isEmpty else { return nil }
        return items[...min(index, items.count - 1)].lastIndex(where: { $0.id == id }) ?? items.firstIndex(where: { $0.id == id })
    }

    /// Repeat all: each substitute leaves the queue, with the interrupted item's old entry (its requeued copy
    /// stays, to be tried again); with nothing before it left to stand on, the old entry stays and the copy
    /// goes. Any other mode: forgotten, nothing removed.
    private func dropSubstitutes() {
        let subs = substitutes
        substitutes = []
        guard !subs.isEmpty, modes.repeatMode == .all else { return }
        var items = music.items, index = music.index
        for sub in subs {
            guard let i = Self.locate(sub.id, in: items, near: index) else { continue }
            let k = sub.requeue.flatMap { r in items[..<i].lastIndex(where: { $0.id == r }) }
            let copy = sub.requeue.flatMap { r in items[(i + 1)...].firstIndex(where: { $0.id == r }) }
            func indexWithout(_ d: Set<Int>) -> Int { index - d.filter { $0 < index }.count - (d.contains(index) ? 1 : 0) }
            var drop: Set<Int> = k.map { Set([i, $0]) } ?? Set([i])
            if indexWithout(drop) < 0, let copy { drop = [i, copy] }
            let ni = max(0, indexWithout(drop))
            items = items.enumerated().filter { !drop.contains($0.offset) }.map(\.element)
            index = min(ni, max(0, items.count - 1))
        }
        music.items = items
        music.index = index
        if loaded?.kind == .track { loaded?.index = index }
    }

    // MARK: - Sleep timer

    /// `sleepTimer`: one timer at a time, so a new choice replaces the one set (and undoes its fade).
    func sleepTimer(_ r: SleepRequest) {
        clearSleep(preloadAgain: r != .endOfTrack)
        switch r {
        case .cancel:
            break
        case .minutes(let n):
            let s = Double(n) * 60
            sleep = .at(now().addingTimeInterval(s))
            sleepJobs = [scheduler.after(max(0, s - Self.sleepFadeS)) { [weak self] in self?.startSleepFade() }]
        case .endOfTrack:
            sleep = .endOfTrack
            if let l = loaded { preloadNext(l.kind) }           // nothing next: the queue player stops at the end
        }
        emitStates()
    }

    /// The minutes timer's last 10 s: the volume steps down to 0, then playback pauses.
    private func startSleepFade() {
        guard !sleepFading else { return }
        sleepJobs.forEach { $0.cancel() }
        sleepFading = true
        let n = Self.sleepFadeSteps, step = Self.sleepFadeS / Double(n)
        sleepJobs = (1...n).map { i in
            scheduler.after(Double(i) * step) { [weak self] in
                guard let self else { return }
                self.setFade(Float(n - i) / Float(n))
                if i == n { self.sleepEnded() }
            }
        }
    }

    /// The deadline: paused (it stays paused if it already was), the volume back for the next play.
    private func sleepEnded() {
        sleepJobs = []; sleepFading = false
        pause()
        setFade(1)
        sleep = nil
        emitStates()
    }

    /// No timer: its jobs cancelled, the volume back, and the next item preloaded again after an end-of-track one.
    private func clearSleep(preloadAgain: Bool = true) {
        let wasEndOfTrack: Bool
        if case .endOfTrack = sleep { wasEndOfTrack = true } else { wasEndOfTrack = false }
        sleepJobs.forEach { $0.cancel() }
        sleepJobs = []; sleepFading = false
        sleep = nil
        if fadeLevel != 1 { setFade(1) }
        if wasEndOfTrack, preloadAgain, let l = loaded { preloadNext(l.kind) }
    }

    /// The scheduler's clock stops while the phone sleeps (paused and locked), so its job can come late; the
    /// deadline is wall time. Past the fade's start, the fade starts now. Past the deadline itself before a play,
    /// the timer is over: it would have paused a player that was paused anyway, and the play goes on.
    private func catchUpSleep(playing: Bool) {
        guard case .at(let deadline) = sleep, !sleepFading else { return }
        let t = now()
        if t >= deadline && !playing {
            clearSleep()
            emitStates()
        } else if t >= deadline.addingTimeInterval(-Self.sleepFadeS) {
            startSleepFade()
        }
    }

    private func setFade(_ level: Float) {
        fadeLevel = level
        backend.setVolume(level)
    }

    /// "End of this track": the volume follows what is left of the item in its last 10 s.
    private func fadeTowardsTheEnd(positionMs: Int) {
        guard case .endOfTrack = sleep, let d = backend.durationMs ?? current?.durationMs, d > 0 else { return }
        let level = Float(min(1, max(0, Double(d - positionMs) / (Self.sleepFadeS * 1000))))
        if level != fadeLevel { setFade(level) }
    }

    /// What the state says is left: to the deadline, or of the item playing; nil with no timer.
    private var sleepRemainingMs: Int? {
        switch sleep {
        case nil:
            return nil
        case .at(let deadline):
            return max(0, Int((deadline.timeIntervalSince(now()) * 1000).rounded()))
        case .endOfTrack:
            let d = (loaded?.kind == active ? backend.durationMs : nil) ?? current?.durationMs ?? 0
            return max(0, d - position(active))
        }
    }

    // MARK: - Loudness

    /// The loudness switch: the loaded track's level and the preloaded next one's change in place (nothing is
    /// preloaded again, so a switch in a track's last seconds keeps the change gapless).
    private func loudnessChanged() {
        guard let l = loaded, l.kind == .track, let item = music.current else { return }
        let next = preloadedNext.flatMap { p in queue(p.kind).items.first { $0.id == p.id } }
        backend.setGain(Gain.factor(item, enabled: prefs.loudness), next: next.map { Gain.factor($0, enabled: prefs.loudness) } ?? 1)
    }

    // MARK: - Backend events

    /// A callback about the item loaded last; anything else is about a replaced item and is dropped.
    private func isCurrent(_ g: Int) -> Bool { loaded != nil && g == generation }

    func backendStarted(generation g: Int) {
        guard isCurrent(g) else { return }
        cancelRetry()            // the retry worked: the next blip gets a fresh set, from where it is then
        disarmStall()
        playing = true; buffering = false; error = nil
        if loaded?.kind == .track { consecutiveFailures = 0 }
        prefetchUpcoming()
        emitState(active)
    }

    /// Paused by something other than the engine (an interruption, a route change).
    func backendPaused(generation g: Int) {
        guard isCurrent(g), playing else { return }
        disarmStall()
        syncPosition()
        playing = false; buffering = false; pausedBySystem = true
        if active == .episode { saveOutgoingEpisode() }
        persist(); emitState(active)
    }

    func backendBuffering(_ on: Bool, generation g: Int) {
        guard isCurrent(g) else { return }
        buffering = on
        if on { armStall() } else { disarmStall() }
        emitState(active)
    }

    func backendTick(positionMs: Int, generation g: Int) {
        guard isCurrent(g) else { return }
        knownMs = positionMs
        if var l = listen, loaded?.kind == .track {
            let d = positionMs - l.lastMs
            if d > 0 && d < 2000 { l.seconds += Double(d) / 1000 }
            l.lastMs = positionMs
            listen = l
        }
        fadeTowardsTheEnd(positionMs: positionMs)
        catchUpSleep(playing: true)
        emitState(active)
        periodic()
        retryLyricsIfDue()
    }

    func backendFinished(generation g: Int) {
        guard isCurrent(g), let l = loaded else { return }
        cancelRetry(); disarmStall()
        if case .endOfTrack = sleep { return sleepAtTheEnd(l) }
        if l.kind == .track {
            finishListen(.ended)
            consecutiveFailures = 0
            if modes.repeatMode == .one, music.current?.id == l.id {
                // Repeat one: the same item again (the preloaded copy of it: gapless), a new listen and so a new play.
                load(.track, index: music.index, startMs: 0, autoplay: true)
                afterMusicMove()
                return
            }
            dropSubstitutes()
            advanceMusic(user: false)
        } else {
            sendProgress(id: l.id, positionS: 0, played: true, emit: false)
            if episodes.index + 1 < episodes.items.count {
                let i = episodes.index + 1
                load(.episode, index: i, startMs: Self.resumeAt(episodes.items[i]), autoplay: true)
                emitQueue(.episode)
            } else {
                loaded = nil; playing = false; buffering = false
                modify(.episode) { $0.positionMs = 0 }
            }
            emitState(.episode); persist()
        }
    }

    /// "End of this track" reached: music moves on to the next track, loaded but paused (repeat one gives way);
    /// an episode stays where it ended. Then the timer is off and the volume back for the next play.
    private func sleepAtTheEnd(_ l: Loaded) {
        sleep = nil; sleepJobs = []
        if l.kind == .track {
            finishListen(.ended)
            consecutiveFailures = 0
            dropSubstitutes()
            advanceMusic(user: false, autoplay: false)
        } else {
            sendProgress(id: l.id, positionS: 0, played: true, emit: true)
            loaded = nil; playing = false; buffering = false
            modify(.episode) { $0.positionMs = 0 }
            persist()
        }
        setFade(1)
        emitStates()
    }

    func backendFailed(network isNetwork: Bool, generation g: Int) {
        guard isCurrent(g), let l = loaded, let item = queue(l.kind).current else { return }
        buffering = false
        disarmStall()
        let pos = backend.positionMs > 0 ? backend.positionMs : knownMs
        if isNetwork && network.isOnline {
            let attempt = netRetry?.attempt ?? 0
            if attempt < Self.networkRetryDelays.count {
                // From where it is now: a retry that played on and failed again resumes there, not at the first blip.
                netRetry?.timer?.cancel()
                netRetry = NetRetry(attempt: attempt + 1, positionMs: max(pos, netRetry?.positionMs ?? 0),
                                    timer: scheduler.after(Self.networkRetryDelays[attempt]) { [weak self] in self?.retryNow() })
                emitState(l.kind)
                return
            }
        }
        cancelRetry()
        if l.kind == .episode {
            // Offline, or the retries ran out, or the file itself: the episode keeps its place, music plays instead.
            let wanted = playing
            modify(.episode) { $0.positionMs = pos }
            loaded = nil; playing = false; error = Self.cannotPlay
            saveOutgoingEpisode(id: item.id, positionMs: pos)
            emitState(.episode); persist()
            if wanted { fallBackToMusic() }
            return
        }
        finishListen(.switched)
        if isNetwork && !network.isOnline {
            // Offline: something on the phone now; the interrupted item is requeued to play once online.
            // It is not marked failed: nothing is wrong with it.
            if apply(chooseNext(mustBeLocal: true), requeue: item) {
                onEvent?(.notice(Self.offlineNotice))
                afterMusicMove()
            } else {
                modify(.track) { $0.positionMs = pos }
                stopForNetwork()
            }
            return
        }
        failedAt[item.id] = now()
        consecutiveFailures += 1
        // A network error that outlasted its retries moves on to something on the phone.
        advanceMusic(user: false, forceLocal: isNetwork)
    }

    // MARK: - Network, stalls, fallbacks

    private func networkChanged() {
        guard network.isOnline else { return }
        if refillTimer != nil || refillWanted {
            refillTimer?.cancel(); refillTimer = nil
            refillWanted = false
            maybeRefill()
        }
        if stoppedOffline {
            stoppedOffline = false
            error = nil
            switch offlineResume {
            case .shuffle:
                tasks.run { [weak self] in await self?.shuffleFavorites() }
            case .advance where active == .track && music.current != nil:
                advanceMusic(user: false)
            case .replay where active == .track && music.current != nil:
                play()
            default:
                emitState(active)
            }
            offlineResume = .replay
        }
    }

    /// Nothing on the phone to play and no network: stopped, and started again when the network is back.
    private func stopForNetwork() {
        loaded = nil; playing = false; buffering = false
        error = Self.offlineStopped
        stoppedOffline = true; offlineResume = .replay
        emitQueue(.track); emitState(.track); persist()
    }

    private func armStall() {
        guard stallTimer == nil, playing, let l = loaded, l.kind == .track, active == .track,
              let cur = music.current, !isLocal(cur) else { return }
        stallTimer = scheduler.after(Self.stallSeconds) { [weak self] in self?.stallSwitch() }
    }

    private func disarmStall() {
        stallTimer?.cancel()
        stallTimer = nil
    }

    /// The web's stall switch: a remote track that is not moving gives way to one on the phone (only a local
    /// choice is taken), and the interrupted track is requeued right after it.
    private func stallSwitch() {
        stallTimer = nil
        guard playing, buffering, let l = loaded, l.kind == .track, let cur = music.current, !isLocal(cur) else { return }
        let c = chooseNext(mustBeLocal: true)
        guard c != .none else { return }
        finishListen(.switched)
        _ = apply(c, requeue: cur)
        onEvent?(.notice(Self.stallNotice))
        afterMusicMove()
    }

    /// An episode that cannot play while it was meant to: music plays instead (the queue, else cached
    /// favorites), and the page is told why.
    private func fallBackToMusic() {
        onEvent?(.notice(Self.episodeFallbackNotice))
        if music.current != nil { play(.track) } else { tasks.run { [weak self] in await self?.shuffleFavorites() } }
    }

    // MARK: - Internals

    private func queue(_ k: Item.Kind) -> PlaybackQueue { k == .track ? music : episodes }

    private func modify(_ k: Item.Kind, _ f: (inout PlaybackQueue) -> Void) {
        if k == .track { f(&music) } else { f(&episodes) }
    }

    private func position(_ k: Item.Kind) -> Int { loaded?.kind == k ? livePosition() : queue(k).positionMs }

    /// While a retry waits, its position (the failed item can read 0); otherwise the backend's.
    private func livePosition() -> Int { netRetry?.positionMs ?? backend.positionMs }

    private func setPosition(_ k: Item.Kind, _ ms: Int) {
        modify(k) { $0.positionMs = max(0, ms) }
        emitState(k); persist()
    }

    /// Moves the other kind's index without playing anything.
    private func step(_ k: Item.Kind, by d: Int) {
        let i = queue(k).index + d
        guard queue(k).items.indices.contains(i) else { return }
        modify(k) { $0.index = i; $0.positionMs = 0 }
        emitQueue(k); emitState(k); persist()
        if k == .track { sync.edited(); maybeRefill() }
    }

    /// The backend's position into the active queue.
    private func syncPosition() {
        guard let l = loaded else { return }
        let pos = livePosition()
        modify(l.kind) { $0.positionMs = pos }
    }

    private func persist(touch: Bool = true) {
        syncPosition()
        if touch { savedAt = Int(now().timeIntervalSince1970) }
        store.save(QueueSnapshot(version: QueueSnapshot.currentVersion, music: music, episodes: episodes, active: active, savedAt: savedAt, ownDevice: ownDevice, modes: modes))
    }

    private func isFailed(_ item: Item) -> Bool {
        failedAt[item.id].map { now().timeIntervalSince($0) < Self.failedTTL } ?? false
    }

    private func isLocal(_ item: Item) -> Bool { item.trackID.flatMap(cache.localURL) != nil }

    private func mediaSource(_ item: Item) -> MediaSource? {
        if let id = item.trackID, let url = cache.localURL(trackID: id) { return .file(url) }
        guard let token = api.token else { return nil }
        return .remote(api.streamURL(item, quality: prefs.quality), token: token)
    }

    /// Loads `queue(k).items[index]` into the backend (the caller has saved whatever was playing).
    private func load(_ k: Item.Kind, index: Int, startMs: Int, autoplay: Bool) {
        finishListen(.switched)
        cancelRetry(); disarmStall()
        stoppedOffline = false; pausedBySystem = false
        modify(k) { $0.index = index; $0.positionMs = startMs }
        let preloaded = preloadedNext
        preloadedNext = nil
        guard let item = queue(k).current else { return }
        if let id = item.trackID { fetchLyrics(id) } else { lyricsID = nil; carLyrics = nil; lyricsRetry = nil }
        let same = preloaded.flatMap { $0.kind == k && $0.id == item.id ? $0.source : nil }
        guard let src = same ?? mediaSource(item) else {
            // Not on the phone and no token to stream it: a failure of this item, on to something on the phone.
            loaded = nil; buffering = false
            if k == .track && autoplay {
                failedAt[item.id] = now()
                consecutiveFailures += 1
                advanceMusic(user: false, forceLocal: true)
            } else {
                playing = false; error = Self.cannotPlay
            }
            return
        }
        if case .file = src, let id = item.trackID { cache.touch(trackID: id) }
        loaded = Loaded(kind: k, index: index, id: item.id)
        playing = autoplay; buffering = autoplay; error = nil
        if let id = item.trackID {
            listen = Listen(trackId: id, startedAt: Int(now().timeIntervalSince1970), seconds: 0, lastMs: startMs)
        }
        if k == .episode { lastProgress = now() }
        knownMs = startMs
        generation += 1
        if case .endOfTrack = sleep, fadeLevel != 1 { setFade(1) }    // the timer goes on with this item, from full volume
        if autoplay { willStartPlayback?() }
        backend.load(src, startMs: startMs, autoplay: autoplay, rate: k == .episode ? rate : 1,
                     gain: Gain.factor(item, enabled: prefs.loudness), generation: generation)
        preloadNext(k)
    }

    /// The next item into the backend for a gapless change.
    /// Repeat one: the current item again (the loop is gapless: `load` matches the preloaded source on the id).
    /// Repeat all without shuffle, at the last item: the first one, which the next pass starts with.
    /// "End of this track" armed: nothing, so the queue player stops at the end instead of running into the next.
    private func preloadNext(_ k: Item.Kind) {
        if case .endOfTrack = sleep {
            preloadedNext = nil
            backend.preload(nil, gain: 1)
            return
        }
        let q = queue(k)
        let usable = { [unowned self] (i: Item) in k == .episode || (!self.isFailed(i) && (self.network.isOnline || self.isLocal(i))) }
        var next = q.upcoming.first(where: usable)
        if k == .track && modes.repeatMode == .one {
            next = q.current
        } else if k == .track, next == nil, modes.repeatMode == .all, !modes.shuffle, let first = q.items.first, usable(first) {
            next = first
        }
        let src = next.flatMap(mediaSource)
        // A stream preloaded here is also downloaded by the lookahead: accepted, so the change stays gapless.
        preloadedNext = next.flatMap { n in src.map { (k, n.id, $0) } }
        backend.preload(src, gain: next.map { Gain.factor($0, enabled: prefs.loudness) } ?? 1)
    }

    /// The lookahead: the next 2 tracks into the cache, once the current music item sounds (so its own
    /// stream starts first), and again when the queue changes under it. The cache skips what it has or is
    /// already fetching, and downloads on any network.
    /// Repeat one: the current track first, when it streams, so the loops after the first play from the phone.
    private func prefetchUpcoming() {
        guard let l = loaded, l.kind == .track, playing, !buffering else { return }
        var next = Array(music.upcoming.prefix(2))
        if modes.repeatMode == .one, let cur = music.current, !isLocal(cur) { next.insert(cur, at: 0) }
        if !next.isEmpty { cache.prefetch(next) }
    }

    private func retryNow() {
        guard var r = netRetry, let l = loaded, let item = queue(l.kind).current, let src = mediaSource(item) else { return }
        r.timer = nil; netRetry = r
        playing = true; buffering = true
        listen?.lastMs = r.positionMs
        knownMs = r.positionMs
        generation += 1
        willStartPlayback?()
        backend.load(src, startMs: r.positionMs, autoplay: true, rate: l.kind == .episode ? rate : 1,
                     gain: Gain.factor(item, enabled: prefs.loudness), generation: generation)
        preloadNext(l.kind)          // the load dropped the preloaded next item
        emitState(l.kind)
    }

    private func cancelRetry() {
        netRetry?.timer?.cancel()
        netRetry = nil
    }

    /// Leaves the active kind for the other: its position stays in its queue, the listen or the episode's
    /// progress is saved.
    private func leaveActive() {
        guard loaded?.kind == active else { return }
        syncPosition()
        cancelRetry(); disarmStall()
        if active == .track { finishListen(.switched) } else { saveOutgoingEpisode() }
        backend.pause()
        loaded = nil; playing = false; buffering = false; error = nil
    }

    private func saveOutgoingEpisode(emit: Bool = true) {
        guard let l = loaded, l.kind == .episode else { return }
        saveOutgoingEpisode(id: l.id, positionMs: livePosition(), emit: emit)
    }

    private func saveOutgoingEpisode(id: String, positionMs: Int, emit: Bool = true) {
        sendProgress(id: id, positionS: max(0, positionMs) / 1000, played: false, emit: emit)
    }

    /// `PUT /episodes/{id}/progress`, and the same values into every queued copy of the episode's `meta`
    /// (as the web patches its snapshots), so going back or a relaunch resumes where it was. The queue goes
    /// to the page on a pause, switch, next or end (`emit`), not on the 15 s save.
    private func sendProgress(id: String, positionS: Int, played: Bool, emit: Bool) {
        var changed = false
        episodes.items = episodes.items.map { ep in
            guard ep.id == id else { return ep }
            var meta = ep.meta.setting("position_s", .int(played ? 0 : positionS))
            if played { meta = meta.setting("played", .bool(true)) }
            guard meta != ep.meta else { return ep }
            changed = true
            return Item(kind: ep.kind, id: ep.id, title: ep.title, artist: ep.artist, album: ep.album, durationMs: ep.durationMs, meta: meta)
        }
        if changed {
            persist()
            if emit { emitQueue(.episode) }
        }
        lastProgress = now()
        tasks.run { [api] in
            // A failure is not retried: the next save (15 s, pause, the end) carries a newer position anyway.
            try? await api.episodeProgress(id, positionS: positionS, played: played)
        }
    }

    /// What is due while playing: `PUT /queue` every 15 s for music, episode progress every 15 s.
    private func periodic() {
        guard playing, let l = loaded, l.kind == active else { return }
        if l.kind == .track {
            sync.playingTick()
        } else if lastProgress.map({ now().timeIntervalSince($0) >= Self.progressEvery }) ?? true {
            saveOutgoingEpisode(emit: false)
        }
    }

    private func finishListen(_ end: ListenEnd) {
        guard let l = listen else { return }
        listen = nil
        guard l.seconds >= 1 else { return }
        events.add(trackId: l.trackId, startedAt: l.startedAt, playedSeconds: Int(l.seconds.rounded()),
                   skipped: end == .skip && l.seconds < Self.skipThresholdS, quality: prefs.quality)
    }

    /// `wrap` (repeat all, on the paths the web's `changeOpts`/`onError` pass it): start over at the end. The
    /// local-only switches (a stall, offline) never wrap: they want something on the phone now.
    private func chooseNext(mustBeLocal: Bool, wrap: Bool = false) -> NextChooser.Choice {
        NextChooser.choose(music, mustBeLocal: mustBeLocal, failed: isFailed, isLocal: isLocal,
                           favorites: cache.cachedFavorites().map { Item(track: $0.0, json: $0.1) }, random: random, wrap: wrap)
    }

    /// Plays `c` next: a queued item moved up to just after the current one, or a cached favorite inserted
    /// there; `requeue` (an interrupted item) goes right after it. False for `.none`. `autoplay` false: loaded,
    /// not played (the sleep timer's end of track).
    private func apply(_ c: NextChooser.Choice, requeue: Item? = nil, autoplay: Bool = true) -> Bool {
        switch c {
        case .queue(let j):
            if j != music.index + 1 {
                let item = music.items.remove(at: j)
                music.items.insert(item, at: music.index + 1)
            }
        case .insert(let item):
            music.items.insert(item, at: music.index + 1)
            if modes.repeatMode == .all { substitutes.append(Substitute(id: item.id, requeue: requeue?.id)) }
        case .wrap(let mustBeLocal):
            // Repeat all: the whole queue again, from its first item that may play (not failed; on the phone
            // offline or after failures). The chooser only wraps when there is one.
            dropSubstitutes()                 // never part of the next pass
            music = PlayModes.newPass(music, shuffle: modes.shuffle, random: random)
            let local = mustBeLocal || !network.isOnline
            guard let start = music.items.firstIndex(where: { !isFailed($0) && (!local || isLocal($0)) }) else { return false }
            load(.track, index: start, startMs: 0, autoplay: autoplay)
            return true
        case .none:
            return false
        }
        if let requeue { music.items.insert(requeue, at: music.index + 2) }
        load(.track, index: music.index + 1, startMs: 0, autoplay: autoplay)
        return true
    }

    private func afterMusicMove() {
        emitQueue(.track); emitState(.track); persist()
        sync.edited()
        maybeRefill()
    }

    /// Picks and loads the next music item (the end of one, a failure, or the user's next).
    private func advanceMusic(user: Bool, forceLocal: Bool = false, autoplay: Bool = true) {
        let offline = !network.isOnline
        let mustBeLocal = forceLocal || offline || consecutiveFailures >= Self.maxConsecutiveFailures
        let choice = chooseNext(mustBeLocal: mustBeLocal, wrap: modes.repeatMode == .all)
        if case .insert = choice, offline { onEvent?(.notice(Self.offlineNotice)) }
        if !apply(choice, autoplay: autoplay) {
            if user { return }      // nothing to go to: the user's next leaves things as they are
            // The end, or nothing playable: stopped, and play starts the current item again.
            let failed = consecutiveFailures > 0 || forceLocal
            playing = false; buffering = false; loaded = nil
            if failed { error = Self.cannotPlay } else { music.positionMs = 0 }
            if offline {
                if failed {
                    // The current item could not play: it is tried again when the network is back.
                    error = Self.offlineStopped; stoppedOffline = true; offlineResume = .replay
                } else if !music.upcoming.isEmpty {
                    // It ended, and only streams are next: the next one plays when the network is back.
                    error = Self.offlineStopped; stoppedOffline = true; offlineResume = .advance
                }
                // The queue ended normally: stopped, as online. Nothing replays on reconnect.
            }
        }
        afterMusicMove()
    }

    /// Radio, shuffle and favorites queues refill when 2 or fewer playable items are left
    /// (the web's refill effect: 20 favorites or random tracks, 10 radio tracks).
    private func maybeRefill() {
        guard modes.repeatMode != .all else { return }     // repeat all loops the queue as it is
        guard !refilling, !refillExhausted, refillTimer == nil, !music.items.isEmpty,
              [.favorites, .shuffle, .radio].contains(music.source) else { return }
        let upcoming = music.upcoming.filter { !isFailed($0) }
        guard upcoming.count <= 2 else { return }
        // Offline the request can only fail: it is asked for when the network comes back (`networkChanged`).
        guard network.isOnline else { refillWanted = true; return }
        refillWanted = false
        refilling = true
        let src = music.source, gen = musicGeneration
        // Favorites loop, so only what is still upcoming is excluded; radio and shuffle exclude the recent queue.
        let exclude = src == .favorites ? upcoming.compactMap(\.trackID) : music.items.suffix(Self.refillExcludeMax).compactMap(\.trackID)
        tasks.run { [weak self, api] in
            var answer: (tracks: [(Track, JSONValue)], source: QueueSource)?
            do {
                switch src {
                case .favorites:
                    let r = try await api.randomFavorites(n: 20, exclude: exclude)
                    answer = (r.tracks, r.source == "favorites" ? .favorites : .shuffle)
                case .shuffle: answer = (try await api.randomTracks(n: 20, exclude: exclude), .shuffle)
                default: answer = (try await api.radio(n: 10, exclude: exclude), .radio)
                }
            } catch {}
            guard let self else { return }
            self.refilling = false
            guard gen == self.musicGeneration else { return self.maybeRefill() }   // the queue was replaced meanwhile
            guard let answer else {
                self.refillTimer = self.scheduler.after(Self.refillRetry) { [weak self] in
                    self?.refillTimer = nil
                    self?.maybeRefill()
                }
                return
            }
            let add = self.appendable(answer.tracks.map { Item(track: $0.0, json: $0.1) }, source: answer.source)
            if !add.isEmpty {
                self.music.items += add
                self.music.source = answer.source
                self.emitQueue(.track); self.persist(); self.sync.edited()
                self.prefetchUpcoming()
            } else if answer.source != .favorites || answer.tracks.isEmpty {
                self.refillExhausted = true
            }
        }
    }

    /// Never a track twice in one answer, nor one already queued; a favorites refill loops, so it only
    /// skips what is still upcoming (the web's `appendable`).
    private func appendable(_ items: [Item], source: QueueSource) -> [Item] {
        var have = Set((source == .favorites ? Array(music.upcoming) : music.items).map(\.id))
        return items.filter { have.insert($0.id).inserted }
    }

    /// The first hello's `resume`: the server's queue replaces the local one when `QueueSync.shouldAdopt` says so.
    private func adoptServerQueue() async {
        guard !playing else { return }
        let e = epoch
        guard let (sq, tracks) = try? await api.queue(), epoch == e,
              QueueSync.shouldAdopt(sq, localEmpty: music.items.isEmpty, savedAt: savedAt, ownDevice: ownDevice, playing: playing)
        else { return }
        var byID: [Int: (Track, JSONValue)] = [:]
        for t in tracks where byID[t.0.id] == nil { byID[t.0.id] = t }
        var items: [Item] = []
        var index = 0
        var currentFound = false
        for (i, id) in sq.track_ids.enumerated() {
            if i == sq.current_index { index = items.count; currentFound = byID[id] != nil }
            if let t = byID[id] { items.append(Item(track: t.0, json: t.1)) }
        }
        guard !items.isEmpty else { return }
        if loaded?.kind == .track { backend.stop(); loaded = nil; listen = nil }
        musicGeneration += 1; refillExhausted = false
        music = PlaybackQueue(items: items, index: min(index, items.count - 1),
                              positionMs: currentFound ? max(0, sq.position_ms) : 0, source: .restored)
        emitQueue(.track); emitState(.track); persist()
    }

    /// Where an episode starts: its saved position, unless played, unstarted or nearly finished (the web's `resumeAt`).
    static func resumeAt(_ ep: Item) -> Int {
        func int(_ v: JSONValue?) -> Int? {
            switch v { case .int(let i)?: return i; case .double(let d)?: return Int(d); default: return nil }
        }
        if ep.meta["played"] == .bool(true) { return 0 }
        let pos = int(ep.meta["position_s"]) ?? 0
        guard pos > 0 else { return 0 }
        let dur = int(ep.meta["duration_s"]) ?? ep.durationMs / 1000
        if dur > 0 && pos > dur - nearEndS { return 0 }
        return pos * 1000
    }

    // MARK: - Events to the web

    private func emitQueue(_ k: Item.Kind) {
        let q = queue(k)
        onEvent?(.queue(kind: k, items: q.items, index: q.index, source: q.source))
    }

    /// Both kinds' states, the active one last (the web mirrors each kind from its latest state).
    private func emitStates() {
        emitState(active == .track ? .episode : .track)
        emitState(active)
    }

    private func emitState(_ k: Item.Kind) {
        let q = queue(k)
        let isActive = k == active, isLoaded = loaded?.kind == k
        let duration = (isLoaded ? backend.durationMs : nil) ?? q.current?.durationMs ?? 0
        onEvent?(.state(StateEvent(kind: k, itemId: q.current?.id, index: q.index, playing: isActive && playing,
                                   positionMs: position(k), durationMs: duration, buffering: isActive && buffering,
                                   error: isActive ? error : nil, rate: k == .episode ? rate : 1,
                                   shuffle: k == .track && modes.shuffle, repeatMode: k == .track ? modes.repeatMode : .off,
                                   sleepRemainingMs: sleepRemainingMs)))
        if isActive { refreshNowPlaying() }
    }

    // MARK: - Now Playing and car lyrics

    /// Writes Now Playing when what it shows changed: the item, playing or not (buffering counts as not, so the
    /// lock screen's clock does not run ahead), the rate, the duration or the car-lyrics line; and after a seek.
    /// Ticks only get here, so a line change is one write, not one every half second.
    private func refreshNowPlaying() {
        guard let nowPlaying else { return }
        let force = forceNowPlaying
        forceNowPlaying = false
        guard let item = queue(active).current else {
            if nowPlayingKey != nil { nowPlayingKey = nil; nowPlaying.clear() }
            return
        }
        let pos = position(active)
        let line = active == .track && prefs.carLyrics && lyricsID == item.trackID ? carLyrics?.line(atMs: pos) : nil
        let r = active == .episode ? rate : 1
        let duration = loaded?.kind == active ? backend.durationMs : nil
        let key = NowPlayingKey(kind: item.kind, id: item.id, title: item.title, artist: item.artist, album: item.album, playing: playing && !buffering, rate: r, durationMs: duration, line: line)
        guard force || key != nowPlayingKey else { return }
        nowPlayingKey = key
        nowPlaying.show(item: item, positionMs: pos, durationMs: duration, rate: r, playing: key.playing, lyricLine: line)
    }

    /// The car-lyrics lines for a track that is being loaded (once per track: a retry keeps them). An answer,
    /// found or not, is kept; a fetch that threw (offline) leaves the song title and is asked again while the
    /// track plays, after 30 s, then 60 s, … up to 10 minutes (`retryLyricsIfDue`, on ticks).
    private func fetchLyrics(_ id: Int) {
        guard nowPlaying != nil, prefs.carLyrics else { lyricsID = nil; carLyrics = nil; lyricsRetry = nil; return }
        guard lyricsID != id else { return }
        lyricsID = id; carLyrics = nil; lyricsRetry = nil
        requestLyrics(id)
    }

    private func requestLyrics(_ id: Int) {
        let e = epoch
        lyricsInFlight = id
        tasks.run { [weak self, api] in
            let result: Result<LyricsDoc, Error>
            do { result = .success(try await api.lyrics(id)) } catch { result = .failure(error) }
            guard let self else { return }
            if self.lyricsInFlight == id { self.lyricsInFlight = nil }
            guard self.epoch == e, self.lyricsID == id else { return }
            switch result {
            case .success(let doc):
                self.carLyrics = CarLyrics(doc)
                self.lyricsRetry = nil
            case .failure:
                let wait = self.lyricsRetry?.nextWait ?? Self.lyricsRetryS
                self.lyricsRetry = (self.now().addingTimeInterval(wait), min(wait * 2, Self.lyricsRetryMaxS))
            }
            self.refreshNowPlaying()
        }
    }

    private func retryLyricsIfDue() {
        guard let r = lyricsRetry, let id = lyricsID, lyricsInFlight == nil, now() >= r.at,
              loaded?.kind == .track, music.current?.trackID == id, prefs.carLyrics, nowPlaying != nil else { return }
        requestLyrics(id)
    }
}
