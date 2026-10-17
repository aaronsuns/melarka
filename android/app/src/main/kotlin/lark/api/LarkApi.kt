package lark.api

import android.net.Uri
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.auth.AuthStore
import lark.bridge.Item
import lark.bridge.lenientJson
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.File
import java.io.IOException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

interface LarkApiProtocol {
    suspend fun info(): ServerInfo                                                   // GET /info, unauthenticated
    suspend fun favorites(): List<ApiTrack>                                          // GET /tracks?favorite=1&limit=500 (+cursor), all pages
    suspend fun randomFavorites(n: Int, exclude: List<Long>): RandomFavorites        // GET /tracks/random?source=favorites
    suspend fun randomTracks(n: Int, exclude: List<Long>): List<ApiTrack>            // GET /tracks/random
    suspend fun radio(n: Int, exclude: List<Long>): List<ApiTrack>                   // GET /radio/next
    suspend fun queue(): QueueAnswer                                                 // GET /queue
    suspend fun saveQueue(trackIds: List<Long>, index: Int, positionMs: Long): ServerQueue?   // PUT /queue; the stored queue (null: unreadable answer)
    suspend fun postEvents(events: List<PlayEvent>): Int                             // POST /events/play
    suspend fun episodeProgress(id: String, positionS: Long, played: Boolean)        // PUT /episodes/{id}/progress
    suspend fun lyrics(trackId: Long): LyricsDoc                                     // GET /tracks/{id}/lyrics
    suspend fun artwork(item: Item): ByteArray                                       // track: /tracks/{id}/cover?size=300; episode: /episodes/{id}/thumbnail
    suspend fun download(trackId: Long, quality: String, dir: File): File            // GET /tracks/{id}/stream?quality= -> file with ext from Content-Type
    fun streamUrl(item: Item, quality: String): Uri                                  // /api/v1/tracks/{id}/stream?quality= or /api/v1/episodes/{id}/stream?kind=audio
    val token: String?
}

/** `<base>/api/v1<path>`, keeping any path prefix the server is mounted under. */
internal fun apiUrl(base: HttpUrl, path: String, query: List<Pair<String, String>> = emptyList()): HttpUrl {
    val b = base.newBuilder().query(null).fragment(null)
    val segs = base.pathSegments.dropLastWhile { it.isEmpty() }
    b.encodedPath("/")
    segs.forEach { b.addPathSegment(it) }
    b.addPathSegment("api").addPathSegment("v1")
    path.split('/').filter { it.isNotEmpty() }.forEach { b.addPathSegment(it) }
    query.forEach { (k, v) -> b.addQueryParameter(k, v) }
    return b.build()
}

/**
 * The Melarka server's HTTP API. Every call sends `Authorization: Bearer <token>`; with no token it throws
 * [LarkError.SignedOut] without a request. The token is never put in a URL ([streamUrl] has none; the player's data
 * source adds the header itself) and never logged.
 */
class LarkApi(base: String, client: OkHttpClient, private val auth: AuthStore) : LarkApiProtocol {
    private val base: HttpUrl = base.toHttpUrl()
    private val json = client.newBuilder().timeouts(JSON_TIMEOUT_S).build()
    private val bulk = client.newBuilder().timeouts(DOWNLOAD_TIMEOUT_S).build()

    /** Run on a 401, before [LarkError.Unauthorized] is thrown. `AppServices` wires `signOut()` here, which clears the
     *  token and deletes the session cookie. With no hook set, the API clears the token itself. */
    @Volatile var onUnauthorized: (suspend () -> Unit)? = null

    override val token: String? get() = auth.token

    private fun url(path: String, query: List<Pair<String, String>> = emptyList()) = apiUrl(base, path, query)

    /** YouTube video ids only: they go straight into a path. */
    private fun episodePath(id: String, tail: String): String {
        if (!VIDEO_ID.matches(id)) throw LarkError.BadRequest
        return "/episodes/$id$tail"
    }

    override fun streamUrl(item: Item, quality: String): Uri = Uri.parse(when (item.kind) {
        Item.Kind.track -> item.youtubeVideoId?.let { v ->
            url("/previews/by-video/$v/stream", listOf("title" to item.title, "channel" to item.artist, "duration_s" to (item.durationMs / 1000).toString()))
        } ?: url("/tracks/${item.id}/stream", listOf("quality" to quality))
        Item.Kind.episode -> url("/episodes/${item.id}/stream", listOf("kind" to "audio"))
    }.toString())

    // MARK: Transport

    private fun request(method: String, url: HttpUrl, body: JsonElement? = null): Request {
        val token = auth.token ?: throw LarkError.SignedOut
        try {
            return Request.Builder().url(url).header("Authorization", "Bearer $token")
                .method(method, body?.let { it.toString().toByteArray().toRequestBody(JSON) }).build()
        } catch (_: IllegalArgumentException) {
            throw LarkError.BadRequest   // a token that is not a valid header value; the exception's own text would quote it
        }
    }

    private suspend fun Call.await(): Response = suspendCancellableCoroutine { c ->
        c.invokeOnCancellation { cancel() }
        enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) { if (c.isActive) c.resumeWithException(e) }
            override fun onResponse(call: Call, response: Response) { if (c.isActive) c.resume(response) else response.close() }
        })
    }

    /** Maps a non-2xx answer to an error; a 401 first runs the sign-out hook. Does not close [resp]. */
    private suspend fun check(resp: Response, sent: Request) {
        if (resp.isSuccessful) return
        if (resp.code == 401) {
            // Only the token this request carried can be signed out: a late 401 for an old token, after a re-login
            // (or after another 401 already signed out), must leave the current state alone.
            val sentToken = sent.header("Authorization")?.removePrefix("Bearer ")
            if (sentToken != null && auth.token == sentToken) signOutOnce()
            throw LarkError.Unauthorized
        }
        throw LarkError.Http(resp.code, withContext(Dispatchers.IO) { errorCode(resp) })
    }

    /** The server's `code` from at most the first 4 KB of the body: enough for a JSON error, never a whole failed download. */
    private fun errorCode(resp: Response): String? = try {
        ((lenientJson.parseToJsonElement(resp.peekBody(4096).string()) as? JsonObject)?.get("code") as? JsonPrimitive)?.takeIf { it.isString }?.content
    } catch (_: Exception) { null }

    private val signOutScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val signOutLock = Any()
    private var signOutTask: Deferred<Unit>? = null

    /** Concurrent 401s share one in-flight sign-out, which a cancelled caller does not abort. */
    private suspend fun signOutOnce() {
        val t = synchronized(signOutLock) {
            signOutTask ?: signOutScope.async {
                try { onUnauthorized?.invoke() ?: auth.clear() } finally { synchronized(signOutLock) { signOutTask = null } }
            }.also { signOutTask = it }
        }
        t.await()
    }

    /** The checked answer's body; the response is always closed. */
    private suspend fun send(req: Request, client: OkHttpClient = json): ByteArray {
        val resp = client.newCall(req).await()
        return resp.use { check(it, req); withContext(Dispatchers.IO) { it.body.bytes() } }
    }

    private suspend fun getJson(url: HttpUrl): JsonElement {
        val bytes = send(request("GET", url))
        return decoding { parseJson(bytes.toString(Charsets.UTF_8)) }
    }

    private fun ids(a: List<Long>) = a.joinToString(",")

    // MARK: Library

    override suspend fun info(): ServerInfo {
        val req = Request.Builder().url(url("/info")).build()   // unauthenticated
        val resp = json.newBuilder().timeouts(INFO_TIMEOUT_S).build().newCall(req).await()
        val bytes = resp.use {
            if (!it.isSuccessful) throw LarkError.Http(it.code, null)
            withContext(Dispatchers.IO) { it.body.bytes() }
        }
        return decoding { infoFrom(parseJson(bytes.toString(Charsets.UTF_8))) }
    }

    override suspend fun favorites(): List<ApiTrack> {
        val out = mutableListOf<ApiTrack>()
        var cursor: String? = null
        repeat(MAX_PAGES) {
            val q = mutableListOf("favorite" to "1", "limit" to PAGE_LIMIT.toString())
            cursor?.let { q += "cursor" to it }
            val page = getJson(url("/tracks", q))
            val next = decoding {
                val o = page as? JsonObject ?: throw LarkError.BadResponse
                out += tracksFrom(o["items"])
                (o["next_cursor"] as? JsonPrimitive)?.takeIf { it.isString }?.content ?: ""
            }
            if (next.isEmpty() || next == cursor) return out   // a repeated cursor would loop forever
            cursor = next
        }
        return out
    }

    override suspend fun randomFavorites(n: Int, exclude: List<Long>): RandomFavorites {
        val q = mutableListOf("source" to "favorites", "n" to n.toString())
        if (exclude.isNotEmpty()) q += "exclude" to ids(exclude)
        val e = getJson(url("/tracks/random", q))
        return decoding {
            val o = e as? JsonObject ?: throw LarkError.BadResponse
            RandomFavorites((o["source"] as? JsonPrimitive)?.takeIf { it.isString }?.content ?: throw LarkError.BadResponse, tracksFrom(o["tracks"]))
        }
    }

    override suspend fun randomTracks(n: Int, exclude: List<Long>): List<ApiTrack> {
        val q = mutableListOf("n" to n.toString())
        if (exclude.isNotEmpty()) q += "exclude" to ids(exclude)
        return getJson(url("/tracks/random", q)).let { e -> decoding { tracksFrom(e) } }
    }

    override suspend fun radio(n: Int, exclude: List<Long>): List<ApiTrack> {
        val q = mutableListOf("n" to n.toString())
        if (exclude.isNotEmpty()) q += "exclude" to ids(exclude)
        return getJson(url("/radio/next", q)).let { e -> decoding { tracksFrom(e) } }
    }

    // MARK: Personal

    override suspend fun queue(): QueueAnswer {
        val e = getJson(url("/queue"))
        return decoding {
            val o = e as? JsonObject ?: throw LarkError.BadResponse
            QueueAnswer(queueFrom(o["queue"] ?: throw LarkError.BadResponse), tracksFrom(o["tracks"]))
        }
    }

    /** Answers the queue as stored, whose `updated_by` is the server's name for this device: the engine uses it to tell
     *  its own queue from another device's. An answer it cannot read is not an error. */
    override suspend fun saveQueue(trackIds: List<Long>, index: Int, positionMs: Long): ServerQueue? {
        // The server's decoder rejects unknown fields: exactly these three, no version.
        val body = JsonObject(linkedMapOf("current_index" to JsonPrimitive(index), "position_ms" to JsonPrimitive(positionMs),
            "track_ids" to JsonArray(trackIds.map { JsonPrimitive(it) })))
        val bytes = send(request("PUT", url("/queue"), body))
        return try { queueFrom((parseJson(bytes.toString(Charsets.UTF_8)) as JsonObject)["queue"]!!) } catch (e: Exception) {
            if (e is CancellationException) throw e
            null
        }
    }

    override suspend fun postEvents(events: List<PlayEvent>): Int {
        if (events.isEmpty()) return 0
        val body = JsonObject(mapOf("events" to JsonArray(events.map { it.toJson() })))
        val bytes = send(request("POST", url("/events/play"), body))
        return decoding {
            ((parseJson(bytes.toString(Charsets.UTF_8)) as JsonObject)["accepted"] as JsonPrimitive).let { p ->
                if (p.isString) throw LarkError.BadResponse else p.content.toInt()
            }
        }
    }

    override suspend fun episodeProgress(id: String, positionS: Long, played: Boolean) {
        val path = episodePath(id, "/progress")
        // `played` only when true: the server treats a missing field as "leave the played flag alone".
        val body = JsonObject(linkedMapOf<String, JsonElement>().apply {
            if (played) put("played", JsonPrimitive(true))
            put("position_s", JsonPrimitive(positionS))
        })
        send(request("PUT", url(path), body))
    }

    override suspend fun lyrics(trackId: Long): LyricsDoc =
        getJson(url("/tracks/$trackId/lyrics")).let { e -> decoding { lyricsFrom(e) } }

    // MARK: Binary

    override suspend fun artwork(item: Item): ByteArray = when (item.kind) {
        Item.Kind.track -> {
            val v = item.youtubeVideoId
            if (v != null) send(request("GET", url("/videos/$v/thumbnail")))
            else send(request("GET", url("/tracks/${item.id}/cover", listOf("size" to "300"))))
        }
        Item.Kind.episode -> send(request("GET", url(episodePath(item.id, "/thumbnail"))))
    }

    override suspend fun download(trackId: Long, quality: String, dir: File): File {
        val req = request("GET", url("/tracks/$trackId/stream", listOf("quality" to quality)))
        val resp = bulk.newCall(req).await()
        return resp.use {
            check(it, req)
            val type = (it.header("Content-Type") ?: "").substringBefore(';').trim().lowercase()
            val ext = fileExtension(type) ?: throw LarkError.BadResponse
            withContext(Dispatchers.IO) {
                dir.mkdirs()
                val dest = File(dir, "$trackId.$ext")
                val part = File(dir, "$trackId.part")
                try {
                    it.body.byteStream().use { src -> Files.copy(src, part.toPath(), StandardCopyOption.REPLACE_EXISTING) }
                    Files.move(part.toPath(), dest.toPath(), StandardCopyOption.REPLACE_EXISTING)
                } finally { part.delete() }   // gone after a successful move; the partial otherwise
                dest
            }
        }
    }

    companion object {
        const val JSON_TIMEOUT_S = 15L
        const val DOWNLOAD_TIMEOUT_S = 120L
        private const val INFO_TIMEOUT_S = 10L
        private const val PAGE_LIMIT = 500
        private const val MAX_PAGES = 100
        private val JSON = "application/json".toMediaType()
        private val VIDEO_ID = Regex("^[A-Za-z0-9_-]{11}$")

        fun fileExtension(contentType: String): String? = when (contentType) {
            "audio/mp4", "audio/x-m4a", "audio/m4a" -> "m4a"
            "audio/mpeg", "audio/mp3" -> "mp3"
            "audio/flac", "audio/x-flac" -> "flac"
            "audio/aac" -> "aac"
            "audio/wav", "audio/x-wav", "audio/wave" -> "wav"
            "audio/ogg", "audio/opus" -> "ogg"
            else -> if (contentType.startsWith("audio/")) "audio" else null
        }

        /** Inactivity timeouts, like URLRequest's timeoutInterval: a slow transfer that keeps moving is not cut off. */
        private fun OkHttpClient.Builder.timeouts(seconds: Long) = connectTimeout(seconds, TimeUnit.SECONDS)
            .readTimeout(seconds, TimeUnit.SECONDS).writeTimeout(seconds, TimeUnit.SECONDS)
    }
}
