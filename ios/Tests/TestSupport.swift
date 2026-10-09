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
        let scene = try XCTUnwrap(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first,
                                  "the test host has no window scene")
        let window = UIWindow(windowScene: scene)
        let web = WKWebView(frame: window.bounds, configuration: configuration)
        window.addSubview(web)
        window.isHidden = false
        addTeardownBlock { @MainActor in
            web.removeFromSuperview()
            window.isHidden = true
        }
        return web
    }
}

/// Calls `done` once, on the first finished navigation.
final class OneShotNavDelegate: NSObject, WKNavigationDelegate {
    private var done: (() -> Void)?
    init(_ done: @escaping () -> Void) { self.done = done }
    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        done?(); done = nil
    }
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
        let web = WKWebView(frame: CGRect(x: 0, y: 0, width: 100, height: 100))
        let nav = OneShotNavDelegate {}
        web.navigationDelegate = nav
        web.loadHTMLString("<html><body>warm-up</body></html>", baseURL: URL(string: "https://warm-up.test/")!)
        try await waitUntil(timeout: 120) {
            (try? await web.evaluateJavaScript("document.body.textContent") as? String) == "warm-up"
        }
        withExtendedLifetime(nav) {}
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
