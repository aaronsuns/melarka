package lark.api

import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import lark.auth.AuthStore
import lark.bridge.Item
import lark.testing.FakeCookies
import lark.testing.MemorySecretStore
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File

/** Port of LarkAPITests over MockWebServer. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class LarkApiTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var server: MockWebServer
    private val cookies = FakeCookies()
    private lateinit var auth: AuthStore
    private lateinit var api: LarkApi
    private lateinit var dir: File

    @Before fun setUp() {
        server = MockWebServer().also { it.start() }
        val secrets = MemorySecretStore().also { it.set("token", "T1") }
        auth = AuthStore(secrets, cookies)
        api = LarkApi(server.url("/").toString(), OkHttpClient(), auth)
        dir = File(tmp.root, "dl")
    }
    @After fun tearDown() { server.shutdown() }

    private fun trackItem(id: Long) = Item(Item.Kind.track, id.toString(), "t", "a", "b", 1000, JsonNull)
    private fun episodeItem(id: String) = Item(Item.Kind.episode, id, "t", "a", "频道", 1000, JsonNull)
    private fun trackJson(id: Int, extra: String = "") =
        """{"id":$id,"title":"T$id","artist":"A","album":"B","duration_ms":1000,"favorite":true,"codec":"flac"$extra}"""
    private fun respond(body: String, status: Int = 200, type: String? = "application/json") {
        server.enqueue(MockResponse().setResponseCode(status).setBody(body).also { if (type != null) it.setHeader("Content-Type", type) })
    }
    private fun last(): RecordedRequest = server.takeRequest()
    private fun body(r: RecordedRequest) = Json.parseToJsonElement(r.body.readUtf8())
    private fun query(r: RecordedRequest) = r.requestUrl!!.let { u -> (0 until u.querySize).map { u.queryParameterName(it) to u.queryParameterValue(it) } }
    private fun <T> run(block: suspend () -> T): T = runBlocking { block() }
    private inline fun <reified E : Throwable> failsWith(block: () -> Unit): E {
        try { block() } catch (e: Throwable) { if (e is E) return e; throw e }
        fail("expected ${E::class.simpleName}"); throw AssertionError()
    }
    private fun expectError(expected: LarkError, block: suspend () -> Unit) {
        val e = failsWith<LarkError> { runBlocking { block() } }
        assertEquals(expected, e)
    }

    @Test fun saveQueueBodyIsExactlyThreeFields() = run {
        respond("""{"queue":{"track_ids":[1,2],"current_index":1,"position_ms":5000,"version":2,"updated_by":"device:9","updated_at":1},"tracks":[]}""")
        val stored = api.saveQueue(listOf(1, 2), 1, 5000)
        assertEquals("device:9", stored?.updatedBy)
        val r = last()
        assertEquals("PUT", r.method); assertEquals("/api/v1/queue", r.requestUrl!!.encodedPath)
        assertEquals("Bearer T1", r.getHeader("Authorization"))
        assertEquals("application/json", r.getHeader("Content-Type"))
        assertEquals(Json.parseToJsonElement("""{"track_ids":[1,2],"current_index":1,"position_ms":5000}"""), body(r))
    }

    @Test fun saveQueueAnUnreadableAnswerIsNotAnError() = run {
        respond("""{"ok":true}""")
        assertNull(api.saveQueue(listOf(1), 0, 0))
    }

    @Test fun queueDecodesQueueAndTracksWithRawMeta() = run {
        respond("""{"queue":{"track_ids":[5],"current_index":0,"position_ms":10,"version":3,"updated_by":"device:1","updated_at":7},"tracks":[${trackJson(5, ""","path":"/x"""")}]}""")
        val (q, tracks) = api.queue()
        assertEquals(listOf(5L), q.trackIds); assertEquals(3L, q.version); assertEquals("device:1", q.updatedBy)
        assertEquals("T5", tracks.first().track.title)
        assertEquals(JsonPrimitive("/x"), tracks.first().json.jsonObject["path"])   // the raw object, unknown fields kept
        assertEquals("GET", last().method)
    }

    @Test fun emptyQueueWithNullTracks() = run {
        respond("""{"queue":{"track_ids":[],"current_index":0,"position_ms":0,"version":0,"updated_by":"","updated_at":0},"tracks":null}""")
        val (q, tracks) = api.queue()
        assertTrue(q.trackIds.isEmpty()); assertTrue(tracks.isEmpty())
    }

    @Test fun trackGainIsReadWhenPresent() = run {
        respond("[${trackJson(1, ""","gain_db":-6.5""")},${trackJson(2)}]")
        val t = api.radio(2, emptyList())
        assertEquals(-6.5, t[0].track.gainDb!!, 0.0); assertNull(t[1].track.gainDb)
    }

    @Test fun anUnusableBodyIsBadResponse() {
        respond("<html>")
        expectError(LarkError.BadResponse) { api.queue() }
        respond("""{"queue":{"track_ids":["x"]}}""")
        expectError(LarkError.BadResponse) { api.queue() }
        respond("""{"items":[{"id":1}],"next_cursor":""}""")
        expectError(LarkError.BadResponse) { api.favorites() }
    }

    @Test fun postEventsShape() = run {
        respond("""{"accepted":1}""")
        val n = api.postEvents(listOf(PlayEvent("e1", 4, 1700, 30, false, "high")))
        assertEquals(1, n)
        val r = last()
        assertEquals("POST", r.method); assertEquals("/api/v1/events/play", r.requestUrl!!.encodedPath)
        assertEquals(Json.parseToJsonElement("""{"events":[{"client_event_id":"e1","track_id":4,"started_at":1700,"played_seconds":30,"skipped":false,"quality":"high"}]}"""), body(r))
    }

    @Test fun postNoEventsSendsNothing() = run {
        assertEquals(0, api.postEvents(emptyList())); assertEquals(0, server.requestCount)
    }

    @Test fun episodeProgressPlayed() = run {
        server.enqueue(MockResponse().setResponseCode(204)); server.enqueue(MockResponse().setResponseCode(204))
        api.episodeProgress("abcdefghijk", 0, true)
        var r = last()
        assertEquals("PUT", r.method); assertEquals("/api/v1/episodes/abcdefghijk/progress", r.requestUrl!!.encodedPath)
        assertEquals(Json.parseToJsonElement("""{"position_s":0,"played":true}"""), body(r))
        api.episodeProgress("abcdefghijk", 12, false)
        r = last()
        assertEquals(Json.parseToJsonElement("""{"position_s":12}"""), body(r))   // `played` only when true
    }

    @Test fun episodeProgressRejectsBadIdWithoutRequest() {
        expectError(LarkError.BadRequest) { api.episodeProgress("../queue", 1, false) }
        assertEquals(0, server.requestCount)
    }

    @Test fun favoritesFollowsCursor() = run {
        respond("""{"items":[${trackJson(1)},${trackJson(2)}],"next_cursor":"c2"}""")
        respond("""{"items":[${trackJson(3)}],"next_cursor":""}""")
        val all = api.favorites()
        assertEquals(listOf(1L, 2L, 3L), all.map { it.track.id })
        assertEquals(2, server.requestCount)
        val r1 = last(); val r2 = last()
        assertEquals("/api/v1/tracks", r1.requestUrl!!.encodedPath)
        assertEquals(listOf("favorite" to "1", "limit" to "500"), query(r1))
        assertEquals(listOf("favorite" to "1", "limit" to "500", "cursor" to "c2"), query(r2))
    }

    @Test fun favoritesStopsOnRepeatedCursor() = run {
        repeat(5) { respond("""{"items":[${trackJson(1)}],"next_cursor":"same"}""") }
        assertEquals(2, api.favorites().size)   // first page, then "same" once; the repeat ends the loop
        assertEquals(2, server.requestCount)
    }

    @Test fun randomFavoritesFallbackSource() = run {
        respond("""{"source":"all","tracks":[${trackJson(9)}]}""")
        val r = api.randomFavorites(20, listOf(1, 2))
        assertEquals("all", r.source); assertEquals(listOf(9L), r.tracks.map { it.track.id })
        val q = last()
        assertEquals("/api/v1/tracks/random", q.requestUrl!!.encodedPath)
        assertEquals(listOf("source" to "favorites", "n" to "20", "exclude" to "1,2"), query(q))
    }

    @Test fun randomTracksAndRadioAreBareArrays() = run {
        respond("[${trackJson(1)},${trackJson(2)}]")
        assertEquals(2, api.randomTracks(5, emptyList()).size)
        assertEquals(listOf("n" to "5"), query(last()))
        respond("[${trackJson(1)},${trackJson(2)}]")
        assertEquals(2, api.radio(5, listOf(3)).size)
        val r = last()
        assertEquals("/api/v1/radio/next", r.requestUrl!!.encodedPath)
        assertEquals(listOf("n" to "5", "exclude" to "3"), query(r))
        respond("null")
        assertTrue(api.radio(5, emptyList()).isEmpty())
    }

    @Test fun lyricsDecodes() = run {
        respond("""{"found":true,"id":4,"synced":true,"lines":[{"t_ms":1000,"text":"hi"}],"offset_ms":-200}""")
        val d = api.lyrics(3)
        assertTrue(d.found); assertTrue(d.synced); assertEquals(-200L, d.offsetMs)
        assertEquals(listOf(LyricsLine(1000, "hi")), d.lines)
        assertEquals("/api/v1/tracks/3/lyrics", last().requestUrl!!.encodedPath)
        respond("""{"found":false,"synced":false,"offset_ms":0}""")
        val none = api.lyrics(3)
        assertFalse(none.found); assertNull(none.lines)
        respond("""{"found":true,"synced":false,"text":"plain words"}""")
        assertEquals("plain words", api.lyrics(3).text)
    }

    @Test fun artworkUrls() = run {
        server.enqueue(MockResponse().setBody(Buffer().write(byteArrayOf(1, 2, 3))).setHeader("Content-Type", "image/jpeg"))
        assertEquals(listOf<Byte>(1, 2, 3), api.artwork(trackItem(7)).toList())
        var r = last()
        assertEquals("/api/v1/tracks/7/cover", r.requestUrl!!.encodedPath); assertEquals(listOf("size" to "300"), query(r))
        server.enqueue(MockResponse().setBody("x"))
        api.artwork(episodeItem("abcdefghijk"))
        assertEquals("/api/v1/episodes/abcdefghijk/thumbnail", last().requestUrl!!.encodedPath)
        server.enqueue(MockResponse().setBody("x"))
        api.artwork(Item(Item.Kind.track, "yt:mjuS9shGYhE", "t", "a", "YouTube", 1000, JsonNull))
        assertEquals("/api/v1/videos/mjuS9shGYhE/thumbnail", last().requestUrl!!.encodedPath)
        expectError(LarkError.BadRequest) { api.artwork(episodeItem("../x")) }
        assertEquals(0, server.requestCount - 3)
    }

    @Test fun downloadNamesFileByContentType() = run {
        for ((type, ext) in listOf("audio/mp4" to "m4a", "audio/mpeg" to "mp3", "audio/flac" to "flac", "audio/mpeg; charset=x" to "mp3")) {
            respond("AUDIO", type = type)
            val f = api.download(12, "high", dir)
            assertEquals("12.$ext", f.name)
            assertEquals("AUDIO", f.readText())
            val r = last()
            assertEquals("/api/v1/tracks/12/stream", r.requestUrl!!.encodedPath); assertEquals(listOf("quality" to "high"), query(r))
            assertEquals("Bearer T1", r.getHeader("Authorization"))
            assertEquals(listOf(f.name), dir.list()!!.toList())   // no partial file left beside it
            f.delete()
        }
    }

    @Test fun downloadLeavesNothingOnError() {
        respond("""{"error":"nope","code":"unplayable"}""", status = 415)
        expectError(LarkError.Http(415, "unplayable")) { api.download(12, "high", dir) }
        assertEquals(emptyList<String>(), dir.list()?.toList() ?: emptyList<String>())
        respond("<html>", type = "text/html")   // a non-audio 200 is refused too
        expectError(LarkError.BadResponse) { api.download(12, "high", dir) }
        assertEquals(emptyList<String>(), dir.list()?.toList() ?: emptyList<String>())
    }

    @Test fun downloadErrorBodyIsReadCapped() {
        respond("""{"error":"x","code":"unplayable"}""" + " ".repeat(100_000), status = 415)
        expectError(LarkError.Http(415, "unplayable")) { api.download(1, "high", dir) }
    }

    @Test fun a401ClearsTokenAndSignals() {
        respond("""{"error":"not signed in","code":"not_signed_in"}""", status = 401)
        var signalled = false
        api.onUnauthorized = { signalled = true; auth.clear() }   // AppServices wires signOut() here
        expectError(LarkError.Unauthorized) { api.lyrics(1) }
        assertNull(auth.token); assertTrue(signalled)
    }

    @Test fun concurrent401sSignOutExactlyOnce() = run {
        val calls = java.util.concurrent.atomic.AtomicInteger()
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest) = MockResponse().setResponseCode(401)
        }
        api.onUnauthorized = { calls.incrementAndGet(); delay(100); auth.clear() }
        (1L..5L).map { i -> kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.Dispatchers.Default).async { runCatching { api.lyrics(i) } } }.awaitAll()
        assertEquals(1, calls.get()); assertNull(auth.token)
    }

    @Test fun stale401LeavesTheNewTokenAlone() {
        // The request goes out with T1; a re-login to T2 lands before the 401 comes back.
        var calls = 0
        api.onUnauthorized = { calls++ }
        val me = MockWebServer().also { it.start(); it.enqueue(MockResponse().setBody("""{"id":2}""")) }
        server.dispatcher = object : okhttp3.mockwebserver.Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                auth.clear()
                cookies.value = "T2"
                runBlocking { auth.harvest(me.url("/").toString()) }
                return MockResponse().setResponseCode(401)
            }
        }
        try { expectError(LarkError.Unauthorized) { api.lyrics(1) } } finally { me.shutdown() }
        assertEquals(0, calls); assertEquals("T2", auth.token)
    }

    @Test fun a401WithoutHookStillClearsTheToken() {
        respond("", status = 401)
        expectError(LarkError.Unauthorized) { api.queue() }
        assertNull(auth.token)
    }

    @Test fun a401OnDownloadSignals() {
        respond("", status = 401)
        var signalled = false
        api.onUnauthorized = { signalled = true }
        expectError(LarkError.Unauthorized) { api.download(1, "high", dir) }
        assertTrue(signalled)
    }

    @Test fun noTokenNoRequest() {
        auth.clear()
        expectError(LarkError.SignedOut) { api.lyrics(1) }
        expectError(LarkError.SignedOut) { api.download(1, "high", dir) }
        assertEquals(0, server.requestCount)
    }

    @Test fun streamUrlCarriesNoToken() {
        val a = LarkApi("https://lark.test", OkHttpClient(), auth)
        val u = a.streamUrl(trackItem(7), "high")
        assertFalse(u.toString().contains("T1"))
        assertEquals("https://lark.test/api/v1/tracks/7/stream?quality=high", u.toString())
        assertEquals("https://lark.test/api/v1/episodes/abcdefghijk/stream?kind=audio", a.streamUrl(episodeItem("abcdefghijk"), "high").toString())
        // A YouTube track (a playlist played from search): its audio preview, by video id.
        val yt = Item(Item.Kind.track, "yt:mjuS9shGYhE", "流年", "王菲", "YouTube", 263_000, JsonNull)
        assertEquals("mjuS9shGYhE", yt.youtubeVideoId)
        assertNull(yt.trackId)
        assertEquals("https://lark.test/api/v1/previews/by-video/mjuS9shGYhE/stream?title=%E6%B5%81%E5%B9%B4&channel=%E7%8E%8B%E8%8F%B2&duration_s=263",
            a.streamUrl(yt, "high").toString())
        assertNull(Item(Item.Kind.track, "yt:short").youtubeVideoId)
        assertNull(trackItem(7).youtubeVideoId)
        assertEquals("T1", a.token)
    }

    @Test fun baseWithAPathPrefixKeepsIt() {
        val a = LarkApi("https://lark.test/music/", OkHttpClient(), auth)
        assertEquals("https://lark.test/music/api/v1/tracks/7/stream?quality=high", a.streamUrl(trackItem(7), "high").toString())
    }

    @Test fun httpErrorCarriesServerCode() {
        respond("""{"error":"x","code":"rate_limited"}""", status = 429)
        expectError(LarkError.Http(429, "rate_limited")) { api.queue() }
        respond("not json", status = 500)
        expectError(LarkError.Http(500, null)) { api.queue() }
    }

    @Test fun infoNeedsNoToken() = run {
        auth.clear()
        respond("""{"name":"Melarka","version":"0.2.15","language":"sv","languages":["en","sv"],"offline_cache":false,"channels":true}""")
        val i = api.info()
        assertEquals("Melarka", i.name); assertEquals("sv", i.language); assertFalse(i.offlineCache); assertTrue(i.channels)
        val r = last()
        assertEquals("/api/v1/info", r.requestUrl!!.encodedPath); assertNull(r.getHeader("Authorization"))
        respond("", status = 503)
        expectError(LarkError.Http(503, null)) { api.info() }
    }

    @Test fun theTokenIsNeverInAnyUrl() = run {
        respond("[]"); respond("[]"); respond("""{"found":false,"synced":false}""")
        api.radio(1, emptyList()); api.randomTracks(1, emptyList()); api.lyrics(1)
        repeat(3) { assertFalse(last().path!!.contains("T1")) }
    }
}
