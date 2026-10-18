package lark.bridge

import android.view.View
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebViewClient
import android.net.Uri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * iOS `WebShell.Coordinator`, without a WebView: navigations go through [NavigationPolicy], a main-frame error or 5xx is
 * a failed page (and the finish after it is not a load), a renderer crash is reported, and dialogs come only from the
 * server's frames.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class WebShellTest {
    private class Host : WebShell.Host {
        val log = mutableListOf<String>()
        var answer = true
        override fun loadFailed(errorCode: Int, description: String?) { log += "failed $errorCode $description" }
        override fun httpFailed(status: Int) { log += "http $status" }
        override fun loaded(url: String) { log += "loaded $url" }
        override fun openExternally(url: String) { log += "external $url" }
        override fun rendererGone() { log += "renderer gone" }
        override fun dialog(message: String, cancellable: Boolean, done: (Boolean) -> Unit) { log += "dialog $message $cancellable"; done(answer) }
        override fun showFullscreen(view: View, callback: WebChromeClient.CustomViewCallback) { log += "fullscreen" }
        override fun hideFullscreen() { log += "fullscreen off" }
    }

    private class Req(url: String, private val main: Boolean, private val gesture: Boolean = false) : WebResourceRequest {
        private val u = Uri.parse(url)
        override fun getUrl(): Uri = u
        override fun isForMainFrame() = main
        override fun isRedirect() = false
        override fun hasGesture() = gesture
        override fun getMethod() = "GET"
        override fun getRequestHeaders(): Map<String, String> = emptyMap()
    }

    private val host = Host()
    private val client = WebShell.Client("https://lark.test/", host)
    private val chrome = WebShell.Chrome("https://lark.test/", host, client)

    @Test fun navigationsFollowThePolicy() {
        assertFalse(client.shouldOverrideUrlLoading(null, Req("https://lark.test/library", main = true)))
        assertTrue(client.shouldOverrideUrlLoading(null, Req("https://www.youtube.com/watch?v=x", main = true)))
        assertTrue(client.shouldOverrideUrlLoading(null, Req("data:text/html,<h1>sign in</h1>", main = true)))
        assertFalse(client.shouldOverrideUrlLoading(null, Req("https://www.youtube.com/embed/x", main = false)))
        assertTrue(client.shouldOverrideUrlLoading(null, Req("tel:123", main = false, gesture = false)))
        assertTrue(client.shouldOverrideUrlLoading(null, Req("mailto:a@b.c", main = false, gesture = true)))
        assertEquals(listOf("external https://www.youtube.com/watch?v=x", "external mailto:a@b.c"), host.log)
    }

    /** `target=_blank` / `window.open`: same-origin loads in the one web view, other web origins open outside. */
    @Test fun newWindows() {
        val loads = mutableListOf<String>()
        chrome.newWindow("https://lark.test/x", gesture = true) { loads += it }
        chrome.newWindow("https://github.com/aaronsuns/melarka", gesture = true) { loads += it }
        chrome.newWindow("javascript:alert(1)", gesture = true) { loads += it }
        assertEquals(listOf("https://lark.test/x"), loads)
        assertEquals(listOf("external https://github.com/aaronsuns/melarka"), host.log)
    }

    @Test fun aMainFrameErrorIsAFailedPageAndItsFinishIsNotALoad() {
        client.onPageStarted(null, "https://lark.test/", null)
        client.mainFrameError(WebViewClient.ERROR_HOST_LOOKUP, "net::ERR_NAME_NOT_RESOLVED")
        client.onPageFinished(null, "https://lark.test/")          // Chromium finishes its error page too
        client.onPageStarted(null, "https://lark.test/", null)
        client.onPageFinished(null, "https://lark.test/")
        assertEquals(listOf("failed ${WebViewClient.ERROR_HOST_LOOKUP} net::ERR_NAME_NOT_RESOLVED", "loaded https://lark.test/"), host.log)
    }

    @Test fun anAbortedLoadIsNotAFailure() {
        client.onPageStarted(null, "https://lark.test/", null)
        client.mainFrameError(WebViewClient.ERROR_UNKNOWN, "net::ERR_ABORTED")
        client.onPageFinished(null, "https://lark.test/")
        assertEquals(listOf("loaded https://lark.test/"), host.log)
    }

    /** A 5xx for the page itself is the error screen, not the proxy's error page; a 404 or a subresource's 502 is not. */
    @Test fun aMainFrame5xxIsAFailedPage() {
        val r502 = WebResourceResponse("text/html", "utf-8", 502, "Bad Gateway", emptyMap(), null)
        client.onPageStarted(null, "https://lark.test/", null)
        client.onReceivedHttpError(null, Req("https://lark.test/api/v1/queue", main = false), r502)
        client.onReceivedHttpError(null, Req("https://lark.test/", main = true),
            WebResourceResponse("text/html", "utf-8", 404, "Not Found", emptyMap(), null))
        client.onPageFinished(null, "https://lark.test/")
        client.onPageStarted(null, "https://lark.test/", null)
        client.onReceivedHttpError(null, Req("https://lark.test/", main = true), r502)
        client.onPageFinished(null, "https://lark.test/")
        assertEquals(listOf("loaded https://lark.test/", "http 502"), host.log)
    }

    @Test fun aRendererCrashIsReported() {
        @Suppress("DEPRECATION")   // the framework makes these; a test has to make its own
        val gone = object : android.webkit.RenderProcessGoneDetail() {
            override fun didCrash() = true
            override fun rendererPriorityAtExit() = 0
        }
        val view = android.webkit.WebView(androidx.test.core.app.ApplicationProvider.getApplicationContext())
        assertTrue(client.onRenderProcessGone(view, gone))
        assertEquals(listOf("renderer gone"), host.log)
    }

    /** `alert`/`confirm` need a native dialog (unfollowing a channel), but only for the server's own frames. */
    @Test fun dialogsOnlyForTheServersFrames() {
        val answers = mutableListOf<Boolean>()
        chrome.dialog("https://lark.test/channels", "Unfollow?", cancellable = true) { answers += it }
        host.answer = false
        chrome.dialog("https://lark.test/", "Sure?", cancellable = true) { answers += it }
        chrome.dialog("https://www.youtube.com/embed/x", "Hi from an iframe", cancellable = false) { answers += it }
        chrome.dialog("about:srcdoc", "Hi", cancellable = false) { answers += it }
        assertEquals(listOf(true, false, false, false), answers)
        assertEquals(listOf("dialog Unfollow? true", "dialog Sure? true"), host.log)
    }
}
