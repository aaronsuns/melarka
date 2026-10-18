package lark.player

import android.content.Context
import android.os.Handler
import androidx.media3.common.Format
import androidx.media3.common.Timeline
import androidx.media3.common.audio.AudioProcessor
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.Renderer
import androidx.media3.exoplayer.audio.AudioRendererEventListener
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.audio.AudioTrackAudioOutputProvider
import androidx.media3.exoplayer.audio.DefaultAudioSink
import androidx.media3.exoplayer.audio.MediaCodecAudioRenderer
import androidx.media3.exoplayer.mediacodec.MediaCodecAdapter
import androidx.media3.exoplayer.mediacodec.MediaCodecSelector
import androidx.media3.exoplayer.source.MediaSource

/**
 * The default renderers, except that audio goes through [GainProcessor] (first in the sink's chain) and an audio
 * renderer that tells it which item's factor to apply. `tail` and `wrapSink` are for tests: processors behind the gain,
 * and a wrapper around the sink (to back it up the way a full AudioTrack buffer does).
 */
@UnstableApi
class LarkRenderersFactory(
    context: Context,
    private val gain: GainProcessor,
    private val tail: List<AudioProcessor> = emptyList(),
    private val wrapSink: (AudioSink) -> AudioSink = { it },
) : DefaultRenderersFactory(context) {

    override fun buildAudioSink(context: Context, enableFloatOutput: Boolean, enableAudioOutputPlaybackParams: Boolean): AudioSink =
        audioSink(context, enableFloatOutput, enableAudioOutputPlaybackParams)

    /**
     * The sink, PCM only: its output provider has no context, so it assumes the default capabilities (no encoded audio
     * passthrough). An AC-3 or E-AC-3 item is decoded like any other instead of being sent as is to an HDMI or optical
     * output, where [GainProcessor] could not touch it: no item ever bypasses its gain.
     */
    fun audioSink(context: Context, enableFloatOutput: Boolean, enableAudioOutputPlaybackParams: Boolean): AudioSink =
        wrapSink(DefaultAudioSink.Builder(context)
            .setEnableFloatOutput(enableFloatOutput)
            .setEnableAudioOutputPlaybackParameters(enableAudioOutputPlaybackParams)
            .setAudioProcessors((listOf<AudioProcessor>(gain) + tail).toTypedArray())
            .setAudioOutputProvider(AudioTrackAudioOutputProvider.Builder(null).build())
            .build())

    override fun buildAudioRenderers(
        context: Context, extensionRendererMode: Int, mediaCodecSelector: MediaCodecSelector, enableDecoderFallback: Boolean,
        audioSink: AudioSink, eventHandler: Handler, eventListener: AudioRendererEventListener, out: ArrayList<Renderer>,
    ) {
        out.add(LarkAudioRenderer(context, MediaCodecAdapter.Factory.getDefault(context), mediaCodecSelector, enableDecoderFallback,
            eventHandler, eventListener, audioSink, gain))
    }
}

/**
 * Finds the item a stream belongs to (its `GainCell` tag, through the timeline and the stream's period) when the
 * stream is handed to the renderer, and switches [GainProcessor] to it when the renderer's output reaches that
 * stream: `onOutputStreamOffsetUsChanged` runs after the last output buffer of the previous stream was processed
 * and before the first of the new one. That is the audio timeline, not the main thread's `onMediaItemTransition`.
 */
@UnstableApi
private class LarkAudioRenderer(
    context: Context, adapterFactory: MediaCodecAdapter.Factory, selector: MediaCodecSelector, fallback: Boolean,
    handler: Handler, listener: AudioRendererEventListener, sink: AudioSink,
    private val gain: GainProcessor,
) : MediaCodecAudioRenderer(context, adapterFactory, selector, fallback, handler, listener, sink) {
    private val cells = StreamCells(gain)

    override fun onStreamChanged(formats: Array<Format>, startPositionUs: Long, offsetUs: Long, mediaPeriodId: MediaSource.MediaPeriodId) {
        // Before super: the first stream is made the output stream right there, which asks for its cell.
        cells.handedOver(offsetUs, cellOf(getTimeline(), mediaPeriodId))
        super.onStreamChanged(formats, startPositionUs, offsetUs, mediaPeriodId)
    }

    override fun onOutputStreamOffsetUsChanged(outputStreamOffsetUs: Long) {
        super.onOutputStreamOffsetUsChanged(outputStreamOffsetUs)
        cells.reached(outputStreamOffsetUs)
    }

    private fun cellOf(timeline: Timeline, id: MediaSource.MediaPeriodId): GainCell? {
        if (timeline.isEmpty || timeline.getIndexOfPeriod(id.periodUid) < 0) return null
        val window = timeline.getWindow(timeline.getPeriodByUid(id.periodUid, Timeline.Period()).windowIndex, Timeline.Window())
        return window.mediaItem.localConfiguration?.tag as? GainCell
    }
}

/**
 * Stream offset -> the stream's cell, for the streams handed to the renderer and not yet output (offsets are unique per
 * item). A stream whose cell could not be found keeps the previous cell, never full volume, and is counted in
 * [GainProcessor.cellMisses] (a smoke test asserts zero).
 */
@UnstableApi
internal class StreamCells(private val gain: GainProcessor) {
    private val cells = LinkedHashMap<Long, GainCell?>()
    val misses: Int get() = gain.cellMisses.get()

    fun handedOver(offsetUs: Long, cell: GainCell?) {
        cells[offsetUs] = cell
        while (cells.size > 8) cells.remove(cells.keys.first())
    }

    fun reached(offsetUs: Long) {
        val cell = cells[offsetUs]
        if (cell == null) gain.cellMisses.incrementAndGet() else gain.use(cell)
    }
}
