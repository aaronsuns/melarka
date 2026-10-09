import Foundation
import WebKit
import os

/// The web ↔ native bridge: injects `window.larkNative`, accepts messages from the server's origin only,
/// and dispatches `NativeEvent`s to the page.
@MainActor final class BridgeController: NSObject, WKScriptMessageHandler {
    static let handlerName = "lark"
    static let userScript = """
    (function () {
      if (window.larkNative) return;
      Object.defineProperty(window, "larkNative", { value: Object.freeze({
        version: 1,
        post: function (msg) { window.webkit.messageHandlers.lark.postMessage(msg); }
      }) });
    })();
    """
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "bridge")

    let serverURL: URL
    private let router: (WebMessage) -> Void
    private weak var webView: WKWebView?
    /// False while the scene is in the background: the page's JS is frozen then, so events are dropped,
    /// and `AppServices` re-sends `queue` and `state` when the scene becomes active again.
    var isActive = true

    init(serverURL: URL, router: @escaping (WebMessage) -> Void) {
        self.serverURL = serverURL
        self.router = router
    }

    func makeConfiguration() -> WKWebViewConfiguration {
        let config = WKWebViewConfiguration()
        let ucc = WKUserContentController()
        ucc.addUserScript(WKUserScript(source: Self.userScript, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        ucc.add(WeakScriptMessageHandler(self), name: Self.handlerName)   // the controller retains its handlers
        config.userContentController = ucc
        config.allowsInlineMediaPlayback = true
        config.mediaTypesRequiringUserActionForPlayback = []
        config.websiteDataStore = .default()
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
        config.applicationNameForUserAgent = "LarkiOS/\(version)"
        return config
    }

    func attach(_ webView: WKWebView) { self.webView = webView }

    /// Dispatches `event` to the page. A no-op with no loaded page or while the scene is inactive.
    func send(_ event: NativeEvent) {
        guard isActive, let webView, webView.url != nil else { return }
        webView.evaluateJavaScript(event.javaScript()) { _, error in
            if error != nil { Self.log.debug("bridge event dispatch failed") }
        }
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        let origin = message.frameInfo.securityOrigin
        guard message.frameInfo.isMainFrame,
              Self.originMatches(scheme: origin.protocol, host: origin.host, port: origin.port, server: serverURL) else {
            Self.log.debug("dropped a bridge message from another frame or origin")
            return
        }
        // The body is never logged: it is page data, and a stray field could hold anything.
        guard let msg = try? WebMessage.decode(message.body) else {
            Self.log.debug("dropped an undecodable bridge message")
            return
        }
        router(msg)
    }

    /// `port` is the `WKSecurityOrigin` port, 0 for the scheme's default.
    nonisolated static func originMatches(scheme: String, host: String, port: Int, server: URL) -> Bool {
        guard let sScheme = server.scheme?.lowercased(), let sHost = server.host?.lowercased() else { return false }
        func effective(_ port: Int?, _ scheme: String) -> Int? {
            if let port, port != 0 { return port }
            switch scheme { case "https": return 443; case "http": return 80; default: return nil }
        }
        return scheme.lowercased() == sScheme && host.lowercased() == sHost
            && effective(port, scheme.lowercased()) == effective(server.port, sScheme)
    }
}

/// Breaks the WKUserContentController → handler retain cycle.
private final class WeakScriptMessageHandler: NSObject, WKScriptMessageHandler {
    weak var target: WKScriptMessageHandler?
    init(_ target: WKScriptMessageHandler) { self.target = target }
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(ucc, didReceive: message)
    }
}

enum NavigationPolicy: Equatable {
    case allow, cancel, openExternally
    /// A same-origin new window: there are no tabs, so it loads in the one web view.
    case loadInMainFrame

    enum Target { case mainFrame, subframe, newWindow }

    /// - main frame: only the server's own http(s) origin (and same-origin `blob:`, `about:blank`) loads; other
    ///   http(s) origins and app schemes open outside the app; `data:`, `javascript:` and foreign blobs are dropped,
    ///   so no page content an attacker controls can replace the Lark UI.
    /// - new window (`target=_blank`, `window.open`): only same-origin http(s) loads, in the main frame.
    /// - subframes (the 视频 YouTube embed): web and in-page schemes stay in the page; app schemes need a tap.
    static func decide(_ url: URL, server: URL, target: Target, userActivated: Bool) -> NavigationPolicy {
        let scheme = url.scheme?.lowercased() ?? ""
        let isWeb = scheme == "http" || scheme == "https"
        let inPage = ["about", "blob", "data", "javascript"].contains(scheme)
        let sameOrigin = isWeb && url.host.map {
            BridgeController.originMatches(scheme: scheme, host: $0, port: url.port ?? 0, server: server)
        } == true
        // Apps (mailto:, tel:, itms-services:…) launch only from the main frame or on a tap.
        let external: NavigationPolicy = (target == .mainFrame || userActivated) ? .openExternally : .cancel

        switch target {
        case .subframe:
            return isWeb || inPage ? .allow : external
        case .newWindow:
            if sameOrigin { return .loadInMainFrame }
            if isWeb { return .openExternally }
            return inPage ? .cancel : external
        case .mainFrame:
            if sameOrigin { return .allow }
            if isWeb { return .openExternally }
            switch scheme {
            case "about": return url.absoluteString.lowercased() == "about:blank" ? .allow : .cancel
            case "blob":
                // blob:<origin>/<uuid>: allowed only when the inner origin is the server's.
                guard let inner = URL(string: String(url.absoluteString.dropFirst("blob:".count))),
                      let host = inner.host, let innerScheme = inner.scheme?.lowercased() else { return .cancel }
                return BridgeController.originMatches(scheme: innerScheme, host: host, port: inner.port ?? 0, server: server)
                    ? .allow : .cancel
            case "data", "javascript": return .cancel
            default: return external
            }
        }
    }
}
