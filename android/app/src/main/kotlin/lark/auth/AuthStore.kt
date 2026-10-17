package lark.auth

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.longOrNull
import lark.api.apiUrl
import lark.bridge.lenientJson
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl

/**
 * The device token native uses for API calls, downloads and streams.
 *
 * The token is the web's HttpOnly `lark_token` session cookie; the page cannot read it, so native harvests it from the
 * web view's cookie jar. It lives only here and in the Keystore-backed [SecretStore]: it is never logged, never put in
 * a URL and never sent over the bridge. Android hands out cookie values without their attributes, so a cookie is taken
 * by name for the server URL and must pass `GET /api/v1/me` before it is stored.
 */
class AuthStore(
    private val secrets: SecretStore,
    private val cookies: CookieSource,
    private val http: OkHttpClient = OkHttpClient(),
) {
    private val lock = Any()
    /** The last cleared token. A 401 or a logout racing the server's Set-Cookie can leave that cookie in place for a
     *  while; it must not be adopted again. A fresh login issues a new token. */
    private var revoked: String? = null

    @Volatile var token: String? = secrets.get(TOKEN)
        private set
    @Volatile var userId: Long? = secrets.get(USER_ID)?.toLongOrNull()
        private set
    /** Called with the new token (null when cleared), only when it actually changes. */
    @Volatile var onChange: ((String?) -> Unit)? = null

    /**
     * Takes the page's `lark_token` cookie for [serverUrl] once the server confirms it. [userId] is the user the page
     * says is signed in. A missing or empty cookie changes nothing: signing out is [clear]'s job, so a briefly empty
     * cookie jar never signs native out. Returns true when a new token was stored.
     */
    suspend fun harvest(serverUrl: String, userId: Long? = null): Boolean {
        synchronized(lock) {
            if (userId != null && userId != this.userId) { this.userId = userId; secrets.set(USER_ID, userId.toString()) }
        }
        val candidate = cookies.larkToken(serverUrl)?.takeIf { it.isNotEmpty() } ?: return false
        if (!isNew(candidate)) return false
        val meId = verify(serverUrl, candidate) ?: return false
        val changed = synchronized(lock) {
            if (!isNew(candidate)) return false   // cleared or adopted by someone else while the server was asked
            token = candidate; secrets.set(TOKEN, candidate)
            if (meId.id != null && meId.id != this.userId) { this.userId = meId.id; secrets.set(USER_ID, meId.id.toString()) }
            true
        }
        if (changed) onChange?.invoke(candidate)
        return changed
    }

    private fun isNew(candidate: String) = synchronized(lock) { candidate != revoked && candidate != token }

    /** Logout, user switch or a 401. */
    fun clear() {
        val had: Boolean
        synchronized(lock) {
            had = token != null
            token?.let { revoked = it }
            token = null; userId = null
            secrets.set(TOKEN, null); secrets.set(USER_ID, null)
        }
        if (had) onChange?.invoke(null)
    }

    private class Me(val id: Long?)

    /** The server's answer to `GET /api/v1/me` for [candidate]; null when it is not a signed-in 200 (or unreachable). */
    private suspend fun verify(serverUrl: String, candidate: String): Me? = withContext(Dispatchers.IO) {
        try {
            val req = Request.Builder().url(apiUrl(serverUrl.toHttpUrl(), "/me")).header("Authorization", "Bearer $candidate").build()
            http.newBuilder().callTimeout(java.time.Duration.ofSeconds(15)).build().newCall(req).execute().use { r ->
                if (r.code != 200) return@use null
                val id = ((lenientJson.parseToJsonElement(r.body.string()) as? JsonObject)?.get("id") as? JsonPrimitive)?.longOrNull
                Me(id)
            }
        } catch (e: kotlinx.coroutines.CancellationException) { throw e
        } catch (_: Exception) { null }   // never carries the token: the message of a bad header value would
    }

    private companion object { const val TOKEN = "token"; const val USER_ID = "userId" }
}
