package lark

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.io.IOException
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

/**
 * The run's server (android/scripts/emulator-smoke.sh: web/e2e/start-server.sh with the demo library of
 * web/e2e/demo/demo.py, reached from the emulator at `larkServer`), through its API with a session of its own, to set
 * it up for a test. The admin password is the run's made-up `larkPassword`.
 */
class DemoServer {
    val url: String = (Instrumented.argument("larkServer") ?: "http://10.0.2.2:4700").trimEnd('/')
    val password: String = checkNotNull(Instrumented.argument("larkPassword")) {
        "no larkPassword: run android/scripts/emulator-smoke.sh (it starts the server and passes it)"
    }
    private val http = OkHttpClient.Builder().callTimeout(15, TimeUnit.SECONDS).build()

    /** A session's token and the user's id. */
    data class Session(val token: String, val userId: Long)

    fun login(device: String): Session {
        val r = call("POST", "/api/v1/auth/login", JSONObject().put("username", "admin").put("password", password).put("device_name", device), null)
        return Session(r.getString("token"), r.getJSONObject("user").getLong("id"))
    }

    /**
     * Opening the app does nothing by itself (`on_open: resume`), and [FAVORITES] are the admin's favorites. Waits for
     * the library scan. Returns id → title.
     */
    fun seedFavorites(session: Session = login("smoke-setup")): Map<Long, String> {
        call("PUT", "/api/v1/me/preferences", JSONObject().put("language", JSONObject.NULL).put("on_open", "resume"), session.token)
        val out = LinkedHashMap<Long, String>()
        for (title in FAVORITES) {
            var id: Long? = null
            Instrumented.waitFor("the library has \"$title\"", 120_000, everyMs = 1_000) {
                val tracks = call("GET", "/api/v1/search?q=" + URLEncoder.encode(title, "UTF-8"), null, session.token).getJSONArray("tracks")
                id = (0 until tracks.length()).map { tracks.getJSONObject(it) }.firstOrNull { it.getString("title") == title }?.getLong("id")
                id != null
            }
            call("PUT", "/api/v1/favorites/${id!!}", null, session.token)
            out[id!!] = title
        }
        return out
    }

    /** The server answers (false offline). */
    fun reachable(): Boolean = try {
        http.newCall(Request.Builder().url("$url/api/v1/info").build()).execute().use { it.isSuccessful }
    } catch (_: IOException) {
        false
    }

    private fun call(method: String, path: String, body: JSONObject?, token: String?): JSONObject {
        val payload = body?.toString()?.toRequestBody("application/json".toMediaType()) ?: if (method == "PUT") ByteArray(0).toRequestBody() else null
        val b = Request.Builder().url(url + path).method(method, payload)
        token?.let { b.header("Authorization", "Bearer $it") }
        http.newCall(b.build()).execute().use { r ->
            val text = r.body.string()
            check(r.isSuccessful) { "$method $path: ${r.code}" }
            return if (text.isBlank()) JSONObject() else JSONObject(text)
        }
    }

    companion object {
        /** Songs of the demo library (web/e2e/demo/demo.py). */
        val FAVORITES = listOf("Morning in A", "Etude for Middle C", "Cruise Control")
    }
}
