package lark.player

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor
import androidx.media3.common.audio.BaseAudioProcessor
import androidx.media3.common.util.UnstableApi
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.concurrent.atomic.AtomicInteger
import kotlin.math.roundToInt

/**
 * One item's loudness factor (0..1). It rides on the item's `MediaItem` as its tag, so the audio renderer finds it from
 * the player's timeline; it holds nothing else (never the token).
 */
class GainCell(factor: Float) {
    /** Written on the main thread (a load, the loudness switch), read on the playback thread. */
    @Volatile var factor: Float = factor.coerceIn(0f, 1f)
}

/**
 * Scales the decoded audio by the factor of the item it belongs to. The audio renderer ([LarkAudioRenderer]) calls
 * [use] on the playback thread at the exact point the output moves from one item to the next: after the last buffer
 * of the old item went into this processor, before the first buffer of the new one does. The sink hands each buffer
 * to `queueInput` in the `handleBuffer` call that accepts it, on the same thread, so a buffer is scaled with the
 * factor of its own item, not of whichever item is current on the main thread, and an attenuated item has no
 * unattenuated sample.
 */
@UnstableApi
class GainProcessor : BaseAudioProcessor() {
    private var cell: GainCell? = null

    /** Streams whose item could not be found (they kept the previous item's factor). Should stay 0. */
    val cellMisses = AtomicInteger()

    /** Playback thread only. */
    fun use(cell: GainCell?) { this.cell = cell }

    override fun onConfigure(inputAudioFormat: AudioProcessor.AudioFormat): AudioProcessor.AudioFormat = when (inputAudioFormat.encoding) {
        C.ENCODING_PCM_16BIT, C.ENCODING_PCM_24BIT, C.ENCODING_PCM_32BIT, C.ENCODING_PCM_FLOAT -> inputAudioFormat
        else -> AudioProcessor.AudioFormat.NOT_SET           // not PCM: inactive (the sink never offers it anyway)
    }

    override fun queueInput(inputBuffer: ByteBuffer) {
        val size = inputBuffer.remaining()
        if (size == 0) return
        inputBuffer.order(ByteOrder.nativeOrder())            // what the sink hands over; every path below reads and writes native order
        val out = replaceOutputBuffer(size)
        val f = cell?.factor ?: 1f
        if (f >= 1f) {
            out.put(inputBuffer)
        } else {
            when (inputAudioFormat.encoding) {
                C.ENCODING_PCM_16BIT -> while (inputBuffer.hasRemaining()) out.putShort((inputBuffer.short * f).roundToInt().toShort())
                C.ENCODING_PCM_FLOAT -> while (inputBuffer.hasRemaining()) out.putFloat(inputBuffer.float * f)
                C.ENCODING_PCM_32BIT -> while (inputBuffer.hasRemaining()) out.putInt((inputBuffer.int * f.toDouble()).roundToInt())
                else -> {                                                 // 24 bit, three bytes in native order
                    val little = ByteOrder.nativeOrder() == ByteOrder.LITTLE_ENDIAN
                    while (inputBuffer.hasRemaining()) {
                        val b0 = inputBuffer.get().toInt() and 0xFF; val b1 = inputBuffer.get().toInt() and 0xFF; val b2 = inputBuffer.get().toInt()
                        val s = if (little) b0 or (b1 shl 8) or (b2 shl 16) else (b0 shl 16) or (b1 shl 8) or (b2 and 0xFF)
                        val v = (s * f).roundToInt()
                        if (little) out.put(v.toByte()).put((v shr 8).toByte()).put((v shr 16).toByte())
                        else out.put((v shr 16).toByte()).put((v shr 8).toByte()).put(v.toByte())
                    }
                }
            }
        }
        out.flip()
    }
}
