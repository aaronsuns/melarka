package lark.testing

import android.content.Context
import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor
import androidx.media3.common.audio.BaseAudioProcessor
import androidx.media3.exoplayer.DecoderCounters
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.ForwardingAudioSink
import androidx.media3.test.utils.TestExoPlayerBuilder
import androidx.media3.test.utils.robolectric.RobolectricUtil
import lark.player.BackendDelegate
import lark.player.LarkRenderersFactory
import lark.player.Media3Backend
import okhttp3.OkHttpClient
import java.io.File
import java.nio.ByteBuffer
import java.nio.ByteOrder

/** Records every callback with its generation. `onFinished` lets a test answer like the engine does. */
class RecordingDelegate : BackendDelegate {
    val events = mutableListOf<String>()
    val ticks = mutableListOf<Pair<Long, Int>>()
    var onFinished: ((Int) -> Unit)? = null

    override fun backendStarted(generation: Int) { events += "started:$generation" }
    override fun backendPaused(generation: Int) { events += "paused:$generation" }
    override fun backendFinished(generation: Int) { events += "finished:$generation"; onFinished?.invoke(generation) }
    override fun backendFailed(network: Boolean, generation: Int) { events += "failed:$network:$generation" }
    override fun backendBuffering(on: Boolean, generation: Int) { events += "buffering:$on:$generation" }
    override fun backendTick(positionMs: Long, generation: Int) { ticks += positionMs to generation }

    val started get() = events.any { it.startsWith("started:") }
    val finished get() = events.any { it.startsWith("finished:") }
    fun has(e: String) = e in events
}

/** A WAV of `seconds` of mono 16-bit 44.1 kHz audio with one constant sample value: real extractor, no server. */
fun wavBytes(seconds: Double, amplitude: Int): ByteArray {
    val frames = (44_100 * seconds).toInt()
    val data = ByteBuffer.allocate(frames * 2).order(ByteOrder.LITTLE_ENDIAN)
    repeat(frames) { data.putShort(amplitude.toShort()) }
    val out = ByteBuffer.allocate(44 + frames * 2).order(ByteOrder.LITTLE_ENDIAN)
    out.put("RIFF".toByteArray()).putInt(36 + frames * 2).put("WAVE".toByteArray())
    out.put("fmt ".toByteArray()).putInt(16).putShort(1).putShort(1).putInt(44_100).putInt(88_200).putShort(2).putShort(16)
    out.put("data".toByteArray()).putInt(frames * 2).put(data.array())
    return out.array()
}

private var wavCount = 0
fun silentWav(dir: File, seconds: Double, amplitude: Int = 0): File =
    File(dir, "tone-${wavCount++}.wav").also { it.writeBytes(wavBytes(seconds, amplitude)) }

/** Sits behind the gain in the audio chain and notes the level (first sample) of every buffer that comes out. */
class CaptureProcessor : BaseAudioProcessor() {
    val levels = mutableListOf<Int>()
    override fun onConfigure(inputAudioFormat: AudioProcessor.AudioFormat): AudioProcessor.AudioFormat =
        if (inputAudioFormat.encoding == C.ENCODING_PCM_16BIT) inputAudioFormat else AudioProcessor.AudioFormat.NOT_SET

    override fun queueInput(inputBuffer: ByteBuffer) {
        if (!inputBuffer.hasRemaining()) return
        levels += inputBuffer.order(ByteOrder.LITTLE_ENDIAN).getShort(inputBuffer.position()).toInt()
        replaceOutputBuffer(inputBuffer.remaining()).put(inputBuffer).flip()
    }

    /** The audible levels in order, consecutive repeats folded: 8000, 8000, 4000 reads [8000, 4000]. */
    fun steps(): List<Int> = levels.filter { it != 0 }.fold(mutableListOf()) { acc, v -> if (acc.lastOrNull() != v) acc += v; acc }
}

/**
 * A sink that refuses each buffer a few times before it takes it, as an AudioTrack with a full buffer does: the renderer
 * then still holds the last buffers of an item when the next item's stream is handed to it.
 */
class BackedUpSink(sink: AudioSink, private val refusals: Int = 3) : ForwardingAudioSink(sink) {
    private var buffer: ByteBuffer? = null
    private var refused = 0
    override fun handleBuffer(buffer: ByteBuffer, presentationTimeUs: Long, encodedAccessUnitCount: Int): Boolean {
        if (buffer !== this.buffer) { this.buffer = buffer; refused = 0 }
        if (refused++ < refusals) return false
        return super.handleBuffer(buffer, presentationTimeUs, encodedAccessUnitCount)
    }
}

/** A [Media3Backend] on a real ExoPlayer from `TestExoPlayerBuilder` (fake clock), with the capture behind the gain. */
class BackendRig(context: Context, val scheduler: FakeScheduler = FakeScheduler(), http: OkHttpClient = OkHttpClient(), backedUp: Boolean = false) {
    val capture = CaptureProcessor()
    val delegate = RecordingDelegate()
    lateinit var player: ExoPlayer
        private set
    /** How often the audio renderer was enabled: a gapless change keeps it, a fresh load enables it again. */
    var audioEnabled = 0
        private set
    val backend: Media3Backend = Media3Backend(scheduler, http) { gain ->
        TestExoPlayerBuilder(context).setRenderersFactory(LarkRenderersFactory(context, gain, listOf(capture)) { if (backedUp) BackedUpSink(it) else it }).build().also {
            player = it
            it.addAnalyticsListener(object : AnalyticsListener {
                override fun onAudioEnabled(eventTime: AnalyticsListener.EventTime, decoderCounters: DecoderCounters) { audioEnabled++ }
            })
        }
    }.also { it.delegate = delegate }

    fun waitUntil(condition: () -> Boolean) = RobolectricUtil.runMainLooperUntil { condition() }

    fun release() { backend.stop(); player.release() }
}
