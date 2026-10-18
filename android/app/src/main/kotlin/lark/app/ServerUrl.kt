package lark.app

import android.net.Uri
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.CacheControl
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.net.URI
import java.util.concurrent.TimeUnit

/** The server address the user types: `https://host[:port]/`, normalised. */
object ServerUrl {
    /** The first-launch field starts empty: there is no default server. The placeholder is only an example. */
    const val INITIAL_TEXT = ""
    const val PLACEHOLDER = "https://music.example.com"

    /**
     * `https://host[:port]/`, or plain http for development hosts (`localhost`, `*.local`, and the emulator's host
     * loopback `10.0.2.2` in debug builds). Null if invalid: no scheme, another scheme, no host, user info, bad port.
     * Path, query and fragment are dropped.
     */
    fun normalize(input: String, allowEmulatorHost: Boolean = BuildConfig.DEBUG): Uri? {
        val u = try { URI(input.trim()) } catch (_: Exception) { return null }
        val scheme = u.scheme?.lowercase() ?: return null
        val host = u.host?.lowercase()?.takeIf { it.isNotEmpty() } ?: return null
        if (u.userInfo != null || u.port > 65535) return null
        when (scheme) {
            "https" -> {}
            "http" -> if (!(host == "localhost" || host.endsWith(".local") || (allowEmulatorHost && host == "10.0.2.2"))) return null
            else -> return null
        }
        return Uri.parse("$scheme://$host${if (u.port >= 0) ":${u.port}" else ""}/")
    }

    fun infoUrl(server: Uri): Uri = server.buildUpon().path("/api/v1/info").build()

    /** True when `GET /api/v1/info` answers 200 (it is unauthenticated): the server screen's check before it is kept. */
    suspend fun check(server: Uri, client: OkHttpClient = LarkHttp.client): Boolean = withContext(Dispatchers.IO) {
        val c = client.newBuilder().callTimeout(10, TimeUnit.SECONDS).build()
        val request = Request.Builder().url(infoUrl(server).toString()).cacheControl(CacheControl.FORCE_NETWORK).get().build()
        try {
            c.newCall(request).execute().use { it.code == 200 }
        } catch (_: IOException) {
            false
        } catch (_: IllegalArgumentException) {
            false
        }
    }
}
