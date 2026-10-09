import XCTest
import WebKit

struct WaitTimeout: Error, CustomStringConvertible {
    let seconds: TimeInterval
    var description: String { "condition not met within \(seconds) s" }
}

/// Polls `condition` every 50 ms until it is true, or throws after `timeout` seconds.
@MainActor func waitUntil(timeout: TimeInterval = 5, _ condition: () async throws -> Bool) async throws {
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if try await condition() { return }
        try await Task.sleep(nanoseconds: 50_000_000)
    }
    if try await condition() { return }
    throw WaitTimeout(seconds: timeout)
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
