package lark.player

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Item
import org.junit.Assert.assertEquals
import org.junit.Test
import java.nio.ByteBuffer
import java.nio.ByteOrder

/** The loudness gain of a music item: the server's `gain_db` as a volume factor, attenuation only. */
class GainTest {
    private fun music(gain: JsonElement?): Item {
        val meta = linkedMapOf<String, JsonElement>("id" to JsonPrimitive(7))
        if (gain != null) meta["gain_db"] = gain
        return Item(Item.Kind.track, "7", "T", "A", "B", 1000, JsonObject(meta))
    }

    @Test fun minusSixDecibelsIsAboutHalf() {
        assertEquals(0.501f, Gain.factor(music(JsonPrimitive(-6.0)), true), 0.001f)
        assertEquals(0.501f, Gain.factor(music(JsonPrimitive(-6)), true), 0.001f)   // a whole number on the wire
        assertEquals(0.668f, Gain.factor(music(JsonPrimitive(-3.5)), true), 0.001f)
    }

    @Test fun unmeasuredIsUnity() {
        assertEquals(1f, Gain.factor(music(null), true), 0f)
        assertEquals(1f, Gain.factor(music(JsonNull), true), 0f)
        assertEquals(1f, Gain.factor(music(JsonPrimitive("-6")), true), 0f)
        assertEquals(1f, Gain.factor(Item(Item.Kind.track, "7"), true), 0f)          // no meta at all
    }

    @Test fun neverABoost() {
        assertEquals(1f, Gain.factor(music(JsonPrimitive(3)), true), 0f)
        assertEquals(1f, Gain.factor(music(JsonPrimitive(0.0)), true), 0f)
    }

    @Test fun switchedOffIsUnity() {
        assertEquals(1f, Gain.factor(music(JsonPrimitive(-6)), false), 0f)
    }

    @Test fun episodesAreNeverAttenuated() {
        val ep = Item(Item.Kind.episode, "v1", "E", "C", "频道", 1000,
            JsonObject(mapOf("video_id" to JsonPrimitive("v1"), "gain_db" to JsonPrimitive(-6))))
        assertEquals(1f, Gain.factor(ep, true), 0f)
    }

    // -- the processor's arithmetic (which item's factor it uses is the backend test's business)

    private fun processor(encoding: Int, factor: Float): GainProcessor {
        val p = GainProcessor()
        p.configure(AudioProcessor.AudioFormat(44_100, 1, encoding))
        p.flush(AudioProcessor.StreamMetadata.DEFAULT)
        p.use(GainCell(factor))
        return p
    }

    private fun run(p: GainProcessor, input: ByteBuffer): ByteBuffer {
        p.queueInput(input)
        return p.output.order(ByteOrder.LITTLE_ENDIAN)
    }

    @Test fun scalesSixteenBitSamples() {
        val inB = ByteBuffer.allocate(6).order(ByteOrder.LITTLE_ENDIAN).putShort(10000).putShort(-10000).putShort(32767).flipped()
        val out = run(processor(C.ENCODING_PCM_16BIT, 0.5f), inB)
        assertEquals(listOf<Short>(5000, -5000, 16384), listOf(out.short, out.short, out.short))
    }

    @Test fun scalesFloatSamples() {
        val inB = ByteBuffer.allocate(8).order(ByteOrder.LITTLE_ENDIAN).putFloat(0.8f).putFloat(-0.4f).flipped()
        val out = run(processor(C.ENCODING_PCM_FLOAT, 0.25f), inB)
        assertEquals(0.2f, out.float, 1e-6f)
        assertEquals(-0.1f, out.float, 1e-6f)
    }

    @Test fun scalesTwentyFourAndThirtyTwoBitSamples() {
        val v24 = 4_000_000
        val b24 = ByteBuffer.allocate(3).order(ByteOrder.LITTLE_ENDIAN).put(v24.toByte()).put((v24 shr 8).toByte()).put((v24 shr 16).toByte()).flipped()
        val o24 = run(processor(C.ENCODING_PCM_24BIT, 0.5f), b24)
        assertEquals(2_000_000, (o24.get().toInt() and 0xFF) or ((o24.get().toInt() and 0xFF) shl 8) or (o24.get().toInt() shl 16))
        val b32 = ByteBuffer.allocate(4).order(ByteOrder.LITTLE_ENDIAN).putInt(-1_000_000_000).flipped()
        assertEquals(-250_000_000, run(processor(C.ENCODING_PCM_32BIT, 0.25f), b32).int)
    }

    @Test fun unityLeavesTheSamplesAlone() {
        val inB = ByteBuffer.allocate(4).order(ByteOrder.LITTLE_ENDIAN).putShort(1234).putShort(-4321).flipped()
        val out = run(processor(C.ENCODING_PCM_16BIT, 1f), inB)
        assertEquals(1234.toShort(), out.short)
        assertEquals((-4321).toShort(), out.short)
    }
}

private fun ByteBuffer.flipped(): ByteBuffer { flip(); return this }
