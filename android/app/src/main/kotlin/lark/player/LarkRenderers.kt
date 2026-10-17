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
        wrapSink(DefaultAudioSink.Builder(context)
            .setEnableFloatOutput(enableFloatOutput)
            .setEnableAudioOutputPlaybackParameters(enableAudioOutputPlaybackParams)
            .setAudioProcessors((listOf<AudioProcessor>(gain) + tail).toTypedArray())
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
    /** Stream offset -> the stream's cell, for the streams handed over and not yet output. Offsets are unique per item. */
    private val cells = LinkedHashMap<Long, GainCell?>()

    override fun onStreamChanged(formats: Array<Format>, startPositionUs: Long, offsetUs: Long, mediaPeriodId: MediaSource.MediaPeriodId) {
        // Before super: the first stream is made the output stream right there, which asks for its cell.
        cells[offsetUs] = cellOf(getTimeline(), mediaPeriodId)
        while (cells.size > 8) cells.remove(cells.keys.first())
        super.onStreamChanged(formats, startPositionUs, offsetUs, mediaPeriodId)
    }

    override fun onOutputStreamOffsetUsChanged(outputStreamOffsetUs: Long) {
        super.onOutputStreamOffsetUsChanged(outputStreamOffsetUs)
        gain.use(cells[outputStreamOffsetUs])
    }

    private fun cellOf(timeline: Timeline, id: MediaSource.MediaPeriodId): GainCell? {
        if (timeline.isEmpty || timeline.getIndexOfPeriod(id.periodUid) < 0) return null
        val window = timeline.getWindow(timeline.getPeriodByUid(id.periodUid, Timeline.Period()).windowIndex, Timeline.Window())
        return window.mediaItem.localConfiguration?.tag as? GainCell
    }
}
