package lark.auth

import android.webkit.CookieManager
import android.webkit.WebStorage
import lark.bridge.BridgeController
import java.net.URI

/** The web view's data for a server as the app services need it: the session cookie, and everything on leaving it. */
interface WebData : CookieSource {
    /** Sign-out: the `lark_token` cookie goes, so only a fresh login can hand native a token again. */
    fun deleteSessionCookie(serverUrl: String)

    /** Leaving the server (iOS `removeWebsiteData`): its cookies, local storage, IndexedDB and the like. */
    fun removeWebsiteData(serverUrl: String)
}

/**
 * The process-wide `CookieManager` and `WebStorage` of every WebView (main thread). `CookieManager` hands out names and
 * values only, so a cookie is deleted by writing it again expired, for the host itself and for its domain (the server
 * may have set it either way). The HTTP cache goes with the web view (`WebShell.destroy`).
 */
class WebViewData(
    private val jar: Jar = SystemJar,
    private val deleteOrigin: (String) -> Unit = { WebStorage.getInstance().deleteOrigin(it) },
) : WebData {
    /** The part of `CookieManager` used here. */
    interface Jar {
        fun getCookie(url: String): String?
        fun setCookie(url: String, value: String)
        fun flush()
    }

    private object SystemJar : Jar {
        override fun getCookie(url: String): String? = CookieManager.getInstance().getCookie(url)
        override fun setCookie(url: String, value: String) = CookieManager.getInstance().setCookie(url, value)
        override fun flush() = CookieManager.getInstance().flush()
    }

    override fun larkToken(url: String): String? = parseCookie(jar.getCookie(url), COOKIE_NAME)

    override fun deleteSessionCookie(serverUrl: String) {
        expire(serverUrl, COOKIE_NAME)
        jar.flush()
    }

    override fun removeWebsiteData(serverUrl: String) {
        cookieNames(jar.getCookie(serverUrl)).forEach { expire(serverUrl, it) }
        jar.flush()
        deleteOrigin(BridgeController.allowedOrigin(serverUrl))
    }

    private fun expire(serverUrl: String, name: String) {
        val host = URI(serverUrl).host ?: return
        for (domain in listOf("", "; Domain=$host")) {
            jar.setCookie(serverUrl, "$name=; Path=/$domain; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT")
        }
    }

    companion object {
        /** The names in a `Cookie:` header as `CookieManager.getCookie` returns it (`a=b; c=d`). */
        fun cookieNames(header: String?): List<String> =
            header.orEmpty().split(';').mapNotNull { p -> p.substringBefore('=').trim().takeIf { it.isNotEmpty() } }.distinct()
    }
}
