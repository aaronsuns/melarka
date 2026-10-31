package lark.player

import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive
import lark.api.ApiTrack
import lark.api.LarkApiProtocol
import lark.bridge.Item
import lark.bridge.Item.Kind
import lark.bridge.NativeEvent
import lark.bridge.NativePrefs
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.SleepRequest
import lark.bridge.WebMessage
import lark.bridge.get
import lark.bridge.setting
import lark.cache.CacheProviding
import lark.core.Cancellable
import lark.core.Clock
import lark.core.NetworkStatus
import lark.core.Scheduler
import lark.core.TaskBag
import java.lang.ref.WeakReference
import java.time.Duration
import java.time.Instant
import kotlin.math.roundToLong

/** Why an intent (a shortcut, the car's autoplay) cannot start anything; iOS `LarkIntentError`'s engine cases. */
enum class IntentBlocker { notSignedIn, offlineNothingCached }

/**
 * The native player, a port of `ios/Sources/Player/PlayerEngine.swift`: one [MediaBackend], two queues (music and
 * 频道 episodes) and one active kind. Everything here runs on the main thread; async work runs in `scope` (the
 * app's main-thread scope) and timers on `scheduler`.
 *
 * - Never-stop (music): a failed item is skipped (and passed over for 30 minutes); a network error retries the
 *   same item at the same position after 2, 5 and 10 s first. Offline, or after 3 failures in a row, only items
 *   on the phone count, then cached favorites ([NextChooser], the web's `nextTrack.ts`). A remote track
 *   that buffers for 6 s gives way to one on the phone (the web's stall switch), and a player stopped for
 *   the network starts again when it comes back. An episode that cannot play falls back to music.
 * - Backend callbacks carry the generation of their load; a callback about a replaced item is dropped.
 * - Radio, shuffle and favorites queues refill themselves when 2 or fewer items are left (the web's refill).
 * - Shuffle and repeat ([modes], music only, the web's `modes.ts`): repeat off changes nothing above; repeat all
 *   loops the queue with no refill (a new, reshuffled pass when shuffle is on); repeat one replays a track that
 *   ended on its own, gaplessly. A cached favorite that stands in for a track under repeat all plays once and
 *   leaves the queue again.
 * - The sleep timer ([sleepTimer], music and episodes) runs here, on `scheduler`, so it keeps time with the
 *   screen off: after n minutes the volume fades to 0 over 10 s, playback pauses and the volume is back for
 *   the next play. "End of this track" fades over the item's last 10 s and stops at its end (music: the next
 *   track loaded, paused; an episode: stays on the finished one, as the web player does).
 * - Loudness: each music item loads with its own gain ([Gain], the `loudness` pref), the preloaded one too, so
 *   a gapless change keeps the right level. The master volume belongs to the sleep timer's fade.
 * - Both queues are saved per user on every change and every 10 s while playing ([QueueStore]); the music
 *   queue goes to `PUT /queue` ([QueueSync]). Music creates play events ([PlayEventQueue]); episodes send
 *   `PUT /episodes/{id}/progress` instead.
 * - The web drives it through [handle]; every change goes back as [onEvent] (`queue`, `state`, `notice`,
 *   `flushed`). No event carries the token.
 */
class PlayerEngine(
    private val backend: MediaBackend,
    private val api: LarkApiProtocol,
    private val cache: CacheProviding,
    private val store: QueueStore,
    val events: PlayEventQueue,
    private val network: NetworkStatus,
    private val scheduler: Scheduler,
    private val clock: Clock,
    private val random: () -> Double,
    private val nowPlaying: NowPlayingSink?,
    scope: CoroutineScope,
    private val texts: EngineTexts = EngineTexts.english,
) : BackendDelegate {
    /** The API this engine talks to (a server change builds a new engine; tests check which server). */
    val client: LarkApiProtocol get() = api
    private val sync = QueueSync(api, scheduler, clock, scope)
    private val tasks = TaskBag(scope)

    var music = PlaybackQueue.empty
        private set
    var episodes = PlaybackQueue.empty
        private set
    var active = Kind.track
        private set(value) {
            val old = field
            field = value
            if (value != old) onActiveChange?.invoke(value)
        }

    /** Playing, or meant to be (loading, buffering, waiting for a network retry). */
    var playing = false
        private set

    /**
     * Paused by the system (a focus loss, or the player on its own), not by the user: the end of an interruption
     * may resume it. Any pause, play or load by the user clears it.
     */
    var pausedBySystem = false
        private set

    var prefs = NativePrefs(quality = "high", carLyrics = true)
        set(value) {
            val old = field
            field = value
            if (value.loudness != old.loudness) loudnessChanged()
            if (value.carLyrics == old.carLyrics) return
            lyricsId = null; carLyrics = null; lyricsRetry = null
            if (loaded?.kind == Kind.track) music.current?.trackId?.let { fetchLyrics(it) }
            refreshNowPlaying()
        }

    /** The episode playback rate; music always plays at 1. */
    var rate: Double = 1.0
        set(value) {
            val old = field
            field = value
            if (value == old) return
            if (loaded?.kind == Kind.episode) backend.setRate(value.toFloat())
            emitState(Kind.episode)
        }

    /** Shuffle and repeat for the music queue (episodes ignore them), saved with the queues. */
    var modes = PlayModes()
        private set(value) {
            val old = field
            field = value
            if (value != old) onModesChange?.invoke(value)
        }

    /** The modes changed (the media session: the lock screen's and the car's repeat and shuffle state). */
    var onModesChange: ((PlayModes) -> Unit)? = null

    /**
     * Repeat all: cached favorites put in for a track that could not play, and the interrupted track's id
     * (requeued after it). Dropped when it ends or is skipped, and before a new pass (the web's `substitutes`).
     */
    private class Substitute(val id: String, var requeue: String?)
    private var substitutes = mutableListOf<Substitute>()
    /** The sleep timer: a deadline (its fade starts 10 s before), or the end of the item playing. */
    private var sleep: Sleep? = null
    /** The fade's start, then its steps. */
    private var sleepJobs: List<Cancellable> = emptyList()
    /** The minutes timer's fade is running. */
    private var sleepFading = false
    /** The master volume the fade set last (1: no fade). */
    private var fadeLevel = 1f
    var onEvent: ((NativeEvent) -> Unit)? = null

    /** The active kind changed (the media session: skip buttons for episodes). */
    var onActiveChange: ((Kind) -> Unit)? = null

    /**
     * Called synchronously right before sound starts: the media service is brought up there (`PlaybackHost`), so it
     * is in the foreground from the first second. The player itself takes audio focus as it starts.
     */
    var willStartPlayback: (() -> Unit)? = null

    /**
     * Asked before the engine starts playback on its own (the network came back for a player stopped offline). False
     * (the app is in the background and its media service is not running, which Android would refuse to start) leaves
     * the player paused by the system until [resumeDeferred] (the user is back) or a play. Null: always allowed.
     */
    var canAutoStart: (() -> Boolean)? = null

    /** A resume [canAutoStart] refused, waiting for [resumeDeferred]. */
    private var deferredResume = false

    /** Dropped by the app services (another server): the network observer, which can't be removed, does nothing more. */
    private var detached = false

    /**
     * What was handed to the backend as the next item: loading that item again uses this same source, so the
     * backend takes the preloaded item over (gapless) even when the track reached the cache meanwhile.
     */
    private data class Preloaded(val kind: Kind, val id: String, val source: MediaSource)
    private var preloadedNext: Preloaded? = null

    /** What the now-playing sink shows now: it is written only when this changes (a car-lyrics line, play, pause), or on a seek. */
    private data class NowPlayingKey(val kind: Kind, val id: String, val title: String, val artist: String, val album: String,
                                     val playing: Boolean, val rate: Double, val durationMs: Long?, val line: String?)
    private var nowPlayingKey: NowPlayingKey? = null
    private var forceNowPlaying = false
    /** The current track's lyrics for the car display (`prefs.carLyrics`), and the track they are for. */
    private var carLyrics: CarLyrics? = null
    private var lyricsId: Long? = null
    /** The lyrics fetch for [lyricsId] threw: when to ask again, and the wait after the next failure. */
    private data class LyricsRetry(val at: Instant, val nextWait: Double)
    private var lyricsRetry: LyricsRetry? = null
    private var lyricsInFlight: Long? = null
    /** Counts lyrics requests, so only the latest answer is shown. */
    private var lyricsRequest = 0

    /** What the backend holds: always the current item of `kind`'s queue. */
    private data class Loaded(val kind: Kind, val index: Int, val id: String)
    /** Bumped by every backend load (a retry too); callbacks with another generation are about a replaced item. */
    private var loadGeneration = 0
    /** The last position the engine knows for the loaded item (load, ticks, seeks): a failed item can read 0. */
    private var knownMs = 0L
    private var stallTimer: Cancellable? = null
    /** Stopped because the network was gone: starts again when it comes back. */
    private var stoppedOffline = false

    /**
     * What the network coming back does for a player [stoppedOffline]: play the interrupted current item again
     * (`replay`), go on to the next one (a track that ended with only streams after it), or ask the server for
     * favorites (a cold start with nothing on the phone).
     */
    private enum class OfflineResume { replay, advance, shuffle }
    private var offlineResume = OfflineResume.replay
    private var loaded: Loaded? = null
    private var buffering = false
    private var error: String? = null

    /** The listen in progress, counted like the web's `PlayerProvider` (`finishListen`). */
    private class Listen(val trackId: Long, val startedAt: Long, var seconds: Double, var lastMs: Long)
    private var listen: Listen? = null
    private enum class ListenEnd { ended, skip, switched }

    private val failedAt = HashMap<String, Instant>()
    private var consecutiveFailures = 0
    private class NetRetry(val attempt: Int, val positionMs: Long, var timer: Cancellable?)
    private var netRetry: NetRetry? = null
    private var lastProgress: Instant? = null

    private var refilling = false
    /** Counts refills, so a cancelled refill's late end touches only its own [refilling]. */
    private var refillCount = 0
    private var refillExhausted = false
    private var refillTimer: Cancellable? = null
    /** A refill was due while offline. */
    private var refillWanted = false
    /** Bumped whenever the music queue is replaced (or wiped): a refill for an older queue is dropped. */
    private var musicGeneration = 0
    /** Bumped by [reset]: async work for a signed-out user is dropped. */
    private var epoch = 0

    private var savedAt = 0L
    private var ownDevice: String? = null
    private var greeted = false

    init {
        backend.delegate = this
        sync.snapshot = {
            val ids = music.items.mapNotNull { it.trackId }
            QueueSync.Snapshot(ids, if (ids.isEmpty()) 0 else minOf(music.index, ids.size - 1), maxOf(0L, position(Kind.track)))
        }
        // A queue holding YouTube tracks (a playlist played from search) is not sent: the server keeps the last
        // library queue, which the web and the other devices restore.
        sync.online = { network.isOnline && music.items.none { it.youtubeVideoId != null } }
        events.online = { network.isOnline }
        sync.onOwnDevice = { device ->
            if (ownDevice != device) {
                ownDevice = device
                persist(touch = false)
            }
        }
        // Weakly, as iOS: an engine replaced by a server change must not keep reacting to the network.
        val self = WeakReference(this)
        network.observe { self.get()?.networkChanged() }
        restore()
    }

    // MARK: - Web messages

    /** Routes a bridge message. `auth`, `openSettings` and `favoriteChanged` belong to the app services. */
    fun handle(m: WebMessage) {
        when (m) {
            is WebMessage.Hello -> hello(m.onOpen)
            is WebMessage.SetQueue -> setQueue(m)
            is WebMessage.Play -> play(m.kind)
            is WebMessage.Pause -> if (m.kind == null || m.kind == active) pause()
            is WebMessage.Next -> if (m.kind == active) next() else step(m.kind, 1)
            is WebMessage.Prev -> if (m.kind == active) prev() else step(m.kind, -1)
            is WebMessage.Seek -> if (m.kind == active) seek(m.ms) else setPosition(m.kind, m.ms)
            is WebMessage.Skip -> if (m.kind == active) skip(m.ms) else setPosition(m.kind, queue(m.kind).positionMs + m.ms)
            is WebMessage.SetRate -> rate = m.rate
            is WebMessage.Stop -> stop(m.kind)
            is WebMessage.SetPrefs -> prefs = m.prefs
            is WebMessage.SetModes -> setModes(m.shuffle, m.repeat)
            is WebMessage.SleepTimer -> sleepTimer(m.request)
            WebMessage.PauseForWeb -> pause()
            is WebMessage.FlushEvents -> flushEvents(m.id)
            is WebMessage.LyricsChanged -> if (lyricsId == m.trackId) requestLyrics(m.trackId)
            is WebMessage.Auth, is WebMessage.FavoriteChanged, WebMessage.OpenSettings -> {}
        }
    }

    private fun hello(onOpen: String) {
        emitAll()
        if (greeted) return     // only the first hello after a launch; a web reload just gets the state
        greeted = true
        when (onOpen) {
            "resume" -> tasks.run { adoptServerQueue() }
            "shuffle_favorites" -> if (!playing) tasks.run { shuffleFavorites() }
        }
    }

    private fun setQueue(sq: WebMessage.SetQueue) {
        val k = sq.kind
        if (sq.items.isEmpty()) return stop(k)
        val idx = sq.index.coerceIn(0, sq.items.size - 1)
        val item = sq.items[idx]

        // An edit (enqueue, remove, reorder, update): the current item stays, and so does playback.
        if (sq.positionMs == null && queue(k).current?.id == item.id) {
            if (k == Kind.track) editedMusic(sq.items, idx)
            modify(k) { it.copy(items = sq.items, index = idx, source = sq.source) }
            if (loaded?.kind == k) loaded = loaded?.copy(index = idx)
            emitQueue(k); emitState(k); persist()
            if (k == Kind.track) { sync.edited(); maybeRefill(); prefetchUpcoming() }
            return
        }

        val pos = maxOf(0L, sq.positionMs ?: 0L)
        var new = PlaybackQueue(sq.items, idx, pos, sq.source)
        if (k == Kind.track) {
            musicGeneration += 1; refillExhausted = false; refillWanted = false; consecutiveFailures = 0
            refillTimer?.cancel(); refillTimer = null
            substitutes = mutableListOf()
            new = shuffledIfOn(new)
        }
        if (k != active && !sq.play) {
            // The other kind, not to be played now: stored for later.
            modify(k) { new }
            emitQueue(k); emitState(k); persist()
            if (k == Kind.track) { sync.edited(); maybeRefill() }
            return
        }
        val switched = k != active
        if (switched) { leaveActive(); active = k } else saveOutgoingEpisode()
        modify(k) { new }
        load(k, idx, pos, sq.play)
        emitQueue(k)
        // The web mirrors each kind from its own state events: tell it the outgoing kind stopped (first, so
        // the new kind's state stays the latest).
        if (switched) emitState(if (k == Kind.track) Kind.episode else Kind.track)
        emitState(k); persist()
        if (k == Kind.track) { sync.edited(); maybeRefill() }
    }

    private fun play(k: Kind) {
        if (k == active) return play()
        if (queue(k).current == null) return
        leaveActive()
        active = k
        val q = queue(k)
        load(k, q.index, q.positionMs, autoplay = true)
        emitState(Kind.track); emitState(Kind.episode); persist()
    }

    private fun stop(k: Kind) {
        if (loaded?.kind == k) {
            syncPosition()
            if (k == Kind.episode) saveOutgoingEpisode() else finishListen(ListenEnd.switched)
            cancelRetry()
            backend.stop()
            loaded = null
            preloadedNext = null
        }
        modify(k) { PlaybackQueue.empty }
        if (k == Kind.track) musicGeneration += 1
        if (k == active) { playing = false; buffering = false; error = null }
        if (k == Kind.episode) active = Kind.track
        emitQueue(k); emitState(Kind.track); emitState(Kind.episode); persist()
        if (k == Kind.track) sync.edited()
    }

    /**
     * `flushEvents {id}`: the listen in progress is recorded and ended (the web's `finishListen("switch")`, no
     * restart, so one play is never two events; the web sends it only on logout), everything pending is
     * posted, then `flushed {id}` is sent whether the POST worked or not (the web waits at most 3 s anyway).
     */
    private fun flushEvents(id: String) {
        finishListen(ListenEnd.switched)
        tasks.run {
            events.flush()
            onEvent?.invoke(NativeEvent.Flushed(id))
        }
    }

    // MARK: - Transport (on the active kind)

    fun play() {
        val q = queue(active)
        if (q.current == null) return
        catchUpSleep(playing = false)
        pausedBySystem = false
        if (loaded?.kind == active) {
            willStartPlayback?.invoke()
            backend.play()
            playing = true
        } else {
            load(active, q.index, q.positionMs, autoplay = true)
        }
        emitState(active)
    }

    fun pause() {
        pausedBySystem = false
        syncPosition()                                       // a waiting retry's position, not the failed item's
        disarmStall()
        stoppedOffline = false
        if (loaded != null) backend.pause()
        if (netRetry != null) { cancelRetry(); loaded = null }    // a retry waiting: play() starts afresh
        val was = playing
        playing = false; buffering = false
        if (active == Kind.episode) saveOutgoingEpisode() else if (was) sync.edited()
        persist(); emitState(active)
    }

    fun toggle() = if (playing) pause() else play()

    /** An interruption began: paused, and marked so its end may resume it. */
    fun systemPause() {
        if (!playing) return
        pause()
        pausedBySystem = true
    }

    /** The remote play's cold start: playback is prepared now ([willStartPlayback]), before the async shuffle. */
    fun prepareForPlayback() { willStartPlayback?.invoke() }

    /**
     * A cold start before the network monitor's first answer, with nothing on the phone ([resumeFromDisk]): it plays
     * once the network is known. The media service keeps its cold-start notification meanwhile.
     */
    val awaitingNetworkAnswer: Boolean get() = stoppedOffline && !network.answered

    /** The backend holds an item (playing or paused); false after a launch until something is played. */
    val isLoaded: Boolean get() = loaded != null

    /** The active queue's current item. */
    val current: Item? get() = queue(active).current

    /** Where the active item is now (the player's position while it is loaded): the media session's position. */
    val activePositionMs: Long get() = position(active)

    fun next() {
        cancelRetry()
        if (active == Kind.episode) {
            if (episodes.index + 1 >= episodes.items.size) return
            saveOutgoingEpisode()
            val i = episodes.index + 1
            load(Kind.episode, i, resumeAt(episodes.items[i]), autoplay = true)
            emitQueue(Kind.episode); emitState(Kind.episode); persist()
        } else {
            finishListen(ListenEnd.skip)
            dropSubstitutes()
            advanceMusic(user = true)
        }
    }

    fun prev() {
        val q = queue(active)
        if (position(active) > RESTART_PREV_MS || q.index == 0) return seek(0)
        cancelRetry()
        val i = q.index - 1
        if (active == Kind.episode) {
            saveOutgoingEpisode()
            load(Kind.episode, i, resumeAt(q.items[i]), autoplay = true)
        } else {
            finishListen(ListenEnd.skip)
            load(Kind.track, i, 0, autoplay = true)
            sync.edited()
        }
        emitQueue(active); emitState(active); persist()
    }

    fun seek(ms: Long) {
        val target = maxOf(0L, ms)
        if (loaded?.kind != active) return setPosition(active, target)
        disarmStall()
        backend.seek(target)
        listen?.lastMs = target
        knownMs = target
        forceNowPlaying = true
        emitState(active)
    }

    fun skip(ms: Long) {
        var target = position(active) + ms
        val d = backend.durationMs ?: queue(active).current?.durationMs
        if (d != null && d > 0) target = minOf(target, d)
        seek(target)
    }

    /**
     * One step of a held fast-forward or rewind ([lark.service.RemoteCommands] scanning): like [skip], but forward stops
     * [SCAN_END_MARGIN_MS] before the end, so the item then finishes and the next starts as usual, and back stops at 0.
     * False when it reached that edge: the scan ends there.
     */
    fun scanStep(ms: Long): Boolean {
        val wanted = position(active) + ms
        var target = maxOf(0L, wanted)
        val d = backend.durationMs ?: queue(active).current?.durationMs
        if (ms > 0 && d != null && d > 0) target = minOf(target, maxOf(0L, d - SCAN_END_MARGIN_MS))
        seek(target)
        return target == wanted
    }

    /**
     * The car's play with nothing loaded, and the Resume shortcut: the saved queue from disk (the current item
     * from the cache when it is there, before any network call); with no music queue, shuffled favorites.
     */
    suspend fun resumeOrShuffleFavorites() {
        if (resumeFromDisk()) return
        shuffleFavorites()
    }

    /**
     * The part of [resumeOrShuffleFavorites] that needs no network and no await: the saved queue, else cached
     * favorites. True when it started playback; false when only the server's favorites are left to try.
     * The car's play command runs this inside its handler, so sound starts at once.
     */
    fun resumeFromDisk(): Boolean {
        // Episodes always stream: with no network the car gets music (from the cache) instead of silence.
        if (active == Kind.episode && network.isOnline && episodes.current != null) { play(); return true }
        if (music.current != null) {
            playLocalInsteadOfStream()?.let { return it }
            if (active == Kind.track) play() else play(Kind.track)
            return true
        }
        if (startCachedFavorites() || offlineWithNothingCached()) return true
        if (!network.answered) {
            // A cold start before the network monitor's first answer, with nothing on the phone: no audio focus
            // is taken (the car's own source keeps playing) and the server is asked once the network is known.
            stoppedOffline = true; offlineResume = OfflineResume.shuffle
            return true
        }
        return false
    }

    /**
     * Offline, or before the network monitor's first answer (a cold launch), a saved current track that is not
     * on the phone is not tried as a stream first: the next cached item in the queue plays, else a cached
     * favorite, and the streamed track is queued right after it for later. Null: play the current item as usual
     * (it is cached, the network is known to be up, or nothing local exists and the network may be up).
     */
    private fun playLocalInsteadOfStream(): Boolean? {
        val cur = music.current ?: return null
        if ((network.isOnline && network.answered) || isLocal(cur)) return null
        val choice = chooseNext(mustBeLocal = true)
        if (choice == NextChooser.Choice.None) return if (network.isOnline) null else offlineWithNothingCached()
        if (active != Kind.track) { leaveActive(); active = Kind.track; emitState(Kind.episode) }
        if (choice is NextChooser.Choice.Insert && !network.isOnline) onEvent?.invoke(NativeEvent.Notice(texts.offlineNotice))
        apply(choice, requeue = cur)
        afterMusicMove()
        return true
    }

    /**
     * The intents ask before they take audio focus: why nothing can start (`resume`: the saved queue counts
     * too), or null when something may play. Signed out there is no token and no cache; offline, only what is
     * on the phone can play.
     */
    fun intentBlocker(resume: Boolean): IntentBlocker? {
        if (api.token == null) return IntentBlocker.notSignedIn
        if (network.isOnline) return null
        if (cache.cachedFavorites().isNotEmpty()) return null
        if (resume && music.items.any { isLocal(it) }) return null
        return IntentBlocker.offlineNothingCached
    }

    /** Why the last attempt to play did not start (the state's `error`), for the intents' failure text. */
    val failureText: String? get() = error

    /**
     * The Shuffle Favorites shortcut, `onOpen: shuffle_favorites`: a shuffled favorites queue that refills itself.
     * Cached favorites first (they start at once, with or without a network); with none, the server's
     * random favorites; with no favorites at all, a shuffle of the whole library, and a notice says so.
     */
    suspend fun shuffleFavorites() {
        if (startCachedFavorites() || offlineWithNothingCached()) return
        val e = epoch
        val items: List<Item>
        var source = QueueSource.favorites
        var notice: String? = null
        try {
            val r = api.randomFavorites(SHUFFLE_SIZE, emptyList())
            if (r.source == "favorites" && r.tracks.isNotEmpty()) {
                items = r.tracks.map { it.toItem() }
            } else {
                val ts = r.tracks.ifEmpty { api.randomTracks(SHUFFLE_SIZE, emptyList()) }
                items = ts.map { it.toItem() }
                source = QueueSource.shuffle
                notice = texts.noFavoritesNotice
            }
        } catch (ex: CancellationException) {
            throw ex
        } catch (_: Exception) {
            Log.i(TAG, "shuffle favorites: the server could not be reached")
            if (epoch == e) shuffleFailed(texts.shuffleFailedNotice)
            return
        }
        if (epoch != e) return
        if (items.isEmpty()) return shuffleFailed(texts.shuffleFailedNotice)
        setQueue(WebMessage.SetQueue(Kind.track, items, 0, 0, play = true, source = source))
        notice?.let { onEvent?.invoke(NativeEvent.Notice(it)) }
    }

    /** Cached favorites, shuffled, playing now (no await). False when none are cached. */
    private fun startCachedFavorites(): Boolean {
        val cached = cache.cachedFavorites().map { it.toItem() }
        if (cached.isEmpty()) return false
        val items = NextChooser.shuffled(cached, random).take(SHUFFLE_SIZE)
        setQueue(WebMessage.SetQueue(Kind.track, items, 0, 0, play = true, source = QueueSource.favorites))
        if (!network.isOnline) onEvent?.invoke(NativeEvent.Notice(texts.offlineNotice))
        return true
    }

    /** Offline with no cached favorite: nothing can play, and the state and a notice say why. True when offline. */
    private fun offlineWithNothingCached(): Boolean {
        if (network.isOnline) return false
        shuffleFailed(texts.nothingCachedNotice)
        stoppedOffline = true; offlineResume = OfflineResume.shuffle      // the server's favorites once the network is back
        return true
    }

    /** Nothing to shuffle: the state carries why (now playing, the page) and a notice says it. */
    private fun shuffleFailed(text: String) {
        if (!playing && active == Kind.track) error = text
        emitState(Kind.track)
        onEvent?.invoke(NativeEvent.Notice(text))
    }

    /** Every 10 s (a timer in the app services): saves the position while playing, sends what is due. */
    fun tick() {
        if (playing && loaded?.kind == active) {
            persist()
            periodic()
        }
        events.flushIfDue()
    }

    /** Sign-out or a user switch: stop, forget both queues and every pending event, on disk too. */
    fun reset() {
        epoch += 1; musicGeneration += 1
        tasks.cancelAll(); sync.cancel()
        refillTimer?.cancel(); refillTimer = null; refillWanted = false
        cancelRetry(); disarmStall(); stoppedOffline = false; deferredResume = false
        clearSleep()
        backend.stop()
        loaded = null; listen = null; playing = false; buffering = false; error = null; pausedBySystem = false
        lyricsId = null; preloadedNext = null
        carLyrics = null; lyricsRetry = null; lyricsInFlight = null
        music = PlaybackQueue.empty; episodes = PlaybackQueue.empty; active = Kind.track
        failedAt.clear(); consecutiveFailures = 0; refilling = false; refillExhausted = false
        modes = PlayModes(); substitutes = mutableListOf()
        savedAt = 0; ownDevice = null; lastProgress = null
        greeted = false               // the next user's page gets its `hello{onOpen}` honoured
        events.clear()
        store.wipe()
        emitAll()
    }

    /** Reads the current user's saved queues (a launch, or after `QueueStore.user` changed). Paused; nothing loads. */
    fun restore() {
        val s = store.load()
        music = s?.music ?: PlaybackQueue.empty
        episodes = s?.episodes ?: PlaybackQueue.empty
        active = s?.active ?: Kind.track
        savedAt = s?.savedAt ?: 0L
        ownDevice = s?.ownDevice
        modes = s?.modes ?: PlayModes()        // a file from before the modes: both off
        substitutes = mutableListOf()
        loaded = null; listen = null; playing = false; buffering = false; error = null
    }

    /**
     * Sign-out, while the token still works: playback stops, the listen in progress is recorded and every
     * pending event gets one POST.
     */
    suspend fun flushBeforeSignOut() {
        syncPosition()
        if (loaded != null) backend.pause()
        playing = false
        finishListen(ListenEnd.switched)
        events.flush()
    }

    /** Re-sends both queues and states (a web reload, or the Activity coming back: the page missed events). */
    fun emitAll() {
        emitQueue(Kind.track); emitQueue(Kind.episode); emitState(Kind.track); emitState(Kind.episode)
    }

    // MARK: - Shuffle and repeat

    /**
     * `setModes`, the lock screen and the car. Shuffle reorders only what follows the current item, which plays
     * on: nothing is loaded. Music only; episodes keep their own order.
     */
    fun setModes(shuffle: Boolean, repeatMode: RepeatMode) {
        var m = modes
        val reorder = shuffle != m.shuffle
        if (reorder) {
            if (shuffle) {
                val (q, original) = PlayModes.shuffleUpcoming(music, random)
                music = q; m = m.copy(original = original)
            } else {
                music = PlayModes.unshuffle(music, m.original ?: emptyList())
                m = m.copy(original = null)
            }
            m = m.copy(shuffle = shuffle)
        }
        m = m.copy(repeatMode = repeatMode)
        if (m == modes) return emitState(Kind.track)
        modes = m
        persist()
        if (reorder) emitQueue(Kind.track)
        emitState(Kind.track)
        if (reorder) sync.edited()
        prefetchUpcoming()
        if (loaded?.kind == Kind.track) preloadNext(Kind.track)
        maybeRefill()                         // repeat all turned off at the end: never-stop again
    }

    /**
     * Shuffle on: a new list plays shuffled after its chosen item (the web's `playList`); the same queue again
     * (a jump) is not reshuffled.
     */
    private fun shuffledIfOn(q: PlaybackQueue): PlaybackQueue {
        if (!modes.shuffle || q.items.map { it.id } == music.items.map { it.id }) return q
        val (s, original) = PlayModes.shuffleUpcoming(q, random)
        modes = modes.copy(original = original)
        return s
    }

    /**
     * A user's edit of the music queue (it is about to replace [music]): the pre-shuffle order follows it, and
     * a substitute's requeued copy taken out keeps the interrupted entry in the loop.
     */
    private fun editedMusic(items: List<Item>, index: Int) {
        val new = PlaybackQueue(items, index, 0, music.source)
        val o = modes.original
        if (modes.shuffle && o != null) modes = modes.copy(original = PlayModes.edited(o, music, new))
        if (substitutes.isEmpty() || items.size + 1 != music.items.size) return
        val old = music.items
        val r = items.indices.firstOrNull { old[it].id != items[it].id } ?: items.size
        if ((old.subList(0, r) + old.subList(r + 1, old.size)).map { it.id } != items.map { it.id }) return   // one entry taken out
        for (sub in substitutes) {
            if (sub.requeue != old[r].id) continue
            val i = locate(sub.id, old, music.index)
            if (i != null && r > i) sub.requeue = null
        }
    }

    /** Where a substitute is: its id at or before the current item (it was played from there), else after it. */
    private fun locate(id: String, items: List<Item>, index: Int): Int? {
        if (items.isEmpty()) return null
        return (minOf(index, items.size - 1) downTo 0).firstOrNull { items[it].id == id }
            ?: items.indexOfFirst { it.id == id }.takeIf { it >= 0 }
    }

    /**
     * Repeat all: each substitute leaves the queue, with the interrupted item's old entry (its requeued copy
     * stays, to be tried again); with nothing before it left to stand on, the old entry stays and the copy
     * goes. Any other mode: forgotten, nothing removed.
     */
    private fun dropSubstitutes() {
        val subs = substitutes
        substitutes = mutableListOf()
        if (subs.isEmpty() || modes.repeatMode != RepeatMode.all) return
        var items = music.items
        var index = music.index
        for (sub in subs) {
            val i = locate(sub.id, items, index) ?: continue
            val req = sub.requeue
            val k = req?.let { r -> (0 until i).lastOrNull { items[it].id == r } }
            val copy = req?.let { r -> (i + 1 until items.size).firstOrNull { items[it].id == r } }
            fun indexWithout(d: Set<Int>) = index - d.count { it < index } - (if (index in d) 1 else 0)
            var drop: Set<Int> = if (k != null) setOf(i, k) else setOf(i)
            if (indexWithout(drop) < 0 && copy != null) drop = setOf(i, copy)
            val ni = maxOf(0, indexWithout(drop))
            items = items.filterIndexed { n, _ -> n !in drop }
            index = minOf(ni, maxOf(0, items.size - 1))
        }
        music = music.copy(items = items, index = index)
        if (loaded?.kind == Kind.track) loaded = loaded?.copy(index = index)
    }

    // MARK: - Sleep timer

    /** `sleepTimer`: one timer at a time, so a new choice replaces the one set (and undoes its fade). */
    fun sleepTimer(r: SleepRequest) {
        clearSleep(preloadAgain = r != SleepRequest.EndOfTrack)
        when (r) {
            SleepRequest.Cancel -> {}
            is SleepRequest.Minutes -> {
                val s = r.minutes * 60.0
                sleep = Sleep.At(clock.now().plusMillis((s * 1000).toLong()))
                sleepJobs = listOf(scheduler.after(maxOf(0.0, s - SLEEP_FADE_S)) { startSleepFade() })
            }
            SleepRequest.EndOfTrack -> {
                sleep = Sleep.EndOfTrack
                loaded?.let { preloadNext(it.kind) }           // nothing next: the player stops at the end
            }
        }
        emitStates()
    }

    /** The minutes timer's last 10 s: the volume steps down to 0, then playback pauses. */
    private fun startSleepFade() {
        if (sleepFading) return
        sleepJobs.forEach { it.cancel() }
        sleepFading = true
        val steps = SleepTimer.fadeSteps()
        sleepJobs = steps.mapIndexed { n, (delay, level) ->
            scheduler.after(delay) {
                setFade(level)
                if (n == steps.size - 1) sleepEnded()
            }
        }
    }

    /** The deadline: paused (it stays paused if it already was), the volume back for the next play. */
    private fun sleepEnded() {
        sleepJobs = emptyList(); sleepFading = false
        pause()
        setFade(1f)
        sleep = null
        emitStates()
    }

    /** No timer: its jobs cancelled, the volume back, and the next item preloaded again after an end-of-track one. */
    private fun clearSleep(preloadAgain: Boolean = true) {
        val wasEndOfTrack = sleep == Sleep.EndOfTrack
        sleepJobs.forEach { it.cancel() }
        sleepJobs = emptyList(); sleepFading = false
        sleep = null
        if (fadeLevel != 1f) setFade(1f)
        if (wasEndOfTrack && preloadAgain) loaded?.let { preloadNext(it.kind) }
    }

    /**
     * The scheduler's clock may stop while the phone sleeps (paused, screen off), so its job can come late; the
     * deadline is wall time. Past the fade's start, the fade starts now. Past the deadline itself before a play,
     * the timer is over: it would have paused a player that was paused anyway, and the play goes on.
     */
    private fun catchUpSleep(playing: Boolean) {
        val s = sleep as? Sleep.At ?: return
        if (sleepFading) return
        val t = clock.now()
        if (!t.isBefore(s.deadline) && !playing) {
            clearSleep()
            emitStates()
        } else if (!t.isBefore(s.deadline.minusMillis((SLEEP_FADE_S * 1000).toLong()))) {
            startSleepFade()
        }
    }

    private fun setFade(level: Float) {
        fadeLevel = level
        backend.setMasterVolume(level)
    }

    /** "End of this track": the volume follows what is left of the item in its last 10 s. */
    private fun fadeTowardsTheEnd(positionMs: Long) {
        if (sleep != Sleep.EndOfTrack) return
        val d = backend.durationMs ?: current?.durationMs ?: return
        if (d <= 0) return
        val level = SleepTimer.endOfTrackLevel(d, positionMs)
        if (level != fadeLevel) setFade(level)
    }

    /** What the state says is left: to the deadline, or of the item playing; null with no timer. */
    private fun sleepRemainingMs(): Long? = when (val s = sleep) {
        null -> null
        is Sleep.At -> SleepTimer.remainingMs(s.deadline, clock.now())
        Sleep.EndOfTrack -> {
            val d = (if (loaded?.kind == active) backend.durationMs else null) ?: current?.durationMs ?: 0L
            maxOf(0L, d - position(active))
        }
    }

    // MARK: - Loudness

    /**
     * The loudness switch: the loaded track's level and the preloaded next one's change in place (nothing is
     * preloaded again, so a switch in a track's last seconds keeps the change gapless).
     */
    private fun loudnessChanged() {
        val l = loaded ?: return
        if (l.kind != Kind.track) return
        val item = music.current ?: return
        val next = preloadedNext?.let { p -> queue(p.kind).items.firstOrNull { it.id == p.id } }
        backend.setGain(Gain.factor(item, prefs.loudness), next?.let { Gain.factor(it, prefs.loudness) } ?: 1f)
    }

    // MARK: - Backend events

    /** A callback about the item loaded last; anything else is about a replaced item and is dropped. */
    private fun isCurrent(g: Int) = loaded != null && g == loadGeneration

    override fun backendStarted(generation: Int) {
        if (!isCurrent(generation)) return
        cancelRetry()            // the retry worked: the next blip gets a fresh set, from where it is then
        disarmStall()
        playing = true; buffering = false; error = null
        if (loaded?.kind == Kind.track) consecutiveFailures = 0
        prefetchUpcoming()
        emitState(active)
    }

    /**
     * Paused by something other than the engine (a focus loss, a route change). Idempotent: a transient loss
     * then a permanent one reports twice, and only the first acts.
     */
    override fun backendPaused(generation: Int) {
        if (!isCurrent(generation) || !playing) return
        disarmStall()
        syncPosition()
        playing = false; buffering = false; pausedBySystem = true
        if (active == Kind.episode) saveOutgoingEpisode()
        persist(); emitState(active)
    }

    /**
     * The output went away or the focus was lost for good (iOS `routeChange(.oldDeviceUnavailable)`, an interruption
     * ending without `.shouldResume`): a user pause, so nothing brings it back. Also after a system pause (a call, then
     * the focus lost for good): that one stops counting as the system's.
     */
    override fun backendPausedForGood(generation: Int) {
        if (!isCurrent(generation)) return
        if (playing || pausedBySystem) pause()
    }

    override fun backendBuffering(on: Boolean, generation: Int) {
        if (!isCurrent(generation)) return
        buffering = on
        if (on) armStall() else disarmStall()
        emitState(active)
    }

    override fun backendTick(positionMs: Long, generation: Int) {
        if (!isCurrent(generation)) return
        knownMs = positionMs
        val l = listen
        if (l != null && loaded?.kind == Kind.track) {
            val d = positionMs - l.lastMs
            if (d in 1L..1999L) l.seconds += d / 1000.0
            l.lastMs = positionMs
        }
        fadeTowardsTheEnd(positionMs)
        catchUpSleep(playing = true)
        emitState(active)
        periodic()
        retryLyricsIfDue()
    }

    override fun backendFinished(generation: Int) {
        if (!isCurrent(generation)) return
        val l = loaded ?: return
        cancelRetry(); disarmStall()
        if (sleep == Sleep.EndOfTrack) return sleepAtTheEnd(l)
        if (l.kind == Kind.track) {
            finishListen(ListenEnd.ended)
            consecutiveFailures = 0
            if (modes.repeatMode == RepeatMode.one && music.current?.id == l.id) {
                // Repeat one: the same item again (the preloaded copy of it: gapless), a new listen and so a new play.
                load(Kind.track, music.index, 0, autoplay = true)
                afterMusicMove()
                return
            }
            dropSubstitutes()
            advanceMusic(user = false)
        } else {
            sendProgress(l.id, 0, played = true, emit = false)
            if (episodes.index + 1 < episodes.items.size) {
                val i = episodes.index + 1
                load(Kind.episode, i, resumeAt(episodes.items[i]), autoplay = true)
                emitQueue(Kind.episode)
            } else {
                loaded = null; playing = false; buffering = false
                modify(Kind.episode) { it.copy(positionMs = 0) }
            }
            emitState(Kind.episode); persist()
        }
    }

    /**
     * "End of this track" reached: music moves on to the next track, loaded but paused (repeat one gives way);
     * an episode stays where it ended. Then the timer is off and the volume back for the next play.
     */
    private fun sleepAtTheEnd(l: Loaded) {
        sleep = null; sleepJobs = emptyList()
        if (l.kind == Kind.track) {
            finishListen(ListenEnd.ended)
            consecutiveFailures = 0
            dropSubstitutes()
            advanceMusic(user = false, autoplay = false)
        } else {
            sendProgress(l.id, 0, played = true, emit = true)
            loaded = null; playing = false; buffering = false
            modify(Kind.episode) { it.copy(positionMs = 0) }
            persist()
        }
        setFade(1f)
        emitStates()
    }

    /** `network`: the failure is the network's (the parameter keeps the interface's name; the status is [NetworkStatus]). */
    override fun backendFailed(network: Boolean, generation: Int) {
        if (!isCurrent(generation)) return
        val l = loaded ?: return
        val item = queue(l.kind).current ?: return
        buffering = false
        disarmStall()
        val pos = if (backend.positionMs > 0) backend.positionMs else knownMs
        if (network && this.network.isOnline) {
            val attempt = netRetry?.attempt ?: 0
            if (attempt < NETWORK_RETRY_DELAYS.size) {
                // From where it is now: a retry that played on and failed again resumes there, not at the first blip.
                netRetry?.timer?.cancel()
                netRetry = NetRetry(attempt + 1, maxOf(pos, netRetry?.positionMs ?: 0L),
                    scheduler.after(NETWORK_RETRY_DELAYS[attempt]) { retryNow() })
                emitState(l.kind)
                return
            }
        }
        cancelRetry()
        if (l.kind == Kind.episode) {
            // Offline, or the retries ran out, or the file itself: the episode keeps its place, music plays instead.
            val wanted = playing
            modify(Kind.episode) { it.copy(positionMs = pos) }
            loaded = null; playing = false; error = texts.cannotPlay
            saveOutgoingEpisode(item.id, pos)
            emitState(Kind.episode); persist()
            if (wanted) fallBackToMusic()
            return
        }
        finishListen(ListenEnd.switched)
        if (network && !this.network.isOnline) {
            // Offline: something on the phone now; the interrupted item is requeued to play once online.
            // It is not marked failed: nothing is wrong with it.
            if (apply(chooseNext(mustBeLocal = true), requeue = item)) {
                onEvent?.invoke(NativeEvent.Notice(texts.offlineNotice))
                afterMusicMove()
            } else {
                modify(Kind.track) { it.copy(positionMs = pos) }
                stopForNetwork()
            }
            return
        }
        failedAt[item.id] = clock.now()
        consecutiveFailures += 1
        // A network error that outlasted its retries moves on to something on the phone.
        advanceMusic(user = false, forceLocal = network)
    }

    // MARK: - Network, stalls, fallbacks

    private fun networkChanged() {
        if (detached || !network.isOnline) return
        sync.reconnected()
        if (refillTimer != null || refillWanted) {
            refillTimer?.cancel(); refillTimer = null
            refillWanted = false
            maybeRefill()
        }
        if (!stoppedOffline) return
        if (canAutoStart?.invoke() == false) {
            // Not now: paused by the system (no longer "offline"), resumed when the user is back or presses play.
            if (!deferredResume) {
                deferredResume = true
                pausedBySystem = true
                error = null
                emitState(active)
            }
            return
        }
        resumeAfterNetwork()
    }

    /** The user is back (the Activity started): a resume [canAutoStart] held back runs now, if it is still due. */
    fun resumeDeferred() {
        if (!deferredResume) return
        deferredResume = false
        if (detached || !stoppedOffline || !network.isOnline) return
        resumeAfterNetwork()
    }

    /** The app services dropped this engine (a server change, after [reset]): it reacts to nothing more. */
    fun detach() {
        detached = true
        deferredResume = false
    }

    /** What the network coming back does for a player [stoppedOffline] ([offlineResume]). */
    private fun resumeAfterNetwork() {
        stoppedOffline = false
        deferredResume = false
        error = null
        when {
            offlineResume == OfflineResume.shuffle -> tasks.run { shuffleFavorites() }
            offlineResume == OfflineResume.advance && active == Kind.track && music.current != null -> advanceMusic(user = false)
            offlineResume == OfflineResume.replay && active == Kind.track && music.current != null -> play()
            else -> emitState(active)
        }
        offlineResume = OfflineResume.replay
    }

    /** Nothing on the phone to play and no network: stopped, and started again when the network is back. */
    private fun stopForNetwork() {
        loaded = null; playing = false; buffering = false
        error = texts.offlineStopped
        stoppedOffline = true; offlineResume = OfflineResume.replay
        emitQueue(Kind.track); emitState(Kind.track); persist()
    }

    private fun armStall() {
        if (stallTimer != null || !playing || active != Kind.track) return
        val l = loaded ?: return
        val cur = music.current ?: return
        if (l.kind != Kind.track || isLocal(cur)) return
        stallTimer = scheduler.after(STALL_SECONDS) { stallSwitch() }
    }

    private fun disarmStall() {
        stallTimer?.cancel()
        stallTimer = null
    }

    /**
     * The web's stall switch: a remote track that is not moving gives way to one on the phone (only a local
     * choice is taken), and the interrupted track is requeued right after it.
     */
    private fun stallSwitch() {
        stallTimer = null
        if (!playing || !buffering) return
        val l = loaded ?: return
        val cur = music.current ?: return
        if (l.kind != Kind.track || isLocal(cur)) return
        val c = chooseNext(mustBeLocal = true)
        if (c == NextChooser.Choice.None) return
        finishListen(ListenEnd.switched)
        apply(c, requeue = cur)
        onEvent?.invoke(NativeEvent.Notice(texts.stallNotice))
        afterMusicMove()
    }

    /**
     * An episode that cannot play while it was meant to: music plays instead (the queue, else cached
     * favorites), and the page is told why.
     */
    private fun fallBackToMusic() {
        onEvent?.invoke(NativeEvent.Notice(texts.episodeFallbackNotice))
        if (music.current != null) play(Kind.track) else tasks.run { shuffleFavorites() }
    }

    // MARK: - Internals

    private fun queue(k: Kind) = if (k == Kind.track) music else episodes

    private inline fun modify(k: Kind, f: (PlaybackQueue) -> PlaybackQueue) {
        if (k == Kind.track) music = f(music) else episodes = f(episodes)
    }

    private fun position(k: Kind): Long = if (loaded?.kind == k) livePosition() else queue(k).positionMs

    /** While a retry waits, its position (the failed item can read 0); otherwise the backend's. */
    private fun livePosition(): Long = netRetry?.positionMs ?: backend.positionMs

    private fun setPosition(k: Kind, ms: Long) {
        modify(k) { it.copy(positionMs = maxOf(0L, ms)) }
        emitState(k); persist()
    }

    /** Moves the other kind's index without playing anything. */
    private fun step(k: Kind, d: Int) {
        val i = queue(k).index + d
        if (i !in queue(k).items.indices) return
        modify(k) { it.copy(index = i, positionMs = 0) }
        emitQueue(k); emitState(k); persist()
        if (k == Kind.track) { sync.edited(); maybeRefill() }
    }

    /** The backend's position into the active queue. */
    private fun syncPosition() {
        val l = loaded ?: return
        val pos = livePosition()
        modify(l.kind) { it.copy(positionMs = pos) }
    }

    private fun persist(touch: Boolean = true) {
        syncPosition()
        if (touch) savedAt = clock.now().epochSecond
        store.save(QueueSnapshot(QueueSnapshot.CURRENT_VERSION, music, episodes, active, savedAt, ownDevice, modes))
    }

    private fun isFailed(item: Item): Boolean =
        failedAt[item.id]?.let { Duration.between(it, clock.now()) < FAILED_TTL } ?: false

    private fun isLocal(item: Item): Boolean = item.trackId?.let { cache.localFile(it) } != null

    private fun mediaSource(item: Item): MediaSource? {
        item.trackId?.let { id -> cache.localFile(id)?.let { return MediaSource.File(it) } }
        val token = api.token ?: return null
        return MediaSource.Remote(api.streamUrl(item, prefs.quality), token)
    }

    /** Loads `queue(k).items[index]` into the backend (the caller has saved whatever was playing). */
    private fun load(k: Kind, index: Int, startMs: Long, autoplay: Boolean) {
        finishListen(ListenEnd.switched)
        cancelRetry(); disarmStall()
        stoppedOffline = false; pausedBySystem = false
        modify(k) { it.copy(index = index, positionMs = startMs) }
        val preloaded = preloadedNext
        preloadedNext = null
        val item = queue(k).current ?: return
        val trackId = item.trackId
        if (trackId != null) fetchLyrics(trackId) else { lyricsId = null; carLyrics = null; lyricsRetry = null }
        val same = preloaded?.takeIf { it.kind == k && it.id == item.id }?.source
        val src = same ?: mediaSource(item)
        if (src == null) {
            // Not on the phone and no token to stream it: a failure of this item, on to something on the phone.
            loaded = null; buffering = false
            if (k == Kind.track && autoplay) {
                failedAt[item.id] = clock.now()
                consecutiveFailures += 1
                advanceMusic(user = false, forceLocal = true)
            } else {
                playing = false; error = texts.cannotPlay
            }
            return
        }
        if (src is MediaSource.File && trackId != null) cache.touch(trackId)
        loaded = Loaded(k, index, item.id)
        playing = autoplay; buffering = autoplay; error = null
        if (trackId != null) listen = Listen(trackId, clock.now().epochSecond, 0.0, startMs)
        if (k == Kind.episode) lastProgress = clock.now()
        knownMs = startMs
        loadGeneration += 1
        if (sleep == Sleep.EndOfTrack && fadeLevel != 1f) setFade(1f)    // the timer goes on with this item, from full volume
        if (autoplay) willStartPlayback?.invoke()
        backend.load(src, startMs, autoplay, if (k == Kind.episode) rate.toFloat() else 1f, Gain.factor(item, prefs.loudness), loadGeneration)
        preloadNext(k)
    }

    /**
     * The next item into the backend for a gapless change.
     * Repeat one: the current item again (the loop is gapless: [load] matches the preloaded source on the id).
     * Repeat all without shuffle, at the last item: the first one, which the next pass starts with.
     * "End of this track" armed: nothing, so the player stops at the end instead of running into the next.
     */
    private fun preloadNext(k: Kind) {
        if (sleep == Sleep.EndOfTrack) {
            preloadedNext = null
            backend.preload(null, 1f)
            return
        }
        val q = queue(k)
        val usable = { i: Item -> k == Kind.episode || (!isFailed(i) && (network.isOnline || isLocal(i))) }
        var next = q.upcoming.firstOrNull(usable)
        if (k == Kind.track && modes.repeatMode == RepeatMode.one) {
            next = q.current
        } else if (k == Kind.track && next == null && modes.repeatMode == RepeatMode.all && !modes.shuffle) {
            q.items.firstOrNull()?.takeIf(usable)?.let { next = it }
        }
        val n = next
        val src = n?.let { mediaSource(it) }
        // A stream preloaded here is also downloaded by the lookahead: accepted, so the change stays gapless.
        preloadedNext = if (n != null && src != null) Preloaded(k, n.id, src) else null
        backend.preload(src, n?.let { Gain.factor(it, prefs.loudness) } ?: 1f)
    }

    /**
     * The lookahead: the next 2 tracks into the cache, once the current music item sounds (so its own
     * stream starts first), and again when the queue changes under it. The cache skips what it has or is
     * already fetching.
     * Repeat one: the current track first, when it streams, so the loops after the first play from the phone.
     */
    private fun prefetchUpcoming() {
        val l = loaded ?: return
        if (l.kind != Kind.track || !playing || buffering) return
        val next = music.upcoming.take(2).toMutableList()
        if (modes.repeatMode == RepeatMode.one) music.current?.takeIf { !isLocal(it) }?.let { next.add(0, it) }
        if (next.isNotEmpty()) cache.prefetch(next)
    }

    private fun retryNow() {
        val r = netRetry ?: return
        val l = loaded ?: return
        val item = queue(l.kind).current ?: return
        val src = mediaSource(item) ?: return
        r.timer = null
        playing = true; buffering = true
        listen?.lastMs = r.positionMs
        knownMs = r.positionMs
        loadGeneration += 1
        willStartPlayback?.invoke()
        backend.load(src, r.positionMs, true, if (l.kind == Kind.episode) rate.toFloat() else 1f, Gain.factor(item, prefs.loudness), loadGeneration)
        preloadNext(l.kind)          // the load dropped the preloaded next item
        emitState(l.kind)
    }

    private fun cancelRetry() {
        netRetry?.timer?.cancel()
        netRetry = null
    }

    /**
     * Leaves the active kind for the other: its position stays in its queue, the listen or the episode's
     * progress is saved.
     */
    private fun leaveActive() {
        if (loaded?.kind != active) return
        syncPosition()
        cancelRetry(); disarmStall()
        if (active == Kind.track) finishListen(ListenEnd.switched) else saveOutgoingEpisode()
        backend.pause()
        loaded = null; playing = false; buffering = false; error = null
    }

    private fun saveOutgoingEpisode(emit: Boolean = true) {
        val l = loaded ?: return
        if (l.kind != Kind.episode) return
        saveOutgoingEpisode(l.id, livePosition(), emit)
    }

    private fun saveOutgoingEpisode(id: String, positionMs: Long, emit: Boolean = true) {
        sendProgress(id, maxOf(0L, positionMs) / 1000, played = false, emit = emit)
    }

    /**
     * `PUT /episodes/{id}/progress`, and the same values into every queued copy of the episode's `meta`
     * (as the web patches its snapshots), so going back or a relaunch resumes where it was. The queue goes
     * to the page on a pause, switch, next or end (`emit`), not on the 15 s save.
     */
    private fun sendProgress(id: String, positionS: Long, played: Boolean, emit: Boolean) {
        var changed = false
        episodes = episodes.copy(items = episodes.items.map { ep ->
            if (ep.id != id) return@map ep
            var meta: JsonElement = ep.meta.setting("position_s", JsonPrimitive(if (played) 0L else positionS))
            if (played) meta = meta.setting("played", JsonPrimitive(true))
            if (meta == ep.meta) return@map ep
            changed = true
            ep.copy(meta = meta)
        })
        if (changed) {
            persist()
            if (emit) emitQueue(Kind.episode)
        }
        lastProgress = clock.now()
        tasks.run {
            // A failure is not retried: the next save (15 s, pause, the end) carries a newer position anyway.
            try { api.episodeProgress(id, positionS, played) } catch (ex: CancellationException) { throw ex } catch (_: Exception) {}
        }
    }

    /** What is due while playing: `PUT /queue` every 15 s for music, episode progress every 15 s. */
    private fun periodic() {
        if (!playing) return
        val l = loaded ?: return
        if (l.kind != active) return
        if (l.kind == Kind.track) {
            sync.playingTick()
        } else if (lastProgress?.let { Duration.between(it, clock.now()) >= PROGRESS_EVERY } != false) {
            saveOutgoingEpisode(emit = false)
        }
    }

    private fun finishListen(end: ListenEnd) {
        val l = listen ?: return
        listen = null
        if (l.seconds < 1) return
        events.add(l.trackId, l.startedAt, l.seconds.roundToLong(),
            skipped = end == ListenEnd.skip && l.seconds < SKIP_THRESHOLD_S, quality = prefs.quality)
    }

    /**
     * `wrap` (repeat all, on the paths the web's `changeOpts`/`onError` pass it): start over at the end. The
     * local-only switches (a stall, offline) never wrap: they want something on the phone now.
     */
    private fun chooseNext(mustBeLocal: Boolean, wrap: Boolean = false): NextChooser.Choice =
        NextChooser.choose(music, mustBeLocal, ::isFailed, ::isLocal, cache.cachedFavorites().map(ApiTrack::toItem), random, wrap)

    /**
     * Plays `c` next: a queued item moved up to just after the current one, or a cached favorite inserted
     * there; `requeue` (an interrupted item) goes right after it. False for `None`. `autoplay` false: loaded,
     * not played (the sleep timer's end of track).
     */
    private fun apply(c: NextChooser.Choice, requeue: Item? = null, autoplay: Boolean = true): Boolean {
        when (c) {
            is NextChooser.Choice.Queue -> if (c.index != music.index + 1) {
                val items = music.items.toMutableList()
                val item = items.removeAt(c.index)
                items.add(music.index + 1, item)
                music = music.copy(items = items)
            }
            is NextChooser.Choice.Insert -> {
                val items = music.items.toMutableList()
                items.add(music.index + 1, c.item)
                music = music.copy(items = items)
                if (modes.repeatMode == RepeatMode.all) substitutes.add(Substitute(c.item.id, requeue?.id))
            }
            is NextChooser.Choice.Wrap -> {
                // Repeat all: the whole queue again, from its first item that may play (not failed; on the phone
                // offline or after failures). The chooser only wraps when there is one.
                dropSubstitutes()                 // never part of the next pass
                music = PlayModes.newPass(music, modes.shuffle, random)
                val local = c.mustBeLocal || !network.isOnline
                val start = music.items.indexOfFirst { !isFailed(it) && (!local || isLocal(it)) }
                if (start < 0) return false
                load(Kind.track, start, 0, autoplay)
                return true
            }
            NextChooser.Choice.None -> return false
        }
        if (requeue != null) {
            val items = music.items.toMutableList()
            items.add(music.index + 2, requeue)
            music = music.copy(items = items)
        }
        load(Kind.track, music.index + 1, 0, autoplay)
        return true
    }

    private fun afterMusicMove() {
        emitQueue(Kind.track); emitState(Kind.track); persist()
        sync.edited()
        maybeRefill()
    }

    /** Picks and loads the next music item (the end of one, a failure, or the user's next). */
    private fun advanceMusic(user: Boolean, forceLocal: Boolean = false, autoplay: Boolean = true) {
        val offline = !network.isOnline
        val mustBeLocal = forceLocal || offline || consecutiveFailures >= MAX_CONSECUTIVE_FAILURES
        val choice = chooseNext(mustBeLocal, wrap = modes.repeatMode == RepeatMode.all)
        if (choice is NextChooser.Choice.Insert && offline) onEvent?.invoke(NativeEvent.Notice(texts.offlineNotice))
        if (!apply(choice, autoplay = autoplay)) {
            if (user) return      // nothing to go to: the user's next leaves things as they are
            // The end, or nothing playable: stopped, and play starts the current item again.
            val failed = consecutiveFailures > 0 || forceLocal
            playing = false; buffering = false; loaded = null
            if (failed) error = texts.cannotPlay else music = music.copy(positionMs = 0)
            if (offline) {
                if (failed) {
                    // The current item could not play: it is tried again when the network is back.
                    error = texts.offlineStopped; stoppedOffline = true; offlineResume = OfflineResume.replay
                } else if (music.upcoming.isNotEmpty()) {
                    // It ended, and only streams are next: the next one plays when the network is back.
                    error = texts.offlineStopped; stoppedOffline = true; offlineResume = OfflineResume.advance
                }
                // The queue ended normally: stopped, as online. Nothing replays on reconnect.
            }
        }
        afterMusicMove()
    }

    /**
     * Radio, shuffle and favorites queues refill when 2 or fewer playable items are left
     * (the web's refill effect: 20 favorites or random tracks, 10 radio tracks).
     */
    private fun maybeRefill() {
        if (modes.repeatMode == RepeatMode.all) return     // repeat all loops the queue as it is
        if (refilling || refillExhausted || refillTimer != null || music.items.isEmpty() || music.source !in REFILLED) return
        val upcoming = music.upcoming.filter { !isFailed(it) }
        if (upcoming.size > REFILL_WHEN_LEFT) return
        // Offline the request can only fail: it is asked for when the network comes back ([networkChanged]).
        if (!network.isOnline) { refillWanted = true; return }
        refillWanted = false
        refilling = true
        val refill = ++refillCount
        val src = music.source
        val gen = musicGeneration
        // Favorites loop, so only the current item and what is still upcoming are excluded (the current one too: else
        // a one-song queue, the page's played favorite, could get that song again right after itself, and Next would
        // play it again); radio and shuffle exclude the recent queue.
        val exclude = (if (src == QueueSource.favorites) listOfNotNull(music.current) + upcoming
            else music.items.takeLast(REFILL_EXCLUDE_MAX)).mapNotNull { it.trackId }
        tasks.run {
            val answer: Pair<List<ApiTrack>, QueueSource>? = try {
                when (src) {
                    QueueSource.favorites -> api.randomFavorites(REFILL_SIZE, exclude).let {
                        it.tracks to if (it.source == "favorites") QueueSource.favorites else QueueSource.shuffle
                    }
                    QueueSource.shuffle -> api.randomTracks(REFILL_SIZE, exclude) to QueueSource.shuffle
                    else -> api.radio(RADIO_REFILL_SIZE, exclude) to QueueSource.radio
                }
            } catch (ex: CancellationException) {
                throw ex
            } catch (_: Exception) {
                null
            } finally {
                // Also when cancelled: a refill that never ends must not block every later one. Only this refill's
                // own flag: a newer refill may have started since a reset.
                if (refillCount == refill) refilling = false
            }
            if (gen != musicGeneration) return@run maybeRefill()   // the queue was replaced meanwhile
            if (answer == null) {
                refillTimer = scheduler.after(REFILL_RETRY) {
                    refillTimer = null
                    maybeRefill()
                }
                return@run
            }
            val add = appendable(answer.first.map(ApiTrack::toItem), answer.second)
            if (add.isNotEmpty()) {
                music = music.copy(items = music.items + add, source = answer.second)
                emitQueue(Kind.track); persist(); sync.edited()
                prefetchUpcoming()
            } else if (answer.second != QueueSource.favorites || answer.first.isEmpty()) {
                refillExhausted = true
            }
        }
    }

    /**
     * Never a track twice in one answer, nor one already queued; a favorites refill loops, so it only
     * skips what is still upcoming (the web's `appendable`).
     */
    private fun appendable(items: List<Item>, source: QueueSource): List<Item> {
        val have = (if (source == QueueSource.favorites) music.upcoming else music.items).mapTo(HashSet()) { it.id }
        return items.filter { have.add(it.id) }
    }

    /** The first hello's `resume`: the server's queue replaces the local one when [QueueSync.shouldAdopt] says so. */
    private suspend fun adoptServerQueue() {
        if (playing) return
        val e = epoch
        val answer = try { api.queue() } catch (ex: CancellationException) { throw ex } catch (_: Exception) { return }
        val sq = answer.queue
        if (epoch != e || !QueueSync.shouldAdopt(sq, music.items.isEmpty(), savedAt, ownDevice, playing)) return
        val byId = HashMap<Long, ApiTrack>()
        for (t in answer.tracks) byId.putIfAbsent(t.track.id, t)
        val items = mutableListOf<Item>()
        var index = 0
        var currentFound = false
        sq.trackIds.forEachIndexed { i, id ->
            if (i == sq.currentIndex) { index = items.size; currentFound = byId[id] != null }
            byId[id]?.let { items.add(it.toItem()) }
        }
        if (items.isEmpty()) return
        if (loaded?.kind == Kind.track) { backend.stop(); loaded = null; listen = null }
        musicGeneration += 1; refillExhausted = false
        music = PlaybackQueue(items, minOf(index, items.size - 1), if (currentFound) maxOf(0L, sq.positionMs) else 0L, QueueSource.restored)
        emitQueue(Kind.track); emitState(Kind.track); persist()
    }

    // MARK: - Events to the web

    private fun emitQueue(k: Kind) {
        val q = queue(k)
        onEvent?.invoke(NativeEvent.Queue(k, q.items, q.index, q.source))
    }

    /** Both kinds' states, the active one last (the web mirrors each kind from its latest state). */
    private fun emitStates() {
        emitState(if (active == Kind.track) Kind.episode else Kind.track)
        emitState(active)
    }

    private fun emitState(k: Kind) {
        val q = queue(k)
        val isActive = k == active
        val isLoaded = loaded?.kind == k
        val duration = (if (isLoaded) backend.durationMs else null) ?: q.current?.durationMs ?: 0L
        onEvent?.invoke(NativeEvent.State(
            kind = k, itemId = q.current?.id, index = q.index, playing = isActive && playing,
            positionMs = position(k), durationMs = duration, buffering = isActive && buffering,
            error = if (isActive) error else null, rate = if (k == Kind.episode) rate else 1.0,
            shuffle = k == Kind.track && modes.shuffle, repeat = if (k == Kind.track) modes.repeatMode else RepeatMode.off,
            sleepRemainingMs = sleepRemainingMs()))
        if (isActive) refreshNowPlaying()
    }

    // MARK: - Now playing and car lyrics

    /**
     * Writes now playing when what it shows changed: the item, playing or not (buffering counts as not, so the
     * lock screen's clock does not run ahead), the rate, the duration or the car-lyrics line; and after a seek.
     * Ticks only get here, so a line change is one write, not one every half second.
     */
    private fun refreshNowPlaying() {
        val np = nowPlaying ?: return
        val force = forceNowPlaying
        forceNowPlaying = false
        val item = queue(active).current
        if (item == null) {
            if (nowPlayingKey != null) { nowPlayingKey = null; np.clear() }
            return
        }
        val pos = position(active)
        val line = if (active == Kind.track && prefs.carLyrics && lyricsId == item.trackId) carLyrics?.line(pos) else null
        val r = if (active == Kind.episode) rate else 1.0
        val duration = if (loaded?.kind == active) backend.durationMs else null
        val key = NowPlayingKey(item.kind, item.id, item.title, item.artist, item.album, playing && !buffering, r, duration, line)
        if (!force && key == nowPlayingKey) return
        nowPlayingKey = key
        np.show(item, pos, duration, r, key.playing, line)
    }

    /**
     * The car-lyrics lines for a track that is being loaded (once per track: a retry keeps them). An answer,
     * found or not, is kept; a fetch that threw (offline) leaves the song title and is asked again while the
     * track plays, after 30 s, then 60 s, … up to 10 minutes ([retryLyricsIfDue], on ticks).
     */
    private fun fetchLyrics(id: Long) {
        if (nowPlaying == null || !prefs.carLyrics) { lyricsId = null; carLyrics = null; lyricsRetry = null; return }
        if (lyricsId == id) return
        lyricsId = id; carLyrics = null; lyricsRetry = null
        // A cached track's lyrics are on the phone: they show at once, offline too. Online, fresh ones are asked
        // as well, since an offset set since the download (here or on another device) must reach the car.
        val doc = cache.cachedLyrics(id)
        if (doc != null) {
            carLyrics = CarLyrics.of(doc)
            if (network.isOnline) requestLyrics(id)
            return
        }
        requestLyrics(id)
    }

    private fun requestLyrics(id: Long) {
        // Offline: asked on the first tick back online ([retryLyricsIfDue]), not tried now.
        if (!network.isOnline) { lyricsRetry = LyricsRetry(clock.now(), LYRICS_RETRY_S); return }
        val e = epoch
        lyricsInFlight = id
        lyricsRequest += 1
        val request = lyricsRequest
        tasks.run {
            val doc = try { api.lyrics(id) } catch (ex: CancellationException) { throw ex } catch (_: Exception) { null }
            if (lyricsInFlight == id) lyricsInFlight = null
            if (doc != null) cache.updateLyrics(id, doc)
            // Only the latest request counts: an older answer may predate a change the web just saved.
            if (epoch != e || lyricsId != id || lyricsRequest != request) return@run
            if (doc != null) {
                carLyrics = CarLyrics.of(doc)
                lyricsRetry = null
            } else {
                val wait = lyricsRetry?.nextWait ?: LYRICS_RETRY_S
                lyricsRetry = LyricsRetry(clock.now().plusMillis((wait * 1000).toLong()), minOf(wait * 2, LYRICS_RETRY_MAX_S))
            }
            refreshNowPlaying()
        }
    }

    private fun retryLyricsIfDue() {
        if (!network.isOnline || lyricsInFlight != null || !prefs.carLyrics || nowPlaying == null) return
        val r = lyricsRetry ?: return
        val id = lyricsId ?: return
        if (clock.now().isBefore(r.at) || loaded?.kind != Kind.track || music.current?.trackId != id) return
        requestLyrics(id)
    }

    companion object {
        private const val TAG = "PlayerEngine"

        const val SKIP_THRESHOLD_S = 30.0
        const val MAX_CONSECUTIVE_FAILURES = 3
        val NETWORK_RETRY_DELAYS = listOf(2.0, 5.0, 10.0)
        val FAILED_TTL: Duration = Duration.ofMinutes(30)
        val PROGRESS_EVERY: Duration = Duration.ofSeconds(15)
        const val REFILL_RETRY = 30.0
        const val REFILL_EXCLUDE_MAX = 300
        /** A refill is asked for when this many playable items or fewer are left. */
        const val REFILL_WHEN_LEFT = 2
        /** Favorites and shuffle refills ask for this many; radio for [RADIO_REFILL_SIZE]. */
        const val REFILL_SIZE = 20
        const val RADIO_REFILL_SIZE = 10
        private val REFILLED = setOf(QueueSource.favorites, QueueSource.shuffle, QueueSource.radio)
        const val SHUFFLE_SIZE = 50
        /** A previous press this far into an item restarts it instead. */
        const val RESTART_PREV_MS = 3000L
        /** A held fast-forward stops this far before the end of the item. */
        const val SCAN_END_MARGIN_MS = 1000L
        /** An episode saved this close to its end starts from the beginning (the web's `resumeAt`). */
        const val NEAR_END_S = 30L
        /** A remote track buffering this long while playing gives way to one on the phone (the web's STALL_MS). */
        const val STALL_SECONDS = 6.0
        /** Car lyrics that could not be fetched are asked again after this, then twice as long each time. */
        const val LYRICS_RETRY_S = 30.0
        const val LYRICS_RETRY_MAX_S = 600.0
        /** The sleep timer's fade: this long, in this many steps (the web's FADE_MS and FADE_STEP_MS). */
        const val SLEEP_FADE_S = 10.0
        const val SLEEP_FADE_STEPS = 40

        /** Where an episode starts: its saved position, unless played, unstarted or nearly finished (the web's `resumeAt`). */
        fun resumeAt(ep: Item): Long {
            fun int(v: JsonElement?): Long? = (v as? JsonPrimitive)?.takeIf { !it.isString }?.content
                ?.let { it.toLongOrNull() ?: it.toDoubleOrNull()?.takeIf(Double::isFinite)?.toLong() }
            if (ep.meta["played"] == JsonPrimitive(true)) return 0
            val pos = int(ep.meta["position_s"]) ?: 0L
            if (pos <= 0) return 0
            val dur = int(ep.meta["duration_s"]) ?: (ep.durationMs / 1000)
            if (dur > 0 && pos > dur - NEAR_END_S) return 0
            return pos * 1000
        }
    }
}
