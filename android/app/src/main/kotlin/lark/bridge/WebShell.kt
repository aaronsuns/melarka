package lark.bridge

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Bitmap
import android.os.Handler
import android.os.Looper
import android.os.Message
import android.util.Log
import android.view.View
import android.view.ViewGroup
import android.webkit.JsPromptResult
import android.webkit.JsResult
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import lark.app.BuildConfig

/**
 * The full-screen web app (iOS `Bridge/WebShell.swift`): one `WebView` for the server, with the bridge installed
 * ([BridgeController.userScript] at document start and the `lark` message listener, both for the server's origin only),
 * the navigation policy, and the load outcomes the app services turn into the native error screen. The Activity owns it
 * (created in `onCreate`, destroyed in `onDestroy`); the bridge and everything behind it are the app's.
 *
 * The decisions live in [Client] and [Chrome], which take no `WebView` for them, so they are unit-tested without one.
 */
@SuppressLint("SetJavaScriptEnabled")   // the web app is the UI; only the server's origin gets the bridge
class WebShell(context: Context, val server: String, private val bridge: BridgeController, host: Host) {
    /** What the shell needs from the Activity and the app services. Main thread. */
    interface Host {
        /** The page failed to load (a main-frame error that [PageLoad] counts). */
        fun loadFailed(errorCode: Int, description: String?)
        /** The page itself answered a 5xx. */
        fun httpFailed(status: Int)
        /** A page finished loading with no error: its cookies may have changed too. */
        fun loaded(url: String)
        /** Another origin or an app scheme: the browser or the app for it. */
        fun openExternally(url: String)
        /** The renderer died (or was killed for memory): this shell is dead; the Activity makes a new one. */
        fun rendererGone()
        /** `alert` (OK) or `confirm` (Cancel, OK) for the server's page; [done] runs exactly once. */
        fun dialog(message: String, cancellable: Boolean, done: (Boolean) -> Unit)
        /** A video's full screen (the 视频 YouTube embed). */
        fun showFullscreen(view: View, callback: WebChromeClient.CustomViewCallback)
        fun hideFullscreen()
    }

    val view: WebView = WebView(context)
    private val client = Client(server, host)
    private val chrome = Chrome(server, host, client)
    private val pageView = object : BridgeView {
        override val url: String? get() = view.url
        override fun evaluate(script: String) = view.evaluateJavascript(script, null)
    }

    init {
        applySettings(view.settings, BuildConfig.VERSION_NAME)
        view.webViewClient = client
        view.webChromeClient = chrome
        val origin = setOf(BridgeController.allowedOrigin(server))
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(view, BridgeController.HANDLER_NAME, origin) { _, message, sourceOrigin, isMainFrame, _ ->
                bridge.onMessage(message.data, sourceOrigin.toString(), isMainFrame)
            }
        } else {
            Log.w(TAG, "this WebView has no message listener: the page runs without the native bridge")
        }
        if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
            WebViewCompat.addDocumentStartJavaScript(view, BridgeController.userScript, origin)
        } else {
            Log.w(TAG, "this WebView has no document-start scripts: the page runs without the native bridge")
        }
        bridge.attach(pageView)
    }

    fun load(url: String) = view.loadUrl(url)

    fun goBack(): Boolean {
        if (!view.canGoBack()) return false
        view.goBack()
        return true
    }

    /** The Activity is going ([clearData]: the server too, so its HTTP cache and history go). The bridge stays the app's. */
    fun destroy(clearData: Boolean) {
        bridge.detach(pageView)     // only this shell's view: a newer shell may have attached already
        chrome.destroyPopups()
        (view.parent as? ViewGroup)?.removeView(view)
        view.stopLoading()
        if (clearData) { view.clearCache(true); view.clearHistory() }
        view.destroy()
    }

    /**
     * Navigations, load outcomes and the renderer (iOS `WebShell.Coordinator`'s navigation delegate). (The webkit lint
     * check does not see the Kotlin `onRenderProcessGone` override below; `WebShellTest` calls it.)
     */
    @SuppressLint("MissingOnRenderProcessGone")
    class Client(private val server: String, private val host: Host) : WebViewClient() {
        /** The current main-frame load failed: its finish (Chromium's error page) is not a load. */
        private var failed = false

        override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest): Boolean =
            navigate(view, request.url.toString(),
                if (request.isForMainFrame) NavigationPolicy.Target.mainFrame else NavigationPolicy.Target.subframe,
                request.hasGesture()) { view?.loadUrl(it) }

        /** True: the web view must not load [url] itself (cancelled, opened outside, or loaded afresh). */
        internal fun navigate(view: WebView?, url: String, target: NavigationPolicy.Target, gesture: Boolean,
                              loadInMainFrame: (String) -> Unit): Boolean =
            when (NavigationPolicy.decide(url, server, target, userActivated = gesture)) {
                NavigationPolicy.allow -> false
                NavigationPolicy.cancel -> true
                NavigationPolicy.openExternally -> { host.openExternally(url); true }
                NavigationPolicy.loadInMainFrame -> { loadInMainFrame(url); true }   // a fresh GET of a same-origin URL
            }

        override fun onPageStarted(view: WebView?, url: String?, favicon: Bitmap?) { failed = false }

        override fun onReceivedError(view: WebView?, request: WebResourceRequest, error: WebResourceError) {
            if (request.isForMainFrame) mainFrameError(error.errorCode, error.description?.toString())
        }

        internal fun mainFrameError(code: Int, description: String?) {
            if (!PageLoad.isFailure(code, description)) return
            failed = true
            host.loadFailed(code, description)
        }

        /** A 5xx for the page itself is the error screen, not the proxy's error page. */
        override fun onReceivedHttpError(view: WebView?, request: WebResourceRequest, response: WebResourceResponse) {
            if (!request.isForMainFrame || !PageLoad.isFailure(response.statusCode)) return
            failed = true
            host.httpFailed(response.statusCode)
        }

        override fun onPageFinished(view: WebView?, url: String?) {
            if (!failed && url != null) host.loaded(url)
        }

        /** The renderer crashed or was killed: this web view can't be used again (returning false would end the app). */
        override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
            host.rendererGone()
            return true
        }
    }

    /** New windows, dialogs, full screen (iOS `WebShell.Coordinator`'s UI delegate). */
    class Chrome(private val server: String, private val host: Host, private val client: Client) : WebChromeClient() {
        /**
         * `target=_blank` / `window.open`: there are no tabs. The new window's first navigation is caught in a throwaway
         * web view and decided as a new window: same-origin loads in the one web view, other web origins open outside.
         */
        override fun onCreateWindow(view: WebView?, isDialog: Boolean, isUserGesture: Boolean, resultMsg: Message?): Boolean {
            val parent = view ?: return false
            val transport = resultMsg?.obj as? WebView.WebViewTransport ?: return false
            val popup = WebView(parent.context)
            popups += popup
            var decided = false
            fun decide(url: String) {
                if (decided) return
                decided = true
                newWindow(url, isUserGesture) { parent.loadUrl(it) }
                main.post { destroyPopup(popup) }
            }
            // A window that never navigates (window.open() with no URL) is not kept.
            main.postDelayed({ destroyPopup(popup) }, POPUP_TIMEOUT_MS)
            popup.webViewClient = @SuppressLint("MissingOnRenderProcessGone") object : WebViewClient() {   // it has one, below
                override fun shouldOverrideUrlLoading(v: WebView?, request: WebResourceRequest): Boolean {
                    decide(request.url.toString()); return true
                }
                override fun onPageStarted(v: WebView?, url: String?, favicon: Bitmap?) {
                    v?.stopLoading()
                    url?.let { decide(it) }
                }
                override fun onRenderProcessGone(v: WebView, detail: RenderProcessGoneDetail): Boolean {
                    destroyPopup(popup)     // a throwaway: nothing to recover
                    return true
                }
            }
            transport.webView = popup
            resultMsg.sendToTarget()
            return true
        }

        private val popups = mutableListOf<WebView>()
        /** Not the parent view's own queue: a view not attached to a window holds its posts until it is. */
        private val main = Handler(Looper.getMainLooper())

        /** Throwaway new-window web views not yet destroyed. */
        val openPopups: Int get() = popups.size

        private fun destroyPopup(popup: WebView) {
            if (popups.remove(popup)) popup.destroy()
        }

        /** The page closed its new window (`window.close()`). */
        override fun onCloseWindow(window: WebView?) {
            window?.let { destroyPopup(it) }
        }

        /** The shell is going: every new window still open goes with it. */
        fun destroyPopups() = popups.toList().forEach { destroyPopup(it) }

        internal fun newWindow(url: String, gesture: Boolean, loadInMainFrame: (String) -> Unit) {
            client.navigate(null, url, NavigationPolicy.Target.newWindow, gesture, loadInMainFrame)
        }

        override fun onJsAlert(view: WebView?, url: String?, message: String?, result: JsResult): Boolean {
            dialog(url, message.orEmpty(), cancellable = false) { result.confirm() }
            return true
        }

        override fun onJsConfirm(view: WebView?, url: String?, message: String?, result: JsResult): Boolean {
            dialog(url, message.orEmpty(), cancellable = true) { if (it) result.confirm() else result.cancel() }
            return true
        }

        /** No `prompt()` (iOS has none either): it answers null at once. */
        override fun onJsPrompt(view: WebView?, url: String?, message: String?, defaultValue: String?, result: JsPromptResult): Boolean {
            result.cancel()
            return true
        }

        /** Dialogs only for the server's own frames: a cross-origin iframe must not raise native-looking Melarka dialogs. */
        internal fun dialog(frameUrl: String?, message: String, cancellable: Boolean, done: (Boolean) -> Unit) {
            if (frameUrl == null || !BridgeController.originMatches(frameUrl, server)) return done(false)
            host.dialog(message, cancellable, done)
        }

        override fun onShowCustomView(view: View?, callback: CustomViewCallback?) {
            if (view == null || callback == null) return
            host.showFullscreen(view, callback)
        }

        override fun onHideCustomView() = host.hideFullscreen()
    }

    companion object {
        private const val TAG = "WebShell"
        /** A new window that has not navigated by then is given up (its throwaway web view destroyed). */
        const val POPUP_TIMEOUT_MS = 10_000L

        /**
         * The page's settings: JavaScript and DOM storage for the web app; nothing from the phone's files or content
         * providers; no http content inside the https page; Safe Browsing on; new windows only on a tap.
         */
        @Suppress("DEPRECATION")   // the file-URL switches are deprecated (off by default since API 30), set off anyway
        fun applySettings(settings: WebSettings, version: String) {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.mediaPlaybackRequiresUserGesture = false      // previews start from the page's own controls
            settings.setSupportMultipleWindows(true)               // target=_blank and window.open reach onCreateWindow
            settings.javaScriptCanOpenWindowsAutomatically = false // only on a tap, as WKWebView
            settings.allowFileAccess = false
            settings.allowContentAccess = false
            settings.allowFileAccessFromFileURLs = false
            settings.allowUniversalAccessFromFileURLs = false
            settings.mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            settings.safeBrowsingEnabled = true                    // also declared in the manifest (meta-data)
            settings.userAgentString = "${settings.userAgentString} LarkAndroid/$version"
        }
    }
}
