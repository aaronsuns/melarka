package lark.testing

import android.net.Uri
import kotlinx.coroutines.CompletableDeferred
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import lark.api.ApiTrack
import lark.api.LarkApiProtocol
import lark.api.LarkError
import lark.api.LyricsDoc
import lark.api.LyricsLine
import lark.api.PlayEvent
import lark.api.QueueAnswer
import lark.api.RandomFavorites
import lark.api.ServerInfo
import lark.api.ServerQueue
import lark.api.Track
import lark.bridge.Item
import java.io.File
import kotlin.coroutines.Continuation
import kotlin.coroutines.resume
import kotlin.coroutines.suspendCoroutine

/** A scripted server for the player tests (iOS `FakeAPI`); calls the tests don't script fail loudly. */
class FakeLarkApi : LarkApiProtocol {
    data class SaveCall(val trackIds: List<Long>, val index: Int, val positionMs: Long)
    data class ProgressCall(val id: String, val positionS: Long, val played: Boolean)
    data class ExcludeCall(val n: Int, val exclude: List<Long>)

    override var token: String? = "T1"

    // Canned answers
    var randomFavoritesAnswer = RandomFavorites("favorites", emptyList())
    var randomTracksAnswer: List<ApiTrack> = emptyList()
    var radioAnswer: List<ApiTrack> = emptyList()
    var queueAnswer = QueueAnswer(ServerQueue(emptyList(), 0, 0, 0, "", 0), emptyList())
    var saveError: Exception? = null
    var postEventsError: Exception? = null
    /** What `postEvents` answers; null: as many as were sent. */
    var accepted: Int? = null
    var refillError: Exception? = null
    var deviceName = "device:1"
    var lyricsAnswers = mutableMapOf<Long, LyricsDoc>()

    /** When set, postEvents waits for it (after recording). */
    var gate: CompletableDeferred<Unit>? = null
    /**
     * While true, saveQueue waits (after recording) until [releaseSaves], and a cancel does not interrupt the wait:
     * the answer still arrives, as from a server that answered while the app was signing out.
     */
    var holdSaves = false
    private val heldSaves = mutableListOf<Continuation<Unit>>()
    fun releaseSaves() { holdSaves = false; heldSaves.toList().also { heldSaves.clear() }.forEach { it.resume(Unit) } }

    // Recorded calls
    val saveCalls = mutableListOf<SaveCall>()
    val postedEvents = mutableListOf<List<PlayEvent>>()
    val progressCalls = mutableListOf<ProgressCall>()
    val randomFavoritesCalls = mutableListOf<ExcludeCall>()
    val randomTracksCalls = mutableListOf<ExcludeCall>()
    val radioCalls = mutableListOf<ExcludeCall>()
    val lyricsCalls = mutableListOf<Long>()
    var queueCalls = 0

    override suspend fun queue(): QueueAnswer { queueCalls++; return queueAnswer }

    override suspend fun saveQueue(trackIds: List<Long>, index: Int, positionMs: Long): ServerQueue? {
        saveCalls.add(SaveCall(trackIds, index, positionMs))
        if (holdSaves) suspendCoroutine { heldSaves.add(it) }
        saveError?.let { throw it }
        return ServerQueue(trackIds, index, positionMs, saveCalls.size.toLong(), deviceName, 0)
    }

    override suspend fun postEvents(events: List<PlayEvent>): Int {
        postedEvents.add(events)
        gate?.await()
        postEventsError?.let { throw it }
        return accepted ?: events.size
    }

    override suspend fun randomFavorites(n: Int, exclude: List<Long>): RandomFavorites {
        randomFavoritesCalls.add(ExcludeCall(n, exclude))
        refillError?.let { throw it }
        return randomFavoritesAnswer
    }

    override suspend fun randomTracks(n: Int, exclude: List<Long>): List<ApiTrack> {
        randomTracksCalls.add(ExcludeCall(n, exclude))
        refillError?.let { throw it }
        return randomTracksAnswer
    }

    override suspend fun radio(n: Int, exclude: List<Long>): List<ApiTrack> {
        radioCalls.add(ExcludeCall(n, exclude))
        refillError?.let { throw it }
        return radioAnswer
    }

    override suspend fun episodeProgress(id: String, positionS: Long, played: Boolean) {
        progressCalls.add(ProgressCall(id, positionS, played))
    }

    override suspend fun lyrics(trackId: Long): LyricsDoc {
        lyricsCalls.add(trackId)
        return lyricsAnswers[trackId] ?: throw LarkError.BadResponse
    }

    override fun streamUrl(item: Item, quality: String): Uri = when (item.kind) {
        Item.Kind.track -> Uri.parse("https://lark.test/api/v1/tracks/${item.id}/stream?quality=$quality")
        Item.Kind.episode -> Uri.parse("https://lark.test/api/v1/episodes/${item.id}/stream?kind=audio")
    }

    override suspend fun info(): ServerInfo = TODO("not scripted")
    override suspend fun favorites(): List<ApiTrack> = TODO("not scripted")
    override suspend fun artwork(item: Item): ByteArray = TODO("not scripted")
    override suspend fun download(trackId: Long, quality: String, dir: File): File = TODO("not scripted")
}

/** A favorite as the API sends it, with its raw JSON (iOS `trackModel`). */
fun trackModel(id: Int, durationMs: Long = 200_000): ApiTrack {
    val json = buildJsonObject {
        put("id", id); put("title", "T$id"); put("artist", "A"); put("album", "B"); put("duration_ms", durationMs); put("favorite", true)
    }
    return ApiTrack(Track(id.toLong(), "T$id", "A", "B", durationMs, true), json)
}

fun lyricsDoc(lines: List<Pair<Long, String>> = listOf(0L to "x"), offset: Long? = null, synced: Boolean = true,
              instrumental: Boolean? = null, found: Boolean = true) =
    LyricsDoc(found, synced, instrumental, lines.map { LyricsLine(it.first, it.second) }, offset, null)

