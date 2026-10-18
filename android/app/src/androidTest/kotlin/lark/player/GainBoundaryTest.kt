package lark.player

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor
import androidx.media3.common.audio.BaseAudioProcessor
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.test.ext.junit.runners.AndroidJUnit4
import lark.Instrumented
import lark.Instrumented.onMain
import lark.core.MainScheduler
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.abs

/**
 * The loudness gain at the item boundary on a real decoder and audio sink: an MP3 at half volume and the preloaded AAC
 * after it at a quarter, played through without a gap. Every decoded buffer is measured behind [GainProcessor]: none is
 * louder than its own item's level (no full-volume blip at the change), the AAC never goes back up to the MP3's level,
 * and no stream ever missed its item's gain (`gainMisses` stays 0).
 *
 * The tones (web-free test assets, `src/androidTest/assets/gain`) are 3 s sines at 0.8 of full scale, made with
 * `ffmpeg -f lavfi -i "aevalsrc=0.8*sin(2*PI*440*t):s=44100:d=3" -ac 1 -c:a libmp3lame -b:a 96k tone-440-3s.mp3` and
 * `… 660 … -c:a aac … tone-660-3s.m4a`.
 */
@UnstableApi
@RunWith(AndroidJUnit4::class)
class GainBoundaryTest {
    private var backend: Media3Backend? = null

    @After fun tearDown() { onMain { backend?.release() } }

    private fun asset(name: String): File {
        val out = File(Instrumented.context.cacheDir, name)
        Instrumented.instrumentation.context.assets.open("gain/$name").use { input -> out.outputStream().use { input.copyTo(it) } }
        return out
    }

    @Test fun eachItemPlaysAtItsOwnGainAcrossAGaplessMp3ToAacChange() {
        val mp3 = MediaSource.File(asset("tone-440-3s.mp3"))
        val aac = MediaSource.File(asset("tone-660-3s.m4a"))
        val probe = PeakProbe()
        val events = Events()
        val b = onMain {
            Media3Backend(MainScheduler(), OkHttpClient()) { gain ->
                ExoPlayer.Builder(Instrumented.context, LarkRenderersFactory(Instrumented.context, gain, tail = listOf(probe))).build()
                    .also(Media3Backend::configure)
                    // No audio focus: Android 15 refuses it to a process with no activity on top or foreground
                    // service, as this test's is (the app's has its media service). Gain is what is tested here.
                    .also { it.setAudioAttributes(it.audioAttributes, false) }
            }.also { it.delegate = events; backend = it }
        }

        onMain { b.load(mp3, 0, autoplay = true, rate = 1f, gain = MP3_GAIN, generation = 1) }
        Instrumented.waitFor("the MP3 plays") { events.has("started 1") }
        // As the engine does: the MP3's end loads the next item, there and then, which takes the preloaded one over
        // (it is already playing; nothing is loaded again).
        events.onFinished = { g -> if (g == 1) b.load(aac, 0, autoplay = true, rate = 1f, gain = AAC_GAIN, generation = 2) }
        onMain { b.preload(aac, AAC_GAIN) }
        Instrumented.waitFor("the MP3 ends", 15_000) { events.has("finished 1") }
        Instrumented.waitFor("the AAC ends", 15_000) { events.has("finished 2") }

        assertEquals("the AAC was the preloaded item, played on without a gap", 1, onMain { b.adoptedPreloads })
        assertEquals("every stream found its item's gain", 0, onMain { b.gainMisses })
        assertTrue("no failure: ${events.all()}", events.all().none { it.startsWith("failed") })

        val buffers = probe.buffers()
        val loud = FULL * MP3_GAIN
        val quiet = FULL * AAC_GAIN
        val max = buffers.maxOfOrNull { it.peak } ?: 0f
        assertTrue("some sound was measured (${buffers.size} buffers)", buffers.size > 20)
        assertTrue("no buffer louder than the MP3's level (max $max, the MP3's $loud)", max <= loud * TOLERANCE)
        // The MP3's last buffer at its level; everything after it is the AAC, all at the AAC's level from its first
        // sample: had the change kept the MP3's gain for a moment, those buffers would be at the MP3's level, and the
        // quiet part after the last loud buffer shorter than the whole AAC.
        val lastLoud = buffers.indexOfLast { it.peak >= loud / TOLERANCE }
        assertTrue("the MP3 played at its level (max $max)", lastLoud >= 0)
        val after = buffers.drop(lastLoud + 1)
        assertTrue("nothing after the change is louder than the AAC's level: ${after.filter { it.peak > quiet * TOLERANCE }}",
            after.all { it.peak <= quiet * TOLERANCE })
        assertTrue("the AAC played at its level", (after.maxOfOrNull { it.peak } ?: 0f) >= quiet / TOLERANCE)
        val quietSeconds = after.sumOf { it.frames }.toDouble() / SAMPLE_RATE
        assertTrue("the whole AAC (3 s) played at its level, from its first buffer: $quietSeconds s", quietSeconds >= 2.85)
    }

    /** What the backend reported, in order (the player's thread is the main thread). */
    private class Events : BackendDelegate {
        private val list = mutableListOf<String>()
        /** Runs in the backend's `backendFinished` (main thread). */
        @Volatile var onFinished: ((Int) -> Unit)? = null
        @Synchronized fun all() = list.toList()
        @Synchronized fun has(e: String) = e in list
        @Synchronized private fun add(e: String) { list += e }
        override fun backendStarted(generation: Int) = add("started $generation")
        override fun backendPaused(generation: Int) = add("paused $generation")
        override fun backendPausedForGood(generation: Int) = add("pausedForGood $generation")
        override fun backendFinished(generation: Int) {
            add("finished $generation")
            onFinished?.invoke(generation)
        }
        override fun backendFailed(network: Boolean, generation: Int) = add("failed $generation")
        override fun backendBuffering(on: Boolean, generation: Int) {}
        override fun backendTick(positionMs: Long, generation: Int) {}
    }

    /** One buffer behind the gain: its peak (0..1 of full scale) and its length in frames. */
    private data class Buffer(val peak: Float, val frames: Int)

    /** Behind the gain: each buffer's peak and length, passed through unchanged. Playback thread. */
    private class PeakProbe : BaseAudioProcessor() {
        private val buffers = mutableListOf<Buffer>()
        @Synchronized fun buffers() = buffers.toList()

        override fun onConfigure(inputAudioFormat: AudioProcessor.AudioFormat): AudioProcessor.AudioFormat = when (inputAudioFormat.encoding) {
            C.ENCODING_PCM_16BIT, C.ENCODING_PCM_FLOAT -> inputAudioFormat
            else -> throw AudioProcessor.UnhandledAudioFormatException(inputAudioFormat)
        }

        override fun queueInput(inputBuffer: ByteBuffer) {
            val size = inputBuffer.remaining()
            if (size == 0) return
            val read = inputBuffer.duplicate().order(ByteOrder.nativeOrder())
            var peak = 0f
            if (inputAudioFormat.encoding == C.ENCODING_PCM_16BIT) {
                while (read.remaining() >= 2) peak = maxOf(peak, abs(read.short.toFloat()) / 32768f)
            } else {
                while (read.remaining() >= 4) peak = maxOf(peak, abs(read.float))
            }
            synchronized(this) { buffers += Buffer(peak, size / inputAudioFormat.bytesPerFrame) }
            replaceOutputBuffer(size).put(inputBuffer).flip()
        }
    }

    private companion object {
        const val FULL = 0.8f            // the tones' amplitude
        const val MP3_GAIN = 0.5f
        const val AAC_GAIN = 0.25f
        /** Codec ripple (MP3 and AAC overshoot a pure sine slightly). */
        const val TOLERANCE = 1.12f
        const val SAMPLE_RATE = 44_100.0
    }
}
