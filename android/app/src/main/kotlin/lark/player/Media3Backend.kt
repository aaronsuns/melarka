package lark.player

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.Timeline
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.FileDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlaybackException
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.extractor.DefaultExtractorsFactory
import lark.core.Cancellable
import lark.core.Scheduler
import okhttp3.OkHttpClient
import java.net.SocketException
import java.net.SocketTimeoutException
import java.net.UnknownHostException

/**
 * The one audio player, on ExoPlayer: a playlist of the current item plus the preloaded next one, so a change at the
 * end of a track is gapless. Driven from the main thread (ExoPlayer's application thread), which is also where the
 * [BackendDelegate] hears from it.
 *
 * - [BackendDelegate.backendTick] comes from a 0.5 s timer on the scheduler while playing.
 * - `isPlaying` is `backendStarted`, a buffering state while the engine wants playback is `backendBuffering`, and a
 *   pause the engine did not ask for is `backendPaused`: headphones unplugged, a permanent focus loss or a media-session
 *   controller (playWhenReady false), and a transient focus loss, a call (playback suppressed). After the latter ExoPlayer
 *   resumes by itself, and the engine hears `backendStarted` again.
 * - A failed item reports `network: true` only for a stream and only for a connection-level failure; a file, and a
 *   server that answers with an error status, never.
 * - Gapless: when the player moves to the preloaded item by itself, `backendFinished` tells the engine, whose `load` of
 *   that same source takes the item over instead of loading it again (the playlist is not touched, nothing is decoded
 *   twice). If the engine loads something else, or nothing, the preloaded item is removed before it can play on.
 * - Streams send the token as an `Authorization` header from the item's own data source, never in the URL; neither the
 *   `MediaItem` nor its tag holds it, and nothing here logs a source.
 * - Loudness: each item carries its [GainCell] as the `MediaItem` tag; [GainProcessor] applies it on the audio timeline,
 *   at the item boundary (see [LarkAudioRenderer]), so a preloaded item starts at its own level with nothing to do at
 *   the change and no item ever sounds before its gain is on. `player.volume` is the master volume, which only the
 *   sleep timer's fade moves.
 * - Episodes keep their pitch at any rate (`PlaybackParameters(rate, 1f)`).
 */
@UnstableApi
class Media3Backend(
    scheduler: Scheduler,
    private val http: OkHttpClient,
    playerFactory: (GainProcessor) -> ExoPlayer,
) : MediaBackend {
    override var delegate: BackendDelegate? = null

    /** For the media session (Task 8); everything that plays goes through this class. */
    private val gain = GainProcessor()
    val player: ExoPlayer = playerFactory(gain)

    /** Streams whose item's gain could not be found (they kept the previous factor). Always 0; a smoke test asserts it. */
    val gainMisses: Int get() = gain.cellMisses.get()

    /** Preloaded items taken over by a `load` (tests). */
    var adoptedPreloads = 0
        private set

    private class Slot(val id: String, val src: MediaSource, val cell: GainCell) {
        val mediaItem: MediaItem = MediaItem.Builder().setUri(src.uri()).setMediaId(id).setTag(cell).build()
        /** The generation of the `load` it was loaded with; null while it is only preloaded. */
        var generation: Int? = null
        var ended = false
        var failed = false
    }

    private val slots = HashMap<String, Slot>()
    private var nextSlot = 0
    private var preloaded: Slot? = null
    /** The engine wants it playing: a pause while this is true was not asked for. */
    private var wantsPlay = false
    private var reportedStarted: Int? = null
    private var reportedBuffering: Int? = null
    private val main = Handler(Looper.getMainLooper())
    private val tick: Cancellable

    private val listener = object : Player.Listener {
        override fun onIsPlayingChanged(isPlaying: Boolean) {
            val g = current()?.generation ?: return
            if (isPlaying) {
                reportedBuffering = null
                if (reportedStarted != g) { reportedStarted = g; delegate?.backendStarted(g) }
            } else {
                reportedStarted = null
            }
        }

        override fun onPlaybackStateChanged(playbackState: Int) {
            val slot = current() ?: return
            val g = slot.generation ?: return
            when (playbackState) {
                Player.STATE_BUFFERING -> if (wantsPlay && reportedBuffering != g) { reportedBuffering = g; delegate?.backendBuffering(true, g) }
                Player.STATE_ENDED -> ended(slot)
                else -> {}
            }
        }

        /**
         * A phone call or another app's transient claim: ExoPlayer keeps playWhenReady (and resumes by itself when the focus
         * returns, which is then an `isPlaying` change and so a `backendStarted`), so the pause is told from here.
         * `wantsPlay` stays true: this is not a pause the engine asked for, and not one that cancels playback.
         */
        override fun onPlaybackSuppressionReasonChanged(playbackSuppressionReason: Int) {
            if (playbackSuppressionReason != Player.PLAYBACK_SUPPRESSION_REASON_TRANSIENT_AUDIO_FOCUS_LOSS || !wantsPlay) return
            val g = current()?.generation ?: return
            reportedStarted = null
            delegate?.backendPaused(g)
        }

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            if (playWhenReady) { wantsPlay = true; return }      // the system resumed it after a transient loss
            if (player.playWhenReady) return                     // a stale event: our own pause-seek-play (an adopt) already went on
            val g = current()?.generation ?: return
            if (!wantsPlay) return                               // a pause the engine asked for
            wantsPlay = false
            reportedStarted = null
            delegate?.backendPaused(g)
        }

        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            if (reason == Player.MEDIA_ITEM_TRANSITION_REASON_AUTO) advanced()
        }

        override fun onPlayerError(error: PlaybackException) = failed(error)
    }

    init {
        player.addListener(listener)
        tick = scheduler.every(TICK_S) { tick() }
    }

    // MARK: - MediaBackend

    override fun load(src: MediaSource, startMs: Long, autoplay: Boolean, rate: Float, gain: Float, generation: Int) {
        wantsPlay = autoplay
        setRate(rate)
        reportedStarted = null; reportedBuffering = null
        val p = preloaded
        if (p != null && p.src == src && p.generation == null && !p.failed && indexOf(p) >= 0) {
            p.cell.factor = gain         // preloaded with this gain already: unchanged
            adopt(p, startMs, autoplay, generation)
            return
        }
        discardAll()
        val slot = newSlot(src, gain)
        slot.generation = generation
        player.setMediaSource(mediaSourceFor(slot), startMs.coerceAtLeast(0))
        player.playWhenReady = autoplay
        player.prepare()
    }

    override fun preload(src: MediaSource?, gain: Float) {
        preloaded?.let { old ->
            val i = indexOf(old)
            if (i >= 0 && i != player.currentMediaItemIndex) player.removeMediaItem(i)
            slots.remove(old.id)
            preloaded = null
        }
        val cur = current()
        if (src == null || cur == null || cur.generation == null) return
        val slot = newSlot(src, gain)
        player.addMediaSource(mediaSourceFor(slot))
        preloaded = slot
    }

    override fun setGain(gain: Float, next: Float) {
        current()?.cell?.factor = gain
        preloaded?.takeIf { it.generation == null }?.cell?.factor = next
    }

    override fun setMasterVolume(volume: Float) { player.volume = volume.coerceIn(0f, 1f) }

    override fun play() {
        wantsPlay = true
        if (current() != null) player.play()
    }

    override fun pause() {
        wantsPlay = false
        player.pause()
    }

    override fun seek(ms: Long) {
        if (current() == null) return
        player.seekTo(ms.coerceAtLeast(0))
    }

    override fun setRate(rate: Float) { player.playbackParameters = PlaybackParameters(rate, 1f) }

    override fun stop() {
        wantsPlay = false
        reportedStarted = null; reportedBuffering = null
        discardAll()
    }

    override val positionMs: Long get() = if (current() == null) 0 else player.currentPosition.coerceAtLeast(0)

    override val durationMs: Long?
        get() = if (current() == null) null else player.duration.takeIf { it != C.TIME_UNSET && it > 0 }

    override val isPlaying: Boolean get() = player.isPlaying && current() != null

    /** Stops the timer and the player for good (the service's end). */
    fun release() {
        tick.cancel()
        main.removeCallbacksAndMessages(null)
        delegate = null
        player.removeListener(listener)
        player.release()
    }

    // MARK: - Items

    private fun newSlot(src: MediaSource, gain: Float): Slot =
        Slot("lark-${nextSlot++}", src, GainCell(gain)).also { slots[it.id] = it }

    private fun mediaSourceFor(slot: Slot) =
        ProgressiveMediaSource.Factory(dataSourceFactory(slot.src, http), DefaultExtractorsFactory().setConstantBitrateSeekingEnabled(true))
            .createMediaSource(slot.mediaItem)

    private fun slotOf(item: MediaItem?): Slot? = item?.let { slots[it.mediaId] }

    private fun indexOf(slot: Slot): Int = (0 until player.mediaItemCount).firstOrNull { player.getMediaItemAt(it).mediaId == slot.id } ?: -1

    /** The current item, if it is ours and loaded by the engine or at least ours (a preloaded one it has not taken over has no generation). */
    private fun current(): Slot? = slotOf(player.currentMediaItem)

    private fun discardAll() {
        player.stop()
        player.clearMediaItems()
        player.playWhenReady = false         // nothing is current any more, so this is not reported
        slots.clear()
        preloaded = null
    }

    /** The engine loads what was preloaded: the same item goes on (it may already be playing, gapless). */
    private fun adopt(p: Slot, startMs: Long, autoplay: Boolean, generation: Int) {
        preloaded = null
        val i = indexOf(p)
        if (i > 0) {                                 // a fast skip: everything before it goes, it becomes the current item
            for (k in i - 1 downTo 0) slots.remove(player.getMediaItemAt(k).mediaId)
            player.removeMediaItems(0, i)
        }
        p.generation = generation
        adoptedPreloads++
        if (startMs > 0) {
            // It may already be playing from 0: silent until the resume position is reached.
            player.pause()
            player.seekTo(startMs)
        }
        player.playWhenReady = autoplay
        // Already playing: no state change will come, so the start is reported from here (after this call returns).
        main.post {
            if (current() === p && p.generation == generation && player.isPlaying && reportedStarted != generation) {
                reportedStarted = generation
                delegate?.backendStarted(generation)
            }
        }
    }

    // MARK: - Events

    private fun tick() {
        val slot = current() ?: return
        val g = slot.generation ?: return
        if (player.isPlaying) delegate?.backendTick(positionMs, g)
    }

    /** The player moved on to the preloaded item by itself: the item before it played to its end. */
    private fun advanced() {
        val now = current()
        val finished = slots.values.firstOrNull { it !== now && it.generation != null && !it.ended && indexOf(it) in 0 until player.currentMediaItemIndex }
        if (finished != null) ended(finished)
        pruneBeforeCurrent()
        // The engine did not take the preloaded item over: it must not play on by itself.
        val next = current()
        if (next != null && next.generation == null && next === preloaded) {
            wantsPlay = false
            discardAll()
        }
    }

    private fun pruneBeforeCurrent() {
        val c = player.currentMediaItemIndex
        if (c <= 0) return
        for (k in 0 until c) slots.remove(player.getMediaItemAt(k).mediaId)
        player.removeMediaItems(0, c)
    }

    private fun ended(slot: Slot) {
        if (slot.ended) return
        slot.ended = true
        val g = slot.generation ?: return
        delegate?.backendFinished(g)
    }

    private fun failed(error: PlaybackException) {
        val slot = slotOfError(error) ?: current() ?: return
        if (slot.failed) return
        slot.failed = true
        val g = slot.generation
        if (g == null) {
            // A preloaded item that cannot be read: the engine's load sees it failed. It must not stop the one playing.
            val i = indexOf(slot)
            if (i >= 0 && i != player.currentMediaItemIndex) player.removeMediaItem(i)
            slots.remove(slot.id)
            if (preloaded === slot) preloaded = null
            player.prepare()
            return
        }
        reportedStarted = null
        delegate?.backendFailed(slot.src is MediaSource.Remote && isNetworkError(error), g)
    }

    private fun slotOfError(error: PlaybackException): Slot? {
        val uid = (error as? ExoPlaybackException)?.mediaPeriodId?.periodUid ?: return null
        val timeline = player.currentTimeline
        if (timeline.isEmpty || timeline.getIndexOfPeriod(uid) < 0) return null
        val window = timeline.getWindow(timeline.getPeriodByUid(uid, Timeline.Period()).windowIndex, Timeline.Window())
        return slotOf(window.mediaItem)
    }

    companion object {
        private const val TICK_S = 0.5

        /** The production player: music audio attributes with audio focus, pause when the headphones are unplugged, a wake lock while streaming. */
        fun create(context: Context, scheduler: Scheduler, http: OkHttpClient): Media3Backend = Media3Backend(scheduler, http) { gain ->
            ExoPlayer.Builder(context, LarkRenderersFactory(context, gain))
                .setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(), true)
                .setHandleAudioBecomingNoisy(true)
                .setWakeMode(C.WAKE_MODE_NETWORK)
                .build()
        }

        /** A stream sends `Authorization: Bearer <token>` with every request; a file needs nothing. The token is in no URL. */
        internal fun dataSourceFactory(src: MediaSource, http: OkHttpClient): DataSource.Factory = when (src) {
            is MediaSource.File -> FileDataSource.Factory()
            is MediaSource.Remote -> OkHttpDataSource.Factory(http).setDefaultRequestProperties(mapOf("Authorization" to "Bearer ${src.token}"))
        }

        /**
         * A connection-level failure, as an `NSURLErrorDomain` error in iOS's chain: no connection, a timeout, an unknown
         * host, and any `HttpDataSourceException` in the cause chain (a drop in the middle of a read, a reset stream, an
         * EOF) except a server's error status and a refused cleartext request, which are the server's or the app's answer.
         */
        internal fun isNetworkError(e: PlaybackException): Boolean {
            if (e.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED ||
                e.errorCode == PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT) return true
            val chain = generateSequence<Throwable>(e) { it.cause }.toList()
            if (chain.any { it is HttpDataSource.InvalidResponseCodeException || it is HttpDataSource.CleartextNotPermittedException }) return false
            return chain.any { it is UnknownHostException || it is SocketException || it is SocketTimeoutException || it is HttpDataSource.HttpDataSourceException }
        }

        private fun MediaSource.uri(): Uri = when (this) {
            is MediaSource.File -> Uri.fromFile(file)
            is MediaSource.Remote -> uri
        }
    }
}
