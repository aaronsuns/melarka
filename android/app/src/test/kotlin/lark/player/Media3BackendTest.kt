package lark.player

import android.net.Uri
import android.media.AudioManager
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.datasource.HttpDataSource
import org.robolectric.Shadows.shadowOf
import androidx.media3.datasource.DataSpec
import androidx.test.core.app.ApplicationProvider
import lark.testing.BackendRig
import lark.testing.silentWav
import lark.testing.wavBytes
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

/** Port of `AVPlayerBackendTests` and the backend half of `GaplessTests`, on a real ExoPlayer with a fake clock. */
// API 30: from 31 on ExoPlayer asks for the media_metrics service, which Robolectric does not publish (a stack trace per test on stderr).
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
class Media3BackendTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var rig: BackendRig
    private val backend get() = rig.backend
    private val d get() = rig.delegate
    private val player get() = rig.player

    @Before fun setUp() { rig = BackendRig(ApplicationProvider.getApplicationContext()) }
    @After fun tearDown() { rig.release() }

    /** A rig whose sink is backed up (replaces the default one; the test's `@After` releases whichever is current). */
    private fun backedUpRig() { rig.release(); rig = BackendRig(ApplicationProvider.getApplicationContext(), backedUp = true) }

    private fun tone(seconds: Double, amplitude: Int = 8000) = MediaSource.File(silentWav(tmp.root, seconds, amplitude))
    private fun load(src: MediaSource, g: Int, startMs: Long = 0, autoplay: Boolean = true, rate: Float = 1f, gain: Float = 1f) =
        backend.load(src, startMs, autoplay, rate, gain, g)
    private fun cell(index: Int) = player.getMediaItemAt(index).localConfiguration?.tag as GainCell

    @Test fun playsGeneratedFileToTheEnd() {
        load(tone(1.0), 1)
        rig.waitUntil { d.started }
        rig.scheduler.advance(0.5)                        // the tick timer is the scheduler's
        assertTrue(d.ticks.toString(), d.ticks.isNotEmpty() && d.ticks.all { it.second == 1 })
        rig.waitUntil { d.finished }
        assertTrue(d.events.toString(), d.has("started:1"))
        assertTrue(d.events.toString(), d.has("finished:1"))
        assertFalse(d.events.toString(), d.events.any { it.startsWith("paused:") || it.startsWith("failed:") })
        val n = d.ticks.size
        rig.scheduler.advance(1.0)
        assertEquals("no ticks once the item has ended", n, d.ticks.size)
    }

    @Test fun missingFileReportsFailureNotNetwork() {
        load(MediaSource.File(File(tmp.root, "missing.wav")), 4)
        rig.waitUntil { d.events.any { it.startsWith("failed:") } }
        assertEquals(listOf("failed:false:4"), d.events.filter { it.startsWith("failed:") })
        assertFalse(d.started)
    }

    @Test fun startPositionAndDuration() {
        load(tone(2.0), 1, startMs = 1000)
        assertEquals(1000, backend.positionMs)               // before the item is ready, the position asked for
        rig.waitUntil { d.started }
        rig.scheduler.advance(0.5)
        assertTrue(d.ticks.toString(), d.ticks.first().first >= 900)
        assertEquals(2000.0, backend.durationMs!!.toDouble(), 50.0)
        rig.waitUntil { d.finished }
    }

    /** A pause the engine asked for is never reported back as `backendPaused`. */
    @Test fun requestedPauseIsNotReported() {
        load(tone(3.0), 1)
        rig.waitUntil { d.started && backend.isPlaying }
        backend.pause()
        assertFalse(backend.isPlaying)
        rig.waitUntil { player.playbackState == Player.STATE_READY && !player.isPlaying }
        assertFalse(d.events.toString(), d.events.any { it.startsWith("paused:") })
        val at = backend.positionMs
        backend.seek(2000)
        rig.waitUntil { backend.positionMs >= 1900 }
        assertTrue(backend.positionMs > at)
        backend.play()
        rig.waitUntil { d.finished }
        assertFalse(d.events.toString(), d.events.any { it.startsWith("paused:") })
    }

    /** A replaced item never reports again: only the new load's generation is heard. */
    @Test fun replacedItemIsSilent() {
        load(tone(1.0), 1)
        val mark = d.events.size; val tickMark = d.ticks.size
        load(tone(1.0), 2)
        rig.waitUntil { d.has("finished:2") }
        rig.scheduler.advance(0.5)
        assertFalse(d.events.toString(), d.events.drop(mark).any { it.endsWith(":1") })
        assertTrue(d.ticks.drop(tickMark).all { it.second == 2 })
    }

    /** Gapless: the player moves to the preloaded item by itself; the engine's load of that same source (from `backendFinished`) takes it over, nothing is decoded again. */
    @Test fun preloadedItemIsAdoptedNotReloaded() {
        val a = tone(1.0); val c = tone(1.0)
        d.onFinished = { g -> if (g == 1) { load(c, 2); backend.preload(null, 1f) } }
        load(a, 1)
        backend.preload(c, 1f)
        rig.waitUntil { d.has("finished:2") }
        assertEquals(1, backend.adoptedPreloads)
        assertEquals(listOf("finished:1", "finished:2"), d.events.filter { it.startsWith("finished:") })
        assertEquals("one audio renderer enabling for both items: the change was gapless, not a reload", 1, rig.audioEnabled)
    }

    /** The engine chose something else at the end: the preloaded item must not play on by itself. */
    @Test fun unadoptedPreloadDoesNotPlay() {
        load(tone(1.0), 1)
        backend.preload(tone(3.0), 1f)
        rig.waitUntil { d.has("finished:1") }
        rig.waitUntil { !player.playWhenReady && player.mediaItemCount == 0 }
        assertFalse(backend.isPlaying)
        assertEquals(0, backend.adoptedPreloads)
        assertFalse(d.events.toString(), d.events.any { it.startsWith("started:2") || it.startsWith("paused:") })
    }

    /** Taking over a preloaded episode that resumes mid-way: silent until the seek lands, so its start never sounds. */
    @Test fun adoptWithAPositionWaitsForTheSeek() {
        val a = tone(1.0); val c = tone(3.0)
        d.onFinished = { g -> if (g == 1) load(c, 2, startMs = 1500) }
        load(a, 1)
        backend.preload(c, 1f)
        rig.waitUntil { rig.scheduler.advance(0.5); d.ticks.any { it.second == 2 } }
        assertEquals(1, backend.adoptedPreloads)
        assertTrue(d.ticks.toString(), d.ticks.first { it.second == 2 }.first >= 1400)
        assertFalse(d.events.toString(), d.events.any { it.startsWith("paused:") })
        rig.waitUntil { d.has("finished:2") }
    }

    /** The same seek asked for twice: playback resumes only once it landed, never from the old position. */
    @Test fun aRepeatedSeekLandsBeforePlaybackResumes() {
        load(tone(3.0), 1, autoplay = false)
        rig.waitUntil { player.playbackState == Player.STATE_READY }
        backend.seek(2000); backend.seek(2000)
        assertEquals(2000, backend.positionMs)
        backend.play()
        rig.waitUntil { d.started }
        assertTrue(backend.positionMs >= 1950)
        rig.waitUntil { d.has("finished:1") }
    }

    /** `stop` drops everything silently; the next load plays. (iOS `rebuild` for a media services reset has no Android counterpart.) */
    @Test fun stopThenLoadPlaysTheNewItemOnly() {
        load(tone(3.0), 1)
        rig.waitUntil { d.started }
        backend.stop()
        assertFalse(backend.isPlaying)
        assertEquals(0, player.mediaItemCount)
        val mark = d.events.size; val tickMark = d.ticks.size
        load(tone(1.0), 2)
        rig.waitUntil { d.has("finished:2") }
        rig.scheduler.advance(0.5)
        assertFalse(d.events.toString(), d.events.drop(mark).any { it.endsWith(":1") })
        assertTrue(d.ticks.drop(tickMark).all { it.second == 2 })
    }

    @Test fun ticksComeEveryHalfSecondWhileOnlyPlaying() {
        load(tone(3.0), 1)
        rig.waitUntil { d.started }
        rig.scheduler.advance(0.5)
        assertEquals(1, d.ticks.size)
        assertEquals(1, d.ticks[0].second)
        backend.pause()
        rig.scheduler.advance(2.0)
        assertEquals(1, d.ticks.size)
    }

    // -- rate keeps pitch

    @Test fun episodeRateKeepsPitch() {
        load(tone(2.0), 1, rate = 1.5f)
        assertEquals(1.5f, player.playbackParameters.speed, 0f)
        assertEquals(1f, player.playbackParameters.pitch, 0f)
        backend.setRate(2f)
        assertEquals(2f, player.playbackParameters.speed, 0f)
        assertEquals(1f, player.playbackParameters.pitch, 0f)
        load(tone(2.0), 2, rate = 1f)
        assertEquals(1f, player.playbackParameters.speed, 0f)
    }

    // -- the token

    private fun serverServing(wav: ByteArray, status: Int = 200): MockWebServer {
        val server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest) =
                if (status != 200) MockResponse().setResponseCode(status)
                else MockResponse().setBody(Buffer().write(wav)).setHeader("Content-Type", "audio/wav")
        }
        server.start()
        return server
    }

    @Test fun remoteSourceSendsTheTokenAsAHeaderNeverInTheUri() {
        val server = serverServing(wavBytes(1.0, 100))
        try {
            val uri = Uri.parse(server.url("/api/v1/tracks/7/stream?quality=high").toString())
            val src = MediaSource.Remote(uri, "SECRET")
            val ds = Media3Backend.dataSourceFactory(src, okhttp3.OkHttpClient()).createDataSource()
            ds.open(DataSpec(uri))
            ds.close()
            val request = server.takeRequest()
            assertEquals("Bearer SECRET", request.getHeader("Authorization"))
            assertEquals("/api/v1/tracks/7/stream?quality=high", request.path)
            assertFalse(uri.toString().contains("SECRET"))
            assertFalse(src.toString().contains("SECRET"))
            // A file needs no header, and there is no token to send.
            assertNotNull(Media3Backend.dataSourceFactory(tone(1.0), okhttp3.OkHttpClient()))
        } finally { server.shutdown() }
    }

    @Test fun aLoadedRemoteItemHoldsNoTokenAnywhereAndStillSendsIt() {
        val server = serverServing(wavBytes(1.0, 100))
        try {
            val src = MediaSource.Remote(Uri.parse(server.url("/api/v1/tracks/7/stream?quality=high").toString()), "SECRET")
            load(src, 1)
            rig.waitUntil { d.has("finished:1") }
            assertEquals("Bearer SECRET", server.takeRequest().getHeader("Authorization"))
            val item = player.currentMediaItem ?: player.getMediaItemAt(0)
            assertFalse(item.localConfiguration!!.uri.toString().contains("SECRET"))
            assertFalse(item.mediaId.contains("SECRET"))
            assertFalse(item.localConfiguration!!.tag.toString().contains("SECRET"))
            assertFalse(item.mediaMetadata.toString().contains("SECRET"))
        } finally { server.shutdown() }
    }

    // -- failures

    @Test fun anUnreachableServerIsANetworkFailure() {
        val server = serverServing(ByteArray(0))
        val uri = Uri.parse(server.url("/x").toString())
        server.shutdown()
        load(MediaSource.Remote(uri, "T"), 3)
        rig.waitUntil { d.events.any { it.startsWith("failed:") } }
        assertEquals(listOf("failed:true:3"), d.events.filter { it.startsWith("failed:") })
    }

    @Test fun aServerAnswerOfNotFoundIsNotANetworkFailure() {
        val server = serverServing(ByteArray(0), status = 404)
        try {
            load(MediaSource.Remote(Uri.parse(server.url("/x").toString()), "T"), 5)
            rig.waitUntil { d.events.any { it.startsWith("failed:") } }
            assertEquals(listOf("failed:false:5"), d.events.filter { it.startsWith("failed:") })
        } finally { server.shutdown() }
    }

    /** A preloaded item that cannot be read must not stop the one playing; the engine's load of it sees it failed. */
    @Test fun aFailingPreloadDoesNotInterruptTheCurrentItem() {
        load(tone(2.0), 1)
        backend.preload(MediaSource.File(File(tmp.root, "missing.wav")), 1f)
        rig.waitUntil { d.has("finished:1") }
        assertFalse(d.events.toString(), d.events.any { it.startsWith("failed:") || it.startsWith("paused:") })
    }

    // -- loudness gain and the master volume

    @Test fun aGainBelowOneIsTheItemsOwnAndLeavesTheMasterVolume() {
        load(tone(1.0), 1, autoplay = false, gain = 0.5f)
        assertEquals(0.5f, cell(0).factor, 0f)
        assertEquals(1f, player.volume, 0f)
    }

    @Test fun anAttenuatedItemNeverSoundsAtFullLevel() {
        load(tone(1.0, 16000), 1, gain = 0.5f)
        rig.waitUntil { d.has("finished:1") }
        assertEquals(listOf(8000), rig.capture.steps())
    }

    @Test fun unityGainLeavesTheLevelAlone() {
        load(tone(1.0, 8000), 1)
        rig.waitUntil { d.has("finished:1") }
        assertEquals(listOf(8000), rig.capture.steps())
    }

    /** The loudness switch, live: the loaded item's gain changes, and a gain asked for last wins. */
    @Test fun setGainChangesTheCurrentItem() {
        load(tone(2.0, 8000), 1, autoplay = false, gain = 0.5f)
        backend.setGain(1f, 1f)
        backend.setGain(0.25f, 1f)
        assertEquals(0.25f, cell(0).factor, 0f)
        backend.play()
        rig.waitUntil { d.has("finished:1") }
        assertEquals(listOf(2000), rig.capture.steps())
    }

    /** (The fake clock lets the renderer queue the whole file ahead, so how much of it still came out at 8000 is not fixed: only the order is.) */
    @Test fun setGainWhilePlayingTakesEffectWithoutARestart() {
        load(tone(3.0, 8000), 1)
        rig.waitUntil { d.started && rig.capture.levels.isNotEmpty() }
        backend.setGain(0.25f, 1f)
        assertEquals(0.25f, cell(0).factor, 0f)
        rig.waitUntil { d.has("finished:1") }
        val steps = rig.capture.steps()
        assertTrue(steps.toString(), steps == listOf(8000) || steps == listOf(8000, 2000))
        assertEquals(1, rig.audioEnabled)
    }

    /** Gapless at two levels: the new item's factor applies exactly at the item boundary, the old tail keeps its own. */
    @Test fun theGainChangesAtTheItemBoundaryAndNowhereElse() {
        val a = tone(1.0, 8000); val c = tone(1.0, 16000)
        d.onFinished = { g -> if (g == 1) { load(c, 2, gain = 0.25f); backend.preload(null, 1f) } }
        load(a, 1, gain = 1f)
        backend.preload(c, 0.25f)
        assertEquals(0.25f, cell(1).factor, 0f)                         // its own gain is set while it is only preloaded
        rig.waitUntil { d.has("finished:2") }
        assertEquals(1, backend.adoptedPreloads)
        assertEquals(1, rig.audioEnabled)
        // 8000 (item one, unchanged), then 4000 (item two at its 0.25): never 16000 (unattenuated), never 2000 (tail attenuated).
        assertEquals(listOf(8000, 4000), rig.capture.steps())
        assertEquals("every stream's item was found", 0, backend.gainMisses)
    }

    /**
     * The same with a sink that refuses each buffer a few times, as a full AudioTrack does. A regression check on the
     * levels only: in Robolectric a WAV runs in bypass, so this does not prove that the factor is switched when the
     * output reaches the item (a switch at stream input would pass too). That is checked on a device (Task 14).
     */
    @Test fun theGainChangesAtTheItemBoundaryEvenWhenTheSinkIsBackedUp() {
        backedUpRig()
        val a = tone(1.0, 8000); val c = tone(1.0, 16000)
        d.onFinished = { g -> if (g == 1) { load(c, 2, gain = 0.25f); backend.preload(null, 1f) } }
        load(a, 1, gain = 1f)
        backend.preload(c, 0.25f)
        rig.waitUntil { d.has("finished:2") }
        assertEquals(1, backend.adoptedPreloads)
        assertEquals(listOf(8000, 4000), rig.capture.steps())
    }

    /** A fast skip takes a preloaded item over before it played: the gain asked for with the load is on it before its first sound. */
    @Test fun anAdoptedPreloadTakesTheNewGainBeforeItsFirstSound() {
        load(tone(3.0, 8000), 1)
        val c = tone(2.0, 16000)
        backend.preload(c, 0.25f)
        load(c, 2, gain = 0.1f)
        assertEquals(1, backend.adoptedPreloads)
        assertEquals(0.1f, cell(player.currentMediaItemIndex).factor, 0f)
        rig.waitUntil { d.has("finished:2") }
        assertTrue(rig.capture.steps().toString(), rig.capture.steps().none { it == 16000 || it == 4000 })
        assertEquals(1600, rig.capture.steps().last())
    }

    @Test fun unityPlaysAtOnce() {
        load(tone(2.0), 1)
        assertTrue(player.playWhenReady)
        assertEquals(1, player.mediaItemCount)
        assertEquals(1f, cell(0).factor, 0f)
        assertNotEquals(Player.STATE_IDLE, player.playbackState)      // prepared, nothing held back
        rig.waitUntil { d.started }
        assertEquals(listOf("buffering:true:1", "started:1"), d.events)
    }

    /** The loudness switch changes the preloaded item in place: the same item, nothing preloaded again. */
    @Test fun setGainChangesThePreloadedItemInPlace() {
        load(tone(3.0), 1, autoplay = false)
        backend.preload(tone(2.0), 1f)
        val next = player.getMediaItemAt(1)
        backend.setGain(0.5f, 0.25f)
        assertEquals(2, player.mediaItemCount)
        assertEquals(next.mediaId, player.getMediaItemAt(1).mediaId)
        assertEquals(0.25f, cell(1).factor, 0f)
        assertEquals(0.5f, cell(0).factor, 0f)
    }

    @Test fun setMasterVolumeIsTheMasterVolumeAndKeepsAcrossItems() {
        backend.setMasterVolume(0.3f)
        assertEquals(0.3f, player.volume, 0.0001f)
        load(tone(1.0), 1, gain = 0.5f)
        assertEquals(0.3f, player.volume, 0.0001f)
        backend.setMasterVolume(2f)                                  // clamped
        assertEquals(1f, player.volume, 0f)
    }

    // -- interruptions

    /** The listener ExoPlayer's audio focus manager registered with the (shadow) AudioManager: what the system calls on a focus change. */
    private fun focusListener(context: android.content.Context): AudioManager.OnAudioFocusChangeListener {
        val request = shadowOf(context.getSystemService(AudioManager::class.java)).lastAudioFocusRequest
        val field = generateSequence<Class<*>>(request.javaClass) { it.superclass }
            .flatMap { it.declaredFields.asSequence() }.first { AudioManager.OnAudioFocusChangeListener::class.java.isAssignableFrom(it.type) }
        field.isAccessible = true
        return field.get(request) as AudioManager.OnAudioFocusChangeListener
    }

    /** A phone call: ExoPlayer keeps playWhenReady and suppresses playback, so the pause is reported from the suppression reason and the system's resume is a start. */
    @Test fun aTransientFocusLossIsReportedAsAPauseAndTheResumeAsAStart() {
        load(tone(10.0), 1)
        rig.waitUntil { d.started }
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        val focus = focusListener(context)
        focus.onAudioFocusChange(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        rig.waitUntil { d.has("paused:1") }
        assertEquals(1, d.events.count { it == "paused:1" })
        assertTrue("ExoPlayer resumes by itself, so the backend still wants playback", player.playWhenReady)
        assertFalse(backend.isPlaying)
        focus.onAudioFocusChange(AudioManager.AUDIOFOCUS_GAIN)
        rig.waitUntil { d.events.count { it == "started:1" } == 2 }
        assertEquals(1, d.events.count { it == "paused:1" })
    }

    /** A pause the engine asked for during a focus loss is not reported again, and nothing is reported while it is not wanted. */
    @Test fun aFocusLossWhileTheEngineHasPausedReportsNothing() {
        load(tone(10.0), 1)
        rig.waitUntil { d.started }
        backend.pause()
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        focusListener(context)
            .onAudioFocusChange(AudioManager.AUDIOFOCUS_LOSS_TRANSIENT)
        rig.waitUntil { player.playbackSuppressionReason == Player.PLAYBACK_SUPPRESSION_REASON_TRANSIENT_AUDIO_FOCUS_LOSS || !player.playWhenReady }
        assertFalse(d.events.toString(), d.events.any { it.startsWith("paused:") })
    }

    // -- what counts as a network failure

    private fun failure(cause: Throwable, code: Int = PlaybackException.ERROR_CODE_IO_UNSPECIFIED) = PlaybackException("x", cause, code)
    private val spec = DataSpec(Uri.parse("https://lark.test/x"))

    @Test fun anyDroppedConnectionInTheChainIsANetworkFailure() {
        // A drop in the middle of a read, a reset stream, an EOF: HttpDataSourceException anywhere in the chain.
        val read = HttpDataSource.HttpDataSourceException(java.io.EOFException("eof"), spec, PlaybackException.ERROR_CODE_IO_UNSPECIFIED, HttpDataSource.HttpDataSourceException.TYPE_READ)
        assertTrue(Media3Backend.isNetworkError(failure(read)))
        assertTrue(Media3Backend.isNetworkError(failure(RuntimeException("wrapped", read))))
        assertTrue(Media3Backend.isNetworkError(failure(java.net.SocketException("reset"))))
        assertTrue(Media3Backend.isNetworkError(failure(java.net.UnknownHostException("h"))))
        assertTrue(Media3Backend.isNetworkError(failure(java.io.IOException("x"), PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT)))
    }

    @Test fun aServerStatusACleartextRefusalAndOtherErrorsAreNotNetworkFailures() {
        val status = HttpDataSource.InvalidResponseCodeException(404, "Not Found", null, emptyMap(), spec, ByteArray(0))
        assertFalse(Media3Backend.isNetworkError(failure(status, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS)))
        val cleartext = HttpDataSource.CleartextNotPermittedException(java.io.IOException("c"), spec)
        assertFalse(Media3Backend.isNetworkError(failure(cleartext, PlaybackException.ERROR_CODE_IO_CLEARTEXT_NOT_PERMITTED)))
        assertFalse(Media3Backend.isNetworkError(failure(java.io.FileNotFoundException("f"), PlaybackException.ERROR_CODE_IO_FILE_NOT_FOUND)))
        assertFalse(Media3Backend.isNetworkError(failure(androidx.media3.common.ParserException.createForMalformedContainer("m", null), PlaybackException.ERROR_CODE_PARSING_CONTAINER_MALFORMED)))
    }

    // -- release

    @Test fun releaseDropsTheDelegateAndAPendingAdoptReport() {
        val a = tone(1.0); val c = tone(3.0)
        d.onFinished = { g -> if (g == 1) load(c, 2) }
        load(a, 1)
        backend.preload(c, 1f)
        rig.waitUntil { d.has("finished:1") }
        val before = d.events.toList()
        rig.release()                                  // the adopt's "started" post is still queued
        org.robolectric.shadows.ShadowLooper.idleMainLooper()
        assertNull(backend.delegate)
        assertEquals(before, d.events)
    }
}
