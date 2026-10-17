package lark.testing

import android.net.Uri
import lark.api.ApiTrack
import lark.api.LarkApiProtocol
import lark.api.LyricsDoc
import lark.api.PlayEvent
import lark.api.QueueAnswer
import lark.api.RandomFavorites
import lark.api.ServerInfo
import lark.api.ServerQueue
import lark.bridge.Item
import java.io.File

/** A scripted server for the player-state tests; calls the tests don't script fail loudly. */
class FakeLarkApi : LarkApiProtocol {
    data class SaveCall(val trackIds: List<Long>, val index: Int, val positionMs: Long)

    val saveCalls = mutableListOf<SaveCall>()
    val postedEvents = mutableListOf<List<PlayEvent>>()
    var queueCalls = 0
    var queueAnswer: QueueAnswer? = null
    var saveError: Exception? = null
    var postEventsError: Exception? = null
    /** What `postEvents` answers; null: as many as were sent. */
    var accepted: Int? = null
    var deviceName = "device:1"
    var gate: kotlinx.coroutines.CompletableDeferred<Unit>? = null   // when set, postEvents waits for it (after recording)

    override suspend fun queue(): QueueAnswer { queueCalls++; return queueAnswer ?: error("no queue answer scripted") }

    override suspend fun saveQueue(trackIds: List<Long>, index: Int, positionMs: Long): ServerQueue? {
        saveCalls.add(SaveCall(trackIds, index, positionMs))
        saveError?.let { throw it }
        return ServerQueue(trackIds, index, positionMs, saveCalls.size.toLong(), deviceName, 0)
    }

    override suspend fun postEvents(events: List<PlayEvent>): Int {
        postedEvents.add(events)
        gate?.await()
        postEventsError?.let { throw it }
        return accepted ?: events.size
    }

    override val token: String? get() = "t"
    override suspend fun info(): ServerInfo = TODO("not scripted")
    override suspend fun favorites(): List<ApiTrack> = TODO("not scripted")
    override suspend fun randomFavorites(n: Int, exclude: List<Long>): RandomFavorites = TODO("not scripted")
    override suspend fun randomTracks(n: Int, exclude: List<Long>): List<ApiTrack> = TODO("not scripted")
    override suspend fun radio(n: Int, exclude: List<Long>): List<ApiTrack> = TODO("not scripted")
    override suspend fun episodeProgress(id: String, positionS: Long, played: Boolean) = TODO("not scripted")
    override suspend fun lyrics(trackId: Long): LyricsDoc = TODO("not scripted")
    override suspend fun artwork(item: Item): ByteArray = TODO("not scripted")
    override suspend fun download(trackId: Long, quality: String, dir: File): File = TODO("not scripted")
    override fun streamUrl(item: Item, quality: String): Uri = TODO("not scripted")
}
