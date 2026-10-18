package lark.auth

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The WebView's cookie jar as the services use it. `CookieManager` only takes `Set-Cookie` strings, and a cookie is
 * deleted by writing it expired (Robolectric's jar ignores expiry, so what is written is checked here; that the WebView
 * drops it is checked on the emulator, Task 14).
 */
class WebViewDataTest {
    private class FakeJar : WebViewData.Jar {
        val cookies = linkedMapOf<String, String>()
        val written = mutableListOf<Pair<String, String>>()
        var flushes = 0
        override fun getCookie(url: String): String? = cookies.entries.joinToString("; ") { "${it.key}=${it.value}" }.ifEmpty { null }
        override fun setCookie(url: String, value: String) {
            written += url to value
            val name = value.substringBefore('=')
            if (value.contains("Max-Age=0")) cookies.remove(name) else cookies[name] = value.substringAfter('=').substringBefore(';')
        }
        override fun flush() { flushes++ }
    }

    private val url = "https://signout.lark.test/"
    private val jar = FakeJar().apply { cookies["lark_token"] = "T1"; cookies["other"] = "keep" }
    private val data = WebViewData(jar, deleteOrigin = { origins += it })
    private val origins = mutableListOf<String>()

    @Test fun cookieNames() {
        assertEquals(listOf("lark_token", "other"), WebViewData.cookieNames("lark_token=T1; other=keep; lark_token=T0"))
        assertEquals(listOf<String>(), WebViewData.cookieNames(null))
    }

    /** Written expired for the host and for its domain (the server may have set either), on path `/`, then flushed. */
    @Test fun signOutDeletesTheSessionCookieAndKeepsTheOthers() {
        assertEquals("T1", data.larkToken(url))
        data.deleteSessionCookie(url)
        assertNull(data.larkToken(url))
        assertEquals("other=keep", jar.getCookie(url))
        assertEquals(listOf(
            url to "lark_token=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
            url to "lark_token=; Path=/; Domain=signout.lark.test; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        ), jar.written)
        assertEquals(1, jar.flushes)
    }

    @Test fun leavingTheServerDeletesItsCookiesAndStorage() {
        data.removeWebsiteData(url)
        assertNull(jar.getCookie(url))
        assertEquals(listOf("https://signout.lark.test"), origins)
    }
}
