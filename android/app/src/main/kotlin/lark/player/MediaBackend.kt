package lark.player

/**
 * What the backend reports. Every call arrives on the main thread.
 *
 * Every callback carries the `generation` of the `load` it is about. The engine drops any callback whose generation
 * is not the one it loaded last, so a late event from a replaced item (its failure, its end, its pause) can never act
 * on the item that replaced it. The backend stamps each item with the generation it was loaded with and reports
 * that, not the current one; a replaced item is silent.
 */
interface BackendDelegate {
    fun backendStarted(generation: Int)

    /** Only a pause the engine did not ask for (an interruption, a route change). */
    fun backendPaused(generation: Int)

    /**
     * A pause nothing resumes: the output went away (headphones unplugged, a Bluetooth device disconnecting: "audio
     * becoming noisy"), or another app took the audio focus for good. The player has paused; for the engine this is the
     * user's pause (iOS: a route change to `oldDeviceUnavailable`, an interruption ending without `.shouldResume`).
     */
    fun backendPausedForGood(generation: Int)
    fun backendFinished(generation: Int)
    fun backendFailed(network: Boolean, generation: Int)
    fun backendBuffering(on: Boolean, generation: Int)

    /** About twice a second while playing. */
    fun backendTick(positionMs: Long, generation: Int)
}

/**
 * The one audio player. [Media3Backend] implements it on ExoPlayer; engine tests use `FakeBackend`. Driven from the
 * main thread only.
 */
interface MediaBackend {
    var delegate: BackendDelegate?

    /**
     * `generation` increases with every load (a retry of the same item too); callbacks report it back. `gain` is the
     * item's own loudness factor (`Gain.factor`, 0..1), applied to that item alone.
     */
    fun load(src: MediaSource, startMs: Long, autoplay: Boolean, rate: Float, gain: Float, generation: Int)

    /** The next item, for a gapless change, with its own gain, so it starts at its level. `null` clears it. */
    fun preload(src: MediaSource?, gain: Float)

    /**
     * The loudness switch: the loaded item's gain and the preloaded next item's, changed in place (nothing is loaded
     * or preloaded again, so the gapless change stays ready).
     */
    fun setGain(gain: Float, next: Float)

    /** The master volume, for the sleep timer's fade; independent of each item's gain, kept across items. */
    fun setMasterVolume(volume: Float)
    fun play()
    fun pause()
    fun seek(ms: Long)

    /** Episodes keep their pitch at any rate. */
    fun setRate(rate: Float)
    fun stop()
    val positionMs: Long
    val durationMs: Long?
    val isPlaying: Boolean
}
