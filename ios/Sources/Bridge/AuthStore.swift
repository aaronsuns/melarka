import Foundation
import os

/// The device token native uses for API calls, downloads and streams.
///
/// The token is the web's HttpOnly `lark_token` session cookie (lark-server `auth.CookieName`); the page
/// cannot read it, so native harvests it from the web view's cookie store. It lives only here and in the
/// Keychain: it is never logged, never put in a URL and never sent over the bridge.
@MainActor final class AuthStore {
    static let cookieName = "lark_token"
    private static let log = Logger(subsystem: BuildInfo.bundleID, category: "auth")
    private let secrets: SecretStore
    /// The last cleared token. A 401 or a logout racing the server's Set-Cookie can leave that cookie in place
    /// for a while; it must not be adopted again. A fresh login issues a new token.
    private var revoked: String?

    private(set) var token: String?
    private(set) var userId: Int?
    /// Called with the new token (nil when cleared), only when it actually changes.
    var onChange: ((String?) -> Void)?

    init(secrets: SecretStore) {
        self.secrets = secrets
        token = secrets.get("token")
        userId = secrets.get("userId").flatMap(Int.init)
    }

    /// Takes `lark_token` for exactly `serverHost` (a host-only cookie, or one whose Domain is that host).
    /// Only an HttpOnly cookie counts, and never the last cleared value.
    /// A missing cookie changes nothing: signing out is `clear()`'s job, so a briefly empty cookie store
    /// never signs native out.
    func harvest(from cookies: [HTTPCookie], serverHost: String, userId: Int?) {
        let host = serverHost.lowercased()
        if let userId, userId != self.userId {
            self.userId = userId
            secrets.set("userId", String(userId))
        }
        // Only the server's HttpOnly cookie: JS on the origin can set a readable `lark_token` next to it
        // (session fixation). The real one has Path=/, so prefer it.
        let candidates = cookies.filter { $0.name == Self.cookieName && $0.isHTTPOnly && Self.domain($0.domain, matches: host) }
        guard let c = candidates.first(where: { $0.path == "/" }) ?? candidates.first,
              !c.value.isEmpty, c.value != revoked else { return }
        guard c.value != token else { return }
        token = c.value
        secrets.set("token", c.value)
        Self.log.info("device token harvested from the session cookie")
        onChange?(token)
    }

    /// Logout, user switch or a 401.
    func clear() {
        let had = token != nil
        if let token { revoked = token }
        token = nil; userId = nil
        secrets.set("token", nil); secrets.set("userId", nil)
        if had {
            Self.log.info("device token cleared")
            onChange?(nil)
        }
    }

    private static func domain(_ domain: String, matches host: String) -> Bool {
        let d = domain.lowercased()
        return d == host || d == "." + host
    }
}
