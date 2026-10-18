package lark.bridge

import java.net.URI

/** The web view as the bridge needs it: [WebShell] adapts a `WebView`; tests use a fake. Main thread only. */
interface BridgeView {
    /** A page is loaded (or loading): an event has somewhere to go. */
    val hasPage: Boolean

    /** `WebView.evaluateJavascript(script, null)`. */
    fun evaluate(script: String)
}

/**
 * The web ↔ native bridge (iOS `BridgeController`): defines `window.larkNative` at document start, accepts messages
 * from the server's main frame only, and dispatches [NativeEvent]s to the page.
 *
 * [WebShell] installs [userScript] with `WebViewCompat.addDocumentStartJavaScript` and the `lark` listener with
 * `WebViewCompat.addWebMessageListener`, both for [allowedOrigin] only; [onMessage] checks the frame and the origin
 * again, so a same-origin iframe (which gets the `lark` object too) or a message from anywhere else is dropped silently.
 * No message body is ever logged: it is page data, and a stray field could hold anything.
 */
class BridgeController(val serverUrl: String, private val router: (WebMessage) -> Unit) {
    private var view: BridgeView? = null

    /**
     * False while the Activity is stopped: the page is not on screen and may be frozen, so events are dropped, and the
     * app services re-send both queues and states when it starts again (`emitAll`).
     */
    var isActive = true

    /** The web view of this server's page, or null when the Activity let it go. */
    fun attach(view: BridgeView?) { this.view = view }

    /** Dispatches [event] to the page. A no-op with no page, no view, or while inactive. */
    fun send(event: NativeEvent) {
        if (!isActive) return
        val v = view ?: return
        if (!v.hasPage) return
        v.evaluate(dispatchScript(event))
    }

    /** A message from `lark.postMessage`: [sourceOrigin] and [isMainFrame] as the web view reports them. */
    fun onMessage(data: String?, sourceOrigin: String, isMainFrame: Boolean) {
        if (!isMainFrame || !originMatches(sourceOrigin, serverUrl)) return
        val msg = WebMessage.decode(data ?: return) ?: return
        router(msg)
    }

    companion object {
        const val HANDLER_NAME = "lark"

        /**
         * `window.larkNative` (as iOS, plus `platform`): defined once, in the top frame only, not writable or
         * configurable and frozen, so the page can't replace it or its `post`. `post` sends the message as JSON over
         * the `lark` object the listener injects (captured now, so a page script reassigning `window.lark` later
         * changes nothing).
         */
        val userScript = """
            (function () {
              if (window !== window.top || window.larkNative) return;
              var port = window.$HANDLER_NAME;
              Object.defineProperty(window, "larkNative", { value: Object.freeze({
                version: 1,
                platform: "android",
                post: function (msg) { var p = port || window.$HANDLER_NAME; if (p) p.postMessage(JSON.stringify(msg)); }
              }) });
            })();
        """.trimIndent()

        private fun effectivePort(port: Int, scheme: String): Int? = when {
            port > 0 -> port
            scheme == "https" -> 443
            scheme == "http" -> 80
            else -> null
        }

        /** [port] 0 or -1 is the scheme's default. */
        fun originMatches(scheme: String, host: String, port: Int, server: String): Boolean {
            val s = try { URI(server) } catch (_: Exception) { return false }
            val sScheme = s.scheme?.lowercase() ?: return false
            val sHost = s.host?.lowercase() ?: return false
            val sc = scheme.lowercase()
            return sc == sScheme && host.lowercase() == sHost && effectivePort(port, sc) == effectivePort(s.port, sScheme)
        }

        /** A frame's origin as the web view reports it (`scheme://host[:port]`; `null` for an opaque one). */
        fun originMatches(origin: String, server: String): Boolean {
            val o = NavigationPolicy.webOrigin(origin) ?: return false
            return originMatches(o.scheme, o.host, o.port, server)
        }

        /** The server's origin as an allowed-origin rule: `scheme://host[:port]` (the port only when it is not the default). */
        fun allowedOrigin(server: String): String {
            val s = URI(server)
            val scheme = s.scheme.lowercase()
            val port = if (s.port > 0 && s.port != effectivePort(-1, scheme)) ":${s.port}" else ""
            return "$scheme://${s.host.lowercase()}$port"
        }
    }
}
