package lark.auth

import android.webkit.CookieManager

/** Where native finds the web view's session cookie. */
interface CookieSource {
    /** The value of the `lark_token` cookie the web view would send to [url]; null when there is none. */
    fun larkToken(url: String): String?
}

const val COOKIE_NAME = "lark_token"

/** The web view's own cookie jar (`CookieManager` is process-wide and shared by every WebView). */
class WebViewCookieSource : CookieSource {
    override fun larkToken(url: String): String? = parseCookie(CookieManager.getInstance().getCookie(url), COOKIE_NAME)
}

/**
 * The value of cookie [name] in a `Cookie:` header as `CookieManager.getCookie` returns it (`a=b; c=d`, no attributes).
 * With the name twice, the last wins: the jar lists the longest Path first, so the server's `Path=/` cookie comes after
 * one a script set for a sub-path. A cookie with an empty value is none.
 */
fun parseCookie(header: String?, name: String): String? {
    if (header.isNullOrEmpty()) return null
    var found: String? = null
    for (part in header.split(';')) {
        val eq = part.indexOf('=')
        if (eq < 0 || part.substring(0, eq).trim() != name) continue
        found = part.substring(eq + 1).trim()
    }
    return found?.takeIf { it.isNotEmpty() }
}
