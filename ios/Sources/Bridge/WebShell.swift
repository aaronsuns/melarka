import SwiftUI
import WebKit

/// The full-screen web app. The `WKWebView` itself is owned by `AppServices`, so SwiftUI updates never
/// recreate it; this view only installs the delegates.
struct WebShell: UIViewRepresentable {
    let webView: WKWebView
    let serverURL: URL
    /// The web view's cookies changed: re-harvest the token.
    let onCookiesChanged: @MainActor () -> Void
    /// The page failed to load, or loaded (the native error screen).
    var onLoadFailed: @MainActor (Error) -> Void = { _ in }
    var onLoaded: @MainActor () -> Void = {}

    func makeCoordinator() -> Coordinator {
        let c = Coordinator(serverURL: serverURL, onCookiesChanged: onCookiesChanged)
        c.onLoadFailed = onLoadFailed; c.onLoaded = onLoaded
        return c
    }

    func makeUIView(context: Context) -> WKWebView {
        webView.allowsBackForwardNavigationGestures = true
        webView.navigationDelegate = context.coordinator
        webView.uiDelegate = context.coordinator
        webView.configuration.websiteDataStore.httpCookieStore.add(context.coordinator)
        if webView.url == nil { webView.load(URLRequest(url: serverURL)) }
        return webView
    }

    func updateUIView(_ uiView: WKWebView, context: Context) {}

    static func dismantleUIView(_ uiView: WKWebView, coordinator: Coordinator) {
        uiView.configuration.websiteDataStore.httpCookieStore.remove(coordinator)
    }

    @MainActor final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate, WKHTTPCookieStoreObserver {
        let serverURL: URL
        let onCookiesChanged: @MainActor () -> Void
        var onLoadFailed: @MainActor (Error) -> Void = { _ in }
        var onLoaded: @MainActor () -> Void = {}

        init(serverURL: URL, onCookiesChanged: @escaping @MainActor () -> Void) {
            self.serverURL = serverURL
            self.onCookiesChanged = onCookiesChanged
        }

        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
            guard let url = action.request.url else { return .cancel }
            let target: NavigationPolicy.Target
            switch action.targetFrame {
            case nil: target = .newWindow
            case let f? where f.isMainFrame: target = .mainFrame
            default: target = .subframe
            }
            switch NavigationPolicy.decide(url, server: serverURL, target: target,
                                           userActivated: action.navigationType == .linkActivated) {
            case .allow:
                return .allow
            case .cancel:
                return .cancel
            case .openExternally:
                await UIApplication.shared.open(url)
                return .cancel
            case .loadInMainFrame:
                webView.load(URLRequest(url: url))   // a fresh GET of a same-origin http(s) URL, nothing else
                return .cancel
            }
        }

        /// A 5xx for the page itself is the error screen, not the proxy's error page.
        func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse) async -> WKNavigationResponsePolicy {
            if response.isForMainFrame, let status = (response.response as? HTTPURLResponse)?.statusCode,
               PageLoad.isFailure(status: status) {
                onLoadFailed(URLError(.badServerResponse))
                return .cancel
            }
            return .allow
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            onLoadFailed(error)
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            onLoadFailed(error)
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            onLoaded()
        }

        func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
            // A crash before the first commit leaves no URL, and reload() would do nothing.
            if webView.url == nil { webView.load(URLRequest(url: serverURL)) } else { webView.reload() }
        }

        nonisolated func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
            Task { @MainActor in self.onCookiesChanged() }
        }

        // `window.confirm` / `alert` (e.g. unfollowing a channel) need a UI delegate, or they return false silently.
        func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                     initiatedByFrame frame: WKFrameInfo) async {
            guard fromServer(frame) else { return }
            await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
                let a = UIAlertController(title: nil, message: message, preferredStyle: .alert)
                a.addAction(UIAlertAction(title: "好", style: .default) { _ in done.resume() })
                present(a, from: webView) { done.resume() }
            }
        }

        func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                     initiatedByFrame frame: WKFrameInfo) async -> Bool {
            guard fromServer(frame) else { return false }
            return await withCheckedContinuation { (done: CheckedContinuation<Bool, Never>) in
                let a = UIAlertController(title: nil, message: message, preferredStyle: .alert)
                a.addAction(UIAlertAction(title: "取消", style: .cancel) { _ in done.resume(returning: false) })
                a.addAction(UIAlertAction(title: "好", style: .default) { _ in done.resume(returning: true) })
                present(a, from: webView) { done.resume(returning: false) }
            }
        }

        /// Panels only for the server's own frames: a cross-origin iframe must not raise native-looking Lark dialogs.
        private func fromServer(_ frame: WKFrameInfo) -> Bool {
            let o = frame.securityOrigin
            return BridgeController.originMatches(scheme: o.protocol, host: o.host, port: o.port, server: serverURL)
        }

        /// Presents `alert`, or calls `fallback` when it can't be shown, so the page's synchronous
        /// alert()/confirm() never waits forever.
        private func present(_ alert: UIAlertController, from webView: WKWebView, orElse fallback: () -> Void) {
            var top = webView.window?.rootViewController
            while let p = top?.presentedViewController, !p.isBeingDismissed { top = p }
            guard let top, top.view.window != nil, !top.isBeingDismissed, top.presentedViewController == nil else {
                return fallback()
            }
            top.present(alert, animated: true)
        }
    }
}
