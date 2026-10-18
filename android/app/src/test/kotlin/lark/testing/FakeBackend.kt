package lark.testing

import lark.player.BackendDelegate
import lark.player.MediaBackend
import lark.player.MediaSource

/** A backend that plays nothing and records what the engine asks of it; tests drive its callbacks by hand. */
class FakeBackend : MediaBackend {
    override var delegate: BackendDelegate? = null
    data class Load(val src: MediaSource, val startMs: Long, val autoplay: Boolean)

    val loads = mutableListOf<Load>()
    val preloads = mutableListOf<MediaSource?>()
    /** The loudness gain of each load and preload, in the same order as `loads` and `preloads`. */
    val gains = mutableListOf<Float>()
    val preloadGains = mutableListOf<Float>()
    /** `setGain` calls (the current item's gain changed in place) and `setMasterVolume` calls. */
    val currentGains = mutableListOf<Float>()
    val nextGains = mutableListOf<Float>()
    val volumes = mutableListOf<Float>()
    val calls = mutableListOf<String>()
    override var positionMs = 0L
    override var durationMs: Long? = null
    override var isPlaying = false
    var lastRate = 1f
    var generation = 0
        private set

    override fun load(src: MediaSource, startMs: Long, autoplay: Boolean, rate: Float, gain: Float, generation: Int) {
        loads += Load(src, startMs, autoplay); positionMs = startMs; isPlaying = false; this.lastRate = rate
        gains += gain
        this.generation = generation
    }
    override fun preload(src: MediaSource?, gain: Float) { preloads += src; preloadGains += gain }
    override fun setGain(gain: Float, next: Float) { currentGains += gain; nextGains += next }
    override fun setMasterVolume(volume: Float) { volumes += volume }
    override fun play() { calls += "play"; isPlaying = true }
    override fun pause() { calls += "pause"; isPlaying = false }
    override fun seek(ms: Long) { calls += "seek:$ms"; positionMs = ms }
    override fun setRate(rate: Float) { calls += "rate:$rate"; this.lastRate = rate }
    override fun stop() { calls += "stop"; isPlaying = false; positionMs = 0 }

    fun start(generation: Int? = null) { isPlaying = true; delegate?.backendStarted(generation ?: this.generation) }
    fun finish(generation: Int? = null) { isPlaying = false; delegate?.backendFinished(generation ?: this.generation) }
    fun fail(network: Boolean, generation: Int? = null) {
        isPlaying = false; delegate?.backendFailed(network, generation ?: this.generation)
    }
    /** A pause the engine did not ask for (an interruption). */
    fun interrupt(generation: Int? = null) { isPlaying = false; delegate?.backendPaused(generation ?: this.generation) }
    /** A pause nothing resumes: the output went away (headphones out), or another app took the audio focus for good. */
    fun pausedForGood(generation: Int? = null) { isPlaying = false; delegate?.backendPausedForGood(generation ?: this.generation) }
    fun buffering(on: Boolean, generation: Int? = null) { delegate?.backendBuffering(on, generation ?: this.generation) }
    /** Plays on to `ms` in 500 ms ticks, as the periodic tick would. */
    fun playTo(ms: Long) {
        while (positionMs + 500 <= ms) { positionMs += 500; delegate?.backendTick(positionMs, generation) }
    }
}
