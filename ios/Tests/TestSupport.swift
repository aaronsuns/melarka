import XCTest
import UIKit
import WebKit
import AVFoundation

struct WaitTimeout: Error, CustomStringConvertible {
    let seconds: TimeInterval
    let file: StaticString
    let line: UInt
    let state: String?
    var description: String {
        "condition not met within \(seconds) s (\((("\(file)" as NSString).lastPathComponent)):\(line))" + (state.map { "; \($0)" } ?? "")
    }
}

/// Polls `condition` every 50 ms until it is true, or throws after `timeout` seconds. The error names the
/// call site and, when `state` is given, what it describes at that moment.
@MainActor func waitUntil(timeout: TimeInterval = 5, file: StaticString = #filePath, line: UInt = #line,
                          state: (@MainActor () -> String)? = nil, _ condition: () async throws -> Bool) async throws {
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if try await condition() { return }
        try await Task.sleep(nanoseconds: 50_000_000)
    }
    if try await condition() { return }
    throw WaitTimeout(seconds: timeout, file: file, line: line, state: state?())
}

extension XCTestCase {
    /// A web view in a visible window of the test host, for as long as the test runs. iOS treats a web view
    /// outside any window as hidden and lets its web content process be throttled and suspended while the test
    /// waits on it, so loads and script calls then take as long as the system pleases.
    @MainActor func onScreenWebView(_ configuration: WKWebViewConfiguration) throws -> WKWebView {
        let web = WKWebView(frame: .zero, configuration: configuration)
        try putOnScreen(web)
        return web
    }

    /// Hosts `web` (one the code under test made) in a visible window of the test host until the test ends.
    @MainActor func putOnScreen(_ web: WKWebView) throws {
        guard web.window == nil else { return }
        let scene = try XCTUnwrap(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first,
                                  "the test host has no window scene")
        let window = UIWindow(windowScene: scene)
        web.frame = window.bounds
        window.addSubview(web)
        window.isHidden = false
        addTeardownBlock { @MainActor in
            web.removeFromSuperview()
            window.isHidden = true
        }
    }

    /// Loads `html` at `base` in `web`, on screen, and waits for that navigation to finish: its own
    /// `didFinish`, or an error naming why it failed (a failed navigation, a web content process that died).
    /// The deadline only bounds a broken simulator: a loaded CI machine can take many seconds to start WebKit.
    @MainActor func loadHTML(_ web: WKWebView, _ html: String, base: URL, timeout: TimeInterval = 60,
                             file: StaticString = #filePath, line: UInt = #line) async throws {
        try putOnScreen(web)
        let nav = NavigationWaiter()
        let previous = web.navigationDelegate
        web.navigationDelegate = nav
        defer { web.navigationDelegate = previous }
        nav.navigation = web.loadHTMLString(html, baseURL: base)
        try await waitUntil(timeout: timeout, file: file, line: line, state: { "loading \(base): \(nav.state)" }) { nav.finished || nav.failure != nil }
        if let failure = nav.failure { throw NavigationFailed(base: base, reason: failure) }
    }
}

struct NavigationFailed: Error, CustomStringConvertible {
    let base: URL
    let reason: String
    var description: String { "loading \(base) failed: \(reason)" }
}

/// Follows one navigation: finished, failed, or its web content process gone.
final class NavigationWaiter: NSObject, WKNavigationDelegate {
    var navigation: WKNavigation?
    private(set) var finished = false
    private(set) var failure: String?
    private(set) var state = "started"
    func webView(_ webView: WKWebView, didCommit navigation: WKNavigation!) { if navigation == self.navigation { state = "committed" } }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { if navigation == self.navigation { finished = true } }
    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        if navigation == self.navigation { failure = "\(error)" }
    }
    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        if navigation == self.navigation { failure = "\(error)" }
    }
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { failure = "the web content process terminated" }
}

// MARK: - HTTP stubbing

/// Answers every request from `handler` and records each one, body included. No test touches the network.
final class StubURLProtocol: URLProtocol {
    typealias Handler = (URLRequest) throws -> (HTTPURLResponse, Data)
    private static let lock = NSLock()
    private static var _handler: Handler?
    private static var _requests: [URLRequest] = []

    static var handler: Handler? {
        get { lock.lock(); defer { lock.unlock() }; return _handler }
        set { lock.lock(); _handler = newValue; lock.unlock() }
    }
    static var requests: [URLRequest] { lock.lock(); defer { lock.unlock() }; return _requests }
    static func reset() { lock.lock(); _handler = nil; _requests = []; lock.unlock() }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var req = request
        if req.httpBody == nil, let stream = req.httpBodyStream {
            stream.open(); defer { stream.close() }
            var data = Data(); var buf = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                data.append(buf, count: n)
            }
            req.httpBody = data
        }
        Self.lock.lock(); Self._requests.append(req); let h = Self._handler; Self.lock.unlock()
        do {
            guard let h else { throw URLError(.resourceUnavailable) }
            let (resp, data) = try h(req)
            client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }
    override func stopLoading() {}
}

func stubSession() -> URLSession {
    let c = URLSessionConfiguration.ephemeral
    c.protocolClasses = [StubURLProtocol.self]
    return URLSession(configuration: c)
}

extension HTTPURLResponse {
    static func status(_ code: Int, headers: [String: String] = [:]) -> HTTPURLResponse {
        HTTPURLResponse(url: URL(string: "https://lark.test/")!, statusCode: code, httpVersion: "HTTP/1.1", headerFields: headers)!
    }
    static func ok(_ headers: [String: String] = ["Content-Type": "application/json"]) -> HTTPURLResponse { status(200, headers: headers) }
}

extension URLRequest {
    var bodyData: Data { httpBody ?? Data() }
    var queryItems: [URLQueryItem] {
        url.flatMap { URLComponents(url: $0, resolvingAgainstBaseURL: false)?.queryItems } ?? []
    }
}

func jsonObject(_ d: Data) throws -> NSObject { try JSONSerialization.jsonObject(with: d) as! NSObject }

func XCTAssertThrowsErrorAsync<T>(_ expr: @autoclosure () async throws -> T, _ check: (Error) -> Void = { _ in },
                                  file: StaticString = #filePath, line: UInt = #line) async {
    do { _ = try await expr(); XCTFail("expected an error", file: file, line: line) }
    catch { check(error) }
}

// MARK: - Cold start

/// System services a fresh simulator starts on first use: the first web view's WebKit processes, the first
/// audio playback. On a loaded CI machine that first start can take far longer than anything a test measures,
/// so each test class that needs one pays for it once, up front, with a deadline that only a broken simulator
/// reaches. The tests' own waits then measure the code under test, not the simulator's cold start.
@MainActor enum ColdStart {
    private static var webKitReady = false
    private static var audioReady = false

    static func webKit() async throws {
        guard !webKitReady else { return }
        let started = Date()
        // On screen, like the tests' own web views: a hidden one may be throttled.
        guard let scene = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }).first else {
            throw NavigationFailed(base: URL(string: "https://warm-up.test/")!, reason: "the test host has no window scene")
        }
        let window = UIWindow(windowScene: scene)
        let web = WKWebView(frame: window.bounds)
        window.addSubview(web)
        window.isHidden = false
        defer { web.removeFromSuperview(); window.isHidden = true }
        let nav = NavigationWaiter()
        web.navigationDelegate = nav
        nav.navigation = web.loadHTMLString("<html><body>warm-up</body></html>", baseURL: URL(string: "https://warm-up.test/")!)
        try await waitUntil(timeout: 120, state: { "warm-up: \(nav.state) \(nav.failure ?? "")" }) { nav.finished || nav.failure != nil }
        if let failure = nav.failure { throw NavigationFailed(base: URL(string: "https://warm-up.test/")!, reason: failure) }
        webKitReady = true
        print("cold start: WebKit ready in \(String(format: "%.1f", Date().timeIntervalSince(started))) s")
    }

    static func audio() async throws {
        guard !audioReady else { return }
        let started = Date()
        let url = try makeSilentFile(seconds: 0.5)
        defer { try? FileManager.default.removeItem(at: url) }
        let player = AVPlayer(url: url)
        player.play()
        try await waitUntil(timeout: 120) {
            player.currentItem?.status == .failed || (player.currentTime().seconds) >= 0.3
        }
        player.pause()
        if let e = player.currentItem?.error { throw e }
        audioReady = true
        print("cold start: audio ready in \(String(format: "%.1f", Date().timeIntervalSince(started))) s")
    }
}

// MARK: - Bounded awaits

@MainActor final class ResultBox<T> { var value: T? }

struct StillWaiting: Error, CustomStringConvertible {
    let what: String
    let seconds: TimeInterval
    var description: String { "\(what) did not return within \(seconds) s" }
}

/// Runs `op` and waits at most `seconds` for it to return. For system calls that cannot be cancelled and,
/// on a struggling simulator, sometimes never call back (WebKit's cookie store): the test fails with what it
/// was waiting for instead of hanging until the test runner's own limit.
@MainActor func within<T>(_ seconds: TimeInterval, _ what: String, file: StaticString = #filePath, line: UInt = #line,
                          _ op: @escaping @MainActor () async -> T) async throws -> T {
    let box = ResultBox<T>()
    let task = Task { @MainActor in box.value = await op() }
    do {
        try await waitUntil(timeout: seconds, file: file, line: line) { box.value != nil }
    } catch is WaitTimeout {
        task.cancel()
        throw StillWaiting(what: what, seconds: seconds)
    }
    return box.value!
}

// MARK: - Cookies

extension WKHTTPCookieStore {
    /// Every cookie in the store, or an error after 30 s.
    @MainActor func cookies(file: StaticString = #filePath, line: UInt = #line) async throws -> [HTTPCookie] {
        try await within(30, "WKHTTPCookieStore.allCookies", file: file, line: line) { await self.allCookies() }
    }

    /// Sets `cookie` and waits until the store hands it back. `setCookie` can return before `allCookies()`
    /// includes the cookie (the store forwards it to WebKit's network process), so code that reads the store
    /// next would not see it yet.
    @MainActor func setCookieAndWait(_ cookie: HTTPCookie, file: StaticString = #filePath, line: UInt = #line) async throws {
        _ = try await within(30, "WKHTTPCookieStore.setCookie", file: file, line: line) { await self.setCookie(cookie); return true }
        try await waitUntil(timeout: 30, file: file, line: line, state: { "cookie \(cookie.name) for \(cookie.domain) not in the store" }) {
            (try? await self.cookies()).map { $0.contains { $0.name == cookie.name && $0.domain == cookie.domain && $0.value == cookie.value } } ?? false
        }
    }
}
