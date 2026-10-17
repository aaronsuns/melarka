package lark.testing

import lark.auth.CookieSource

/** The web view's `lark_token` cookie: set `value` to what the page's cookie jar holds (null: none). */
class FakeCookies(var value: String? = null) : CookieSource {
    val asked = mutableListOf<String>()
    override fun larkToken(url: String): String? { asked += url; return value }
}
