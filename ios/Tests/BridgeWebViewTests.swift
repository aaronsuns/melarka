import XCTest
import WebKit
@testable import Lark

final class BridgeWebViewTests: XCTestCase {
    @MainActor func testUserScriptAndRoundTrip() async throws {
        var got: [WebMessage] = []
        let bridge = BridgeController(serverURL: URL(string: "https://lark.test/")!) { got.append($0) }
        let web = try onScreenWebView(bridge.makeConfiguration())
        bridge.attach(web)
        let loaded = expectation(description: "loaded"); let nav = OneShotNavDelegate { loaded.fulfill() }; web.navigationDelegate = nav
        web.loadHTMLString("<html><body>t</body></html>", baseURL: URL(string: "https://lark.test/")!)
        await fulfillment(of: [loaded], timeout: 10)
        let v = try await web.evaluateJavaScript("typeof window.larkNative.post + ':' + window.larkNative.version") as? String
        XCTAssertEqual(v, "function:1")
        _ = try await web.evaluateJavaScript("window.larkNative.post({type:'play', kind:'track'}); 0")
        try await waitUntil { got == [.play(.track)] }
        // native → web
        _ = try await web.evaluateJavaScript("window.__seen=null; addEventListener('lark-native', e => window.__seen = e.detail.type); 0")
        bridge.send(.authRequired)
        try await waitUntil { (try? await web.evaluateJavaScript("window.__seen") as? String) == "authRequired" }
        withExtendedLifetime(nav) {}
    }

    @MainActor private func load(_ web: WKWebView, _ html: String, base: String) async {
        let loaded = expectation(description: "loaded \(base)"); let nav = OneShotNavDelegate { loaded.fulfill() }; web.navigationDelegate = nav
        web.loadHTMLString(html, baseURL: URL(string: base)!)
        await fulfillment(of: [loaded], timeout: 10)
        withExtendedLifetime(nav) {}
    }

    @MainActor func testMessagesFromOtherOriginsAreDropped() async throws {
        var got: [WebMessage] = []
        let bridge = BridgeController(serverURL: URL(string: "https://lark.test/")!) { got.append($0) }
        let web = try onScreenWebView(bridge.makeConfiguration())
        bridge.attach(web)
        await load(web, "<html><body>t</body></html>", base: "https://evil.test/")
        _ = try await web.evaluateJavaScript("window.larkNative.post({type:'openSettings'}); 0")
        // Control: a post from the server origin, sent after the evil one over the same channel.
        // Once it has arrived, the evil post would have arrived before it.
        await load(web, "<html><body>t</body></html>", base: "https://lark.test/")
        _ = try await web.evaluateJavaScript("window.larkNative.post({type:'play', kind:'track'}); 0")
        try await waitUntil { !got.isEmpty }
        XCTAssertEqual(got, [.play(.track)])
    }

    @MainActor func testMessagesFromSubframesAreDropped() async throws {
        // A same-origin srcdoc iframe has the server's origin, and `webkit.messageHandlers` is exposed to every frame:
        // only the isMainFrame guard stops it.
        var got: [WebMessage] = []
        let bridge = BridgeController(serverURL: URL(string: "https://lark.test/")!) { got.append($0) }
        let web = try onScreenWebView(bridge.makeConfiguration())
        bridge.attach(web)
        await load(web, """
            <html><body><iframe srcdoc="<script>webkit.messageHandlers.lark.postMessage({type:'openSettings'}); parent.__ran = 1</script>"></iframe></body></html>
            """, base: "https://lark.test/")
        try await waitUntil { (try? await web.evaluateJavaScript("window.__ran === 1") as? Bool) == true }
        _ = try await web.evaluateJavaScript("window.larkNative.post({type:'play', kind:'track'}); 0")   // control
        try await waitUntil { !got.isEmpty }
        XCTAssertEqual(got, [.play(.track)])
    }

    @MainActor func testUserScriptCannotBeReplaced() async throws {
        let bridge = BridgeController(serverURL: URL(string: "https://lark.test/")!) { _ in }
        let web = try onScreenWebView(bridge.makeConfiguration())
        bridge.attach(web)
        let loaded = expectation(description: "loaded"); let nav = OneShotNavDelegate { loaded.fulfill() }; web.navigationDelegate = nav
        web.loadHTMLString("<html><body>t</body></html>", baseURL: URL(string: "https://lark.test/")!)
        await fulfillment(of: [loaded], timeout: 10)
        let v = try await web.evaluateJavaScript(
            "try { window.larkNative = 1 } catch (e) {}; try { window.larkNative.post = null } catch (e) {}; typeof window.larkNative.post") as? String
        XCTAssertEqual(v, "function")
        withExtendedLifetime(nav) {}
    }

    @MainActor func testSendIsANoOpWhileInactive() async throws {
        let bridge = BridgeController(serverURL: URL(string: "https://lark.test/")!) { _ in }
        let web = try onScreenWebView(bridge.makeConfiguration())
        bridge.attach(web)
        let loaded = expectation(description: "loaded"); let nav = OneShotNavDelegate { loaded.fulfill() }; web.navigationDelegate = nav
        web.loadHTMLString("<html><body>t</body></html>", baseURL: URL(string: "https://lark.test/")!)
        await fulfillment(of: [loaded], timeout: 10)
        _ = try await web.evaluateJavaScript("window.__n=0; addEventListener('lark-native', () => window.__n++); 0")
        bridge.isActive = false
        bridge.send(.authRequired)
        bridge.isActive = true
        bridge.send(.flushed(id: "x"))
        try await waitUntil { (try? await web.evaluateJavaScript("window.__n") as? Int) == 1 }
        withExtendedLifetime(nav) {}
    }

    func testOriginMatching() {
        let server = URL(string: "https://lark.test/")!
        XCTAssertTrue(BridgeController.originMatches(scheme: "https", host: "lark.test", port: 0, server: server))
        XCTAssertTrue(BridgeController.originMatches(scheme: "https", host: "lark.test", port: 443, server: server))
        XCTAssertFalse(BridgeController.originMatches(scheme: "https", host: "lark.test", port: 8443, server: server))
        XCTAssertFalse(BridgeController.originMatches(scheme: "http", host: "lark.test", port: 0, server: server))
        XCTAssertFalse(BridgeController.originMatches(scheme: "https", host: "evil.lark.test", port: 0, server: server))
        let dev = URL(string: "http://mac.local:8080")!
        XCTAssertTrue(BridgeController.originMatches(scheme: "http", host: "mac.local", port: 8080, server: dev))
        XCTAssertFalse(BridgeController.originMatches(scheme: "http", host: "mac.local", port: 0, server: dev))
    }

    func testNavigationPolicy() {
        let server = URL(string: "https://lark.test/")!
        func d(_ s: String, _ target: NavigationPolicy.Target = .mainFrame, tapped: Bool = true) -> NavigationPolicy {
            NavigationPolicy.decide(URL(string: s)!, server: server, target: target, userActivated: tapped)
        }
        // main frame
        XCTAssertEqual(d("https://lark.test/library"), .allow)
        XCTAssertEqual(d("https://lark.test/library", tapped: false), .allow)
        XCTAssertEqual(d("https://www.youtube.com/watch?v=x"), .openExternally)
        XCTAssertEqual(d("http://lark.test/"), .openExternally)                         // not the server's origin
        XCTAssertEqual(d("mailto:a@b.c"), .openExternally)
        XCTAssertEqual(d("about:blank"), .allow)
        XCTAssertEqual(d("data:text/html,<h1>sign in</h1>"), .cancel)
        XCTAssertEqual(d("javascript:alert(1)"), .cancel)
        XCTAssertEqual(d("blob:https://lark.test/abc"), .allow)
        XCTAssertEqual(d("blob:https://evil.test/abc"), .cancel)
        XCTAssertEqual(d("about:srcdoc"), .cancel)
        // subframes: the 视频 YouTube embed and in-page frames stay in the page
        XCTAssertEqual(d("https://www.youtube.com/embed/x", .subframe, tapped: false), .allow)
        XCTAssertEqual(d("about:blank", .subframe, tapped: false), .allow)
        XCTAssertEqual(d("data:text/html,x", .subframe, tapped: false), .allow)
        XCTAssertEqual(d("blob:https://evil.test/abc", .subframe, tapped: false), .allow)
        XCTAssertEqual(d("tel:123", .subframe, tapped: false), .cancel)                  // no app launch without a tap
        XCTAssertEqual(d("itms-services://?action=download-manifest", .subframe, tapped: false), .cancel)
        XCTAssertEqual(d("mailto:a@b.c", .subframe, tapped: true), .openExternally)
        // new windows (target=_blank, window.open): only same-origin http(s) loads in place
        XCTAssertEqual(d("https://lark.test/x", .newWindow), .loadInMainFrame)
        XCTAssertEqual(d("https://www.youtube.com/watch?v=x", .newWindow), .openExternally)
        XCTAssertEqual(d("https://github.com/aaronsuns/melarka", .newWindow), .openExternally)   // the web settings' "Source code" link opens in Safari
        XCTAssertEqual(d("data:text/html,<h1>sign in</h1>", .newWindow), .cancel)
        XCTAssertEqual(d("javascript:alert(1)", .newWindow), .cancel)
        XCTAssertEqual(d("blob:https://lark.test/abc", .newWindow), .cancel)
        XCTAssertEqual(d("about:blank", .newWindow), .cancel)
        XCTAssertEqual(d("tel:123", .newWindow, tapped: false), .cancel)
    }
}
