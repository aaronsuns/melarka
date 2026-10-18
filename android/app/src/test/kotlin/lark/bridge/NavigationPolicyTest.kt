package lark.bridge

import lark.bridge.NavigationPolicy.Target
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** iOS `BridgeWebViewTests.testOriginMatching` / `testNavigationPolicy`, and `PageLoadFailureTests.testWhatCountsAsAFailedPage`. */
class NavigationPolicyTest {
    @Test fun originMatching() {
        val server = "https://lark.test/"
        assertTrue(BridgeController.originMatches("https", "lark.test", 0, server))
        assertTrue(BridgeController.originMatches("https", "lark.test", -1, server))          // Android's "no port"
        assertTrue(BridgeController.originMatches("https", "lark.test", 443, server))
        assertTrue(BridgeController.originMatches("HTTPS", "Lark.Test", 0, server))
        assertFalse(BridgeController.originMatches("https", "lark.test", 8443, server))
        assertFalse(BridgeController.originMatches("http", "lark.test", 0, server))
        assertFalse(BridgeController.originMatches("https", "evil.lark.test", 0, server))
        val dev = "http://mac.local:8080"
        assertTrue(BridgeController.originMatches("http", "mac.local", 8080, dev))
        assertFalse(BridgeController.originMatches("http", "mac.local", 0, dev))
    }

    /** What `addWebMessageListener` reports as a frame's origin: `scheme://host[:port]`, no path. */
    @Test fun messageOriginMatching() {
        assertTrue(BridgeController.originMatches("https://lark.test", "https://lark.test/"))
        assertTrue(BridgeController.originMatches("https://lark.test:443", "https://lark.test/"))
        assertTrue(BridgeController.originMatches("http://mac.local:8080", "http://mac.local:8080/"))
        assertFalse(BridgeController.originMatches("https://evil.test", "https://lark.test/"))
        assertFalse(BridgeController.originMatches("https://lark.test.evil.test", "https://lark.test/"))
        assertFalse(BridgeController.originMatches("https://lark.test@example.com", "https://lark.test/"))
        assertFalse(BridgeController.originMatches("http://lark.test", "https://lark.test/"))
        assertFalse(BridgeController.originMatches("null", "https://lark.test/"))           // an opaque origin (sandbox, data:)
        assertFalse(BridgeController.originMatches("", "https://lark.test/"))
    }

    /** The rule `addWebMessageListener` and `addDocumentStartJavaScript` take: the server's origin, nothing else. */
    @Test fun allowedOriginRule() {
        assertEquals("https://lark.test", BridgeController.allowedOrigin("https://lark.test/"))
        assertEquals("http://mac.local:8080", BridgeController.allowedOrigin("http://mac.local:8080/"))
        assertEquals("https://lark.test:8443", BridgeController.allowedOrigin("https://Lark.test:8443/"))
    }

    @Test fun navigationPolicy() {
        val server = "https://lark.test/"
        fun d(s: String, target: Target = Target.mainFrame, tapped: Boolean = true) =
            NavigationPolicy.decide(s, server, target, userActivated = tapped)
        // main frame
        assertEquals(NavigationPolicy.allow, d("https://lark.test/library"))
        assertEquals(NavigationPolicy.allow, d("https://lark.test/library", tapped = false))
        assertEquals(NavigationPolicy.openExternally, d("https://www.youtube.com/watch?v=x"))
        assertEquals(NavigationPolicy.openExternally, d("http://lark.test/"))                     // not the server's origin
        assertEquals(NavigationPolicy.openExternally, d("mailto:a@b.c"))
        assertEquals(NavigationPolicy.allow, d("about:blank"))
        assertEquals(NavigationPolicy.cancel, d("data:text/html,<h1>sign in</h1>"))
        assertEquals(NavigationPolicy.cancel, d("javascript:alert(1)"))
        assertEquals(NavigationPolicy.allow, d("blob:https://lark.test/abc"))
        assertEquals(NavigationPolicy.cancel, d("blob:https://evil.test/abc"))
        assertEquals(NavigationPolicy.cancel, d("about:srcdoc"))
        // subframes: the 视频 YouTube embed and in-page frames stay in the page
        assertEquals(NavigationPolicy.allow, d("https://www.youtube.com/embed/x", Target.subframe, tapped = false))
        assertEquals(NavigationPolicy.allow, d("about:blank", Target.subframe, tapped = false))
        assertEquals(NavigationPolicy.allow, d("data:text/html,x", Target.subframe, tapped = false))
        assertEquals(NavigationPolicy.allow, d("blob:https://evil.test/abc", Target.subframe, tapped = false))
        assertEquals(NavigationPolicy.cancel, d("tel:123", Target.subframe, tapped = false))       // no app launch without a tap
        assertEquals(NavigationPolicy.cancel, d("itms-services://?action=download-manifest", Target.subframe, tapped = false))
        assertEquals(NavigationPolicy.openExternally, d("mailto:a@b.c", Target.subframe, tapped = true))
        // new windows (target=_blank, window.open): only same-origin http(s) loads in place
        assertEquals(NavigationPolicy.loadInMainFrame, d("https://lark.test/x", Target.newWindow))
        assertEquals(NavigationPolicy.openExternally, d("https://www.youtube.com/watch?v=x", Target.newWindow))
        assertEquals(NavigationPolicy.openExternally, d("https://github.com/aaronsuns/melarka", Target.newWindow))   // the web settings' "Source code" link
        assertEquals(NavigationPolicy.cancel, d("data:text/html,<h1>sign in</h1>", Target.newWindow))
        assertEquals(NavigationPolicy.cancel, d("javascript:alert(1)", Target.newWindow))
        assertEquals(NavigationPolicy.cancel, d("blob:https://lark.test/abc", Target.newWindow))
        assertEquals(NavigationPolicy.cancel, d("about:blank", Target.newWindow))
        assertEquals(NavigationPolicy.cancel, d("tel:123", Target.newWindow, tapped = false))
        // Local schemes never leave the page, in any frame, tapped or not: no file, content provider or browser page.
        for (target in Target.entries) for (tapped in listOf(true, false)) {
            for (local in listOf("file:///sdcard/Download/x.html", "FILE:///data/data/x", "content://media/external/1",
                                 "chrome://settings", "chrome-extension://abc/x.html", "android.resource://pkg/raw/x",
                                 "view-source:https://lark.test/", "filesystem:https://lark.test/temporary/x")) {
                assertEquals("$local $target $tapped", NavigationPolicy.cancel, d(local, target, tapped))
            }
        }
        // An intent: URL is only ever a plain view of the URL (the opener never parses it into an Intent).
        assertEquals(NavigationPolicy.openExternally, d("intent://scan/#Intent;scheme=zxing;package=x;end"))
        assertEquals(NavigationPolicy.cancel, d("intent://scan/#Intent;end", Target.subframe, tapped = false))
    }

    /** URLs a parser could read two ways: the origin is the host after the user info, and `\` ends it as `/` does. */
    @Test fun trickyUrlsAreNotTheServer() {
        val server = "https://lark.test/"
        fun d(s: String) = NavigationPolicy.decide(s, server, Target.mainFrame, userActivated = false)
        assertEquals(NavigationPolicy.openExternally, d("https://lark.test@example.com/"))
        assertEquals(NavigationPolicy.openExternally, d("https://evil.test\\@lark.test/"))
        assertEquals(NavigationPolicy.openExternally, d("https://lark.test.evil.test/"))
        assertEquals(NavigationPolicy.openExternally, d("https://lark.test:8443/"))
        assertEquals(NavigationPolicy.allow, d("https://LARK.test:443/x?y#z"))
        assertEquals(NavigationPolicy.openExternally, d("https:///nohost"))
        assertEquals(NavigationPolicy.cancel, d("blob:null/abc"))
        assertEquals(NavigationPolicy.cancel, d("not a url"))                     // no scheme: dropped, not launched
    }

    // MARK: PageLoad (iOS PageLoadFailureTests.testWhatCountsAsAFailedPage)

    @Test fun whatCountsAsAFailedPage() {
        assertTrue(PageLoad.isFailure(android.webkit.WebViewClient.ERROR_HOST_LOOKUP, "net::ERR_NAME_NOT_RESOLVED"))      // cannotFindHost
        assertTrue(PageLoad.isFailure(android.webkit.WebViewClient.ERROR_HOST_LOOKUP, "net::ERR_INTERNET_DISCONNECTED"))  // notConnectedToInternet
        assertTrue(PageLoad.isFailure(android.webkit.WebViewClient.ERROR_CONNECT, "net::ERR_CONNECTION_REFUSED"))
        assertTrue(PageLoad.isFailure(android.webkit.WebViewClient.ERROR_TIMEOUT, "net::ERR_TIMED_OUT"))
        assertTrue(PageLoad.isFailure(android.webkit.WebViewClient.ERROR_FAILED_SSL_HANDSHAKE, "net::ERR_CERT_AUTHORITY_INVALID"))
        assertFalse(PageLoad.isFailure(android.webkit.WebViewClient.ERROR_UNKNOWN, "net::ERR_ABORTED"))   // a new navigation replaced it
        assertTrue(PageLoad.isFailure(status = 502))
        assertTrue(PageLoad.isFailure(status = 503))
        assertFalse(PageLoad.isFailure(status = 200))
        assertFalse(PageLoad.isFailure(status = 404))
    }
}
