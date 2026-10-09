import Foundation

/// Which main-frame load outcomes mean "the page is not there" (the native error screen), and which are only
/// a navigation being replaced.
enum PageLoad {
    static func isFailure(_ error: Error) -> Bool {
        let e = error as NSError
        if e.domain == NSURLErrorDomain && e.code == NSURLErrorCancelled { return false }   // another load replaced it
        if e.domain == "WebKitErrorDomain" && e.code == 102 { return false }                 // frame load interrupted (a policy cancel)
        return true
    }

    /// A 5xx for the page itself: the proxy is up but the server is not.
    static func isFailure(status: Int) -> Bool { status >= 500 }
}
