package lark.service

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import lark.bridge.Item
import lark.bridge.RepeatMode
import lark.player.PlayModes
import lark.player.PlayerEngine
import java.lang.ref.WeakReference
import kotlin.math.roundToLong

/**
 * Lock screen, notification, headphone and car buttons: a port of `ios/Sources/Player/RemoteCommands.swift`. The media
 * session ([SessionPlayer], [PlaybackService]) turns its player commands and custom commands into these handlers. The
 * car's play goes to the last media app even when it is not running: with nothing loaded it resumes the saved queue or
 * shuffles cached favorites ([handlePlay]). Skip ±(30/15 s) replaces next/previous on the notification, so it is on
 * only for 频道 episodes; repeat and shuffle are music's: on only while music is the active kind.
 *
 * What it shows (the state of the iOS `MPRemoteCommandCenter`) is [kind], [skipEnabled], [modesEnabled], [repeatMode]
 * and [shuffle]; listeners hear every change. Main thread only.
 */
class RemoteCommands(
    private val scope: CoroutineScope,
    /**
     * The engine for this command, resolved when it arrives: the commands are installed once, and the engine is built
     * on first use and rebuilt for another server. Null: nothing can play yet.
     */
    private val resolve: () -> PlayerEngine?,
) {
    /** What a handler did (iOS `MPRemoteCommandHandlerStatus`). */
    enum class Status { success, noActionableNowPlayingItem, commandFailed }

    private var attached: WeakReference<PlayerEngine>? = null
    private val listeners = mutableListOf<() -> Unit>()

    /** The active kind the buttons follow. */
    var kind = Item.Kind.track
        private set
    /** Skip forward 30 s and back 15 s: episodes only. */
    val skipEnabled: Boolean get() = kind == Item.Kind.episode
    /** Repeat and shuffle: music only. */
    val modesEnabled: Boolean get() = kind == Item.Kind.track
    /** The modes as the lock screen and the car show them. */
    var repeatMode = RepeatMode.off
        private set
    var shuffle = false
        private set

    /** The engine commands go to now. */
    val engine: PlayerEngine? get() = resolve()

    /** Commands for this engine only, held weakly (iOS `init(center:engine:)`). */
    constructor(scope: CoroutineScope, engine: PlayerEngine) : this(scope, weakly(engine)) {
        attach(engine)
    }

    fun addListener(fn: () -> Unit) { listeners.add(fn) }
    fun removeListener(fn: () -> Unit) { listeners.remove(fn) }
    private fun changed() { listeners.toList().forEach { it() } }

    /** A newly built engine: the skip buttons follow its active kind, repeat and shuffle its modes. */
    fun attach(engine: PlayerEngine) {
        attached?.get()?.let { it.onActiveChange = null; it.onModesChange = null }
        attached = WeakReference(engine)
        engine.onActiveChange = { update(it) }
        engine.onModesChange = { show(it) }
        kind = engine.active
        show(engine.modes)
    }

    /** Skip intervals for episodes only; repeat and shuffle for music only; next and previous always. */
    fun update(kind: Item.Kind) {
        if (kind == this.kind) return
        this.kind = kind
        changed()
    }

    /** The modes as the lock screen and the car show them. */
    fun show(m: PlayModes) {
        repeatMode = m.repeatMode
        shuffle = m.shuffle
        changed()
    }

    /** Lets the engine go (the engine's callbacks are cleared). */
    fun detach() {
        attached?.get()?.let { it.onActiveChange = null; it.onModesChange = null }
        attached = null
    }

    // MARK: Handlers

    /** The cold start's shuffle from the server, while it runs (the media service holds the foreground meanwhile). */
    private var coldStart: Job? = null

    /** A play's shuffle from the server is still running: playback may start any moment. */
    val starting: Boolean get() = coldStart?.isActive == true

    fun playNow(): Status {
        val engine = resolve() ?: return Status.noActionableNowPlayingItem
        coldStart = startPlay(engine, scope)
        return Status.success
    }

    fun pauseNow(): Status {
        val engine = resolve()?.takeIf { it.current != null } ?: return Status.noActionableNowPlayingItem
        engine.pause()
        return Status.success
    }

    fun toggleNow(): Status = if (resolve()?.playing == true) pauseNow() else playNow()

    fun nextNow(): Status {
        val engine = resolve()?.takeIf { it.current != null } ?: return Status.noActionableNowPlayingItem
        engine.next()
        return Status.success
    }

    fun previousNow(): Status {
        val engine = resolve()?.takeIf { it.current != null } ?: return Status.noActionableNowPlayingItem
        engine.prev()
        return Status.success
    }

    fun seekNow(seconds: Double): Status {
        val engine = resolve()?.takeIf { it.current != null && seconds.isFinite() } ?: return Status.noActionableNowPlayingItem
        engine.seek((seconds * 1000).roundToLong())
        return Status.success
    }

    fun skipNow(seconds: Double): Status {
        val engine = resolve()?.takeIf { it.current != null && seconds.isFinite() } ?: return Status.noActionableNowPlayingItem
        engine.skip((seconds * 1000).roundToLong())
        return Status.success
    }

    /** The repeat button (the car's, or a controller's): the shuffle stays as it is. Episodes ignore it. */
    fun changeRepeatNow(m: RepeatMode): Status {
        val engine = resolve()?.takeIf { it.active == Item.Kind.track } ?: return Status.noActionableNowPlayingItem
        engine.setModes(engine.modes.shuffle, m)
        return Status.success
    }

    /** The shuffle button. Episodes ignore it. */
    fun changeShuffleNow(on: Boolean): Status {
        val engine = resolve()?.takeIf { it.active == Item.Kind.track } ?: return Status.noActionableNowPlayingItem
        engine.setModes(on, engine.modes.repeatMode)
        return Status.success
    }

    /**
     * The notification's own buttons: skip 30 s forward and 15 s back (episodes), shuffle on/off and repeat
     * off → all → one → off (music). An unknown action fails.
     */
    fun custom(action: String): Status = when (action) {
        SKIP_FORWARD_30 -> if (skipEnabled) skipNow(SKIP_FORWARD_S) else Status.noActionableNowPlayingItem
        SKIP_BACK_15 -> if (skipEnabled) skipNow(-SKIP_BACKWARD_S) else Status.noActionableNowPlayingItem
        SHUFFLE -> resolve()?.let { changeShuffleNow(!it.modes.shuffle) } ?: Status.noActionableNowPlayingItem
        REPEAT -> resolve()?.let { changeRepeatNow(nextRepeat(it.modes.repeatMode)) } ?: Status.noActionableNowPlayingItem
        else -> Status.commandFailed
    }

    companion object {
        const val SKIP_FORWARD_S = 30.0
        const val SKIP_BACKWARD_S = 15.0

        /** The notification's custom commands. */
        const val SKIP_FORWARD_30 = "lark.SKIP_FORWARD_30"
        const val SKIP_BACK_15 = "lark.SKIP_BACK_15"
        const val SHUFFLE = "lark.SHUFFLE"
        const val REPEAT = "lark.REPEAT"
        val CUSTOM_ACTIONS = listOf(SKIP_FORWARD_30, SKIP_BACK_15, SHUFFLE, REPEAT)

        private fun weakly(engine: PlayerEngine): () -> PlayerEngine? {
            val ref = WeakReference(engine)
            return { ref.get() }
        }

        fun nextRepeat(m: RepeatMode): RepeatMode = when (m) {
            RepeatMode.off -> RepeatMode.all
            RepeatMode.all -> RepeatMode.one
            RepeatMode.one -> RepeatMode.off
        }

        /**
         * What the play command runs. A cold start (the car, a Bluetooth button with the app not running): the saved
         * queue from disk, its current item from the cache, else cached favorites, all started inside this handler with
         * no network call and no suspension. Only when nothing is on the phone does it ask the server, and then playback
         * is prepared first (`prepareForPlayback`: the media service comes up), before the coroutine.
         */
        fun handlePlay(engine: PlayerEngine, scope: CoroutineScope): Status {
            startPlay(engine, scope)
            return Status.success
        }

        /** [handlePlay]'s steps; the shuffle's coroutine when the server has to be asked, else null. */
        private fun startPlay(engine: PlayerEngine, scope: CoroutineScope): Job? {
            if (engine.isLoaded) {
                engine.play()
            } else if (!engine.resumeFromDisk()) {
                engine.prepareForPlayback()
                return scope.launch { engine.shuffleFavorites() }
            }
            return null
        }
    }
}
