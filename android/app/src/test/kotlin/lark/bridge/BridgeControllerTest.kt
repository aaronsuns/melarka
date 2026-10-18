package lark.bridge

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * iOS `BridgeWebViewTests` without a web view: what the page posts reaches the router only from the server's main
 * frame, and events go to the page as `dispatchScript` while the Activity is started. The script itself runs in the
 * emulator smoke test (Task 14); here its text is checked.
 */
class BridgeControllerTest {
    private class FakeView(override var url: String? = "https://lark.test/library") : BridgeView {
        val scripts = mutableListOf<String>()
        override fun evaluate(script: String) { scripts += script }
    }

    private val got = mutableListOf<WebMessage>()
    private val bridge = BridgeController("https://lark.test/") { got += it }
    private val view = FakeView().also { bridge.attach(it) }

    @Test fun roundTrip() {
        bridge.onMessage("""{"type":"play","kind":"track"}""", "https://lark.test", isMainFrame = true)
        assertEquals(listOf(WebMessage.Play(Item.Kind.track)), got)
        // native → web
        bridge.send(NativeEvent.AuthRequired)
        assertEquals(listOf(dispatchScript(NativeEvent.AuthRequired)), view.scripts)
    }

    @Test fun messagesFromOtherOriginsAreDropped() {
        bridge.onMessage("""{"type":"openSettings"}""", "https://evil.test", isMainFrame = true)
        bridge.onMessage("""{"type":"openSettings"}""", "http://lark.test", isMainFrame = true)
        bridge.onMessage("""{"type":"openSettings"}""", "https://lark.test:8443", isMainFrame = true)
        bridge.onMessage("""{"type":"openSettings"}""", "null", isMainFrame = true)
        bridge.onMessage("""{"type":"play","kind":"track"}""", "https://lark.test", isMainFrame = true)   // control
        assertEquals(listOf(WebMessage.Play(Item.Kind.track)), got)
    }

    /** A same-origin iframe gets the `lark` object too (the listener's rule is per origin): only the main-frame check stops it. */
    @Test fun messagesFromSubframesAreDropped() {
        bridge.onMessage("""{"type":"openSettings"}""", "https://lark.test", isMainFrame = false)
        bridge.onMessage("""{"type":"play","kind":"track"}""", "https://lark.test", isMainFrame = true)   // control
        assertEquals(listOf(WebMessage.Play(Item.Kind.track)), got)
    }

    @Test fun undecodableOrMissingBodiesAreDropped() {
        bridge.onMessage(null, "https://lark.test", isMainFrame = true)
        bridge.onMessage("not json", "https://lark.test", isMainFrame = true)
        bridge.onMessage("""{"type":"rm -rf"}""", "https://lark.test", isMainFrame = true)
        bridge.onMessage("[" .repeat(10_000), "https://lark.test", isMainFrame = true)
        assertTrue(got.isEmpty())
    }

    /** A closed bridge (a server change) routes nothing and sends nothing, even with its old view attached again. */
    @Test fun aClosedBridgeIsInert() {
        bridge.close()
        bridge.attach(view)
        bridge.onMessage("""{"type":"play","kind":"track"}""", "https://lark.test", isMainFrame = true)
        bridge.send(NativeEvent.AuthRequired)
        assertTrue(got.isEmpty())
        assertTrue(view.scripts.isEmpty())
    }

    @Test fun sendIsANoOpWhileInactive() {
        bridge.isActive = false
        bridge.send(NativeEvent.AuthRequired)
        bridge.isActive = true
        bridge.send(NativeEvent.Flushed("x"))
        assertEquals(listOf(dispatchScript(NativeEvent.Flushed("x"))), view.scripts)
    }

    @Test fun sendIsANoOpWithNoPageOrNoView() {
        view.url = null
        bridge.send(NativeEvent.AuthRequired)
        assertTrue(view.scripts.isEmpty())
        bridge.attach(null)
        bridge.send(NativeEvent.AuthRequired)                 // the Activity is gone: nothing to evaluate in
        assertTrue(view.scripts.isEmpty())
    }

    /** The web view went somewhere else (an error page, about:blank, another origin): nothing is dispatched there. */
    @Test fun eventsGoOnlyToTheServersPage() {
        for (url in listOf("https://evil.test/", "about:blank", "http://lark.test/", "data:text/html,x", "chrome-error://chromewebdata/")) {
            view.url = url
            bridge.send(NativeEvent.AuthRequired)
        }
        assertTrue(view.scripts.isEmpty())
        view.url = "https://lark.test:443/x?y"
        bridge.send(NativeEvent.AuthRequired)
        assertEquals(1, view.scripts.size)
    }

    /** A shell going away detaches only its own view: a newer shell (after a renderer crash) keeps the bridge. */
    @Test fun detachingAnOldViewKeepsTheNewOne() {
        val newer = FakeView()
        bridge.attach(newer)
        bridge.detach(view)
        bridge.send(NativeEvent.AuthRequired)
        assertEquals(1, newer.scripts.size)
        bridge.detach(newer)
        bridge.send(NativeEvent.AuthRequired)
        assertEquals(1, newer.scripts.size)
    }

    /** `window.larkNative = {version:1, platform:"android", post}`, frozen, not replaceable, the main frame only. */
    @Test fun userScript() {
        val s = BridgeController.userScript
        assertTrue(s.contains("Object.defineProperty(window, \"larkNative\""))   // not writable, not configurable: can't be replaced
        assertTrue(s.contains("Object.freeze("))                                // nor its post
        assertTrue(s.contains("version: 1"))
        assertTrue(s.contains("platform: \"android\""))
        assertTrue(s.contains("postMessage(JSON.stringify(msg))"))             // the listener takes strings
        assertTrue(s.contains("window !== window.top"))                         // as iOS forMainFrameOnly
        assertTrue(s.contains("window.larkNative) return"))                     // once
        assertEquals("lark", BridgeController.HANDLER_NAME)
    }
}
