import XCTest
@testable import Lark

final class AuthStoreTests: XCTestCase {
    private func cookie(_ name: String, _ value: String, _ domain: String, path: String = "/", httpOnly: Bool = true) -> HTTPCookie {
        var props: [HTTPCookiePropertyKey: Any] = [.name: name, .value: value, .domain: domain, .path: path]
        if httpOnly { props[HTTPCookiePropertyKey("HttpOnly")] = "TRUE" }
        let c = HTTPCookie(properties: props)!
        XCTAssertEqual(c.isHTTPOnly, httpOnly)
        return c
    }

    @MainActor func testHarvestTakesLarkTokenForServerHostOnly() {
        let s = MemorySecretStore(); let a = AuthStore(secrets: s)
        a.harvest(from: [cookie("lark_token", "T1", "lark.test"), cookie("lark_token", "EVIL", "other.test"), cookie("x", "y", "lark.test")],
                  serverHost: "lark.test", userId: 3)
        XCTAssertEqual(a.token, "T1"); XCTAssertEqual(s.get("token"), "T1"); XCTAssertEqual(a.userId, 3)
    }

    @MainActor func testHarvestIgnoresParentAndLookalikeDomains() {
        let a = AuthStore(secrets: MemorySecretStore())
        a.harvest(from: [cookie("lark_token", "P", ".test"), cookie("lark_token", "L", "evillark.test")], serverHost: "lark.test", userId: 1)
        XCTAssertNil(a.token)
        a.harvest(from: [cookie("lark_token", "D", ".lark.test")], serverHost: "lark.test", userId: 1)
        XCTAssertEqual(a.token, "D")
    }

    @MainActor func testClearRemovesToken() {
        let s = MemorySecretStore(); let a = AuthStore(secrets: s)
        var changes: [String?] = []
        a.onChange = { changes.append($0) }
        a.harvest(from: [cookie("lark_token", "T1", "lark.test")], serverHost: "lark.test", userId: 3)
        a.clear()
        XCTAssertNil(a.token); XCTAssertNil(a.userId); XCTAssertNil(s.get("token")); XCTAssertNil(s.get("userId"))
        XCTAssertEqual(changes, ["T1", nil])
    }

    @MainActor func testMissingCookieKeepsTokenAndSameTokenIsNoChange() {
        let s = MemorySecretStore(); let a = AuthStore(secrets: s)
        var changes = 0
        a.onChange = { _ in changes += 1 }
        a.harvest(from: [cookie("lark_token", "T1", "lark.test")], serverHost: "lark.test", userId: 3)
        a.harvest(from: [cookie("lark_token", "T1", "lark.test")], serverHost: "lark.test", userId: nil)
        a.harvest(from: [], serverHost: "lark.test", userId: nil)   // a flaky empty cookie store must not sign native out
        XCTAssertEqual(a.token, "T1"); XCTAssertEqual(a.userId, 3); XCTAssertEqual(changes, 1)
    }

    @MainActor func testTokenSurvivesRelaunch() {
        let s = MemorySecretStore()
        AuthStore(secrets: s).harvest(from: [cookie("lark_token", "T1", "lark.test")], serverHost: "lark.test", userId: 3)
        let again = AuthStore(secrets: s)
        XCTAssertEqual(again.token, "T1"); XCTAssertEqual(again.userId, 3)
    }

    @MainActor func testHarvestIgnoresScriptSetCookiesAndPrefersRootPath() {
        // JS on the origin can set a non-HttpOnly lark_token next to the real one (session fixation).
        let a = AuthStore(secrets: MemorySecretStore())
        a.harvest(from: [cookie("lark_token", "FIXED", "lark.test", httpOnly: false)], serverHost: "lark.test", userId: 1)
        XCTAssertNil(a.token)
        a.harvest(from: [cookie("lark_token", "SUB", "lark.test", path: "/a"), cookie("lark_token", "REAL", "lark.test")],
                  serverHost: "lark.test", userId: 1)
        XCTAssertEqual(a.token, "REAL")
    }

    @MainActor func testClearedTokenIsNotHarvestedAgain() {
        // A 401 or a logout racing the server's Set-Cookie leaves the old cookie in place for a moment.
        let a = AuthStore(secrets: MemorySecretStore())
        var changes: [String?] = []
        a.onChange = { changes.append($0) }
        a.harvest(from: [cookie("lark_token", "T1", "lark.test")], serverHost: "lark.test", userId: 3)
        a.clear()
        a.harvest(from: [cookie("lark_token", "T1", "lark.test")], serverHost: "lark.test", userId: 3)
        XCTAssertNil(a.token)
        a.harvest(from: [cookie("lark_token", "T2", "lark.test")], serverHost: "lark.test", userId: 3)   // a fresh login
        XCTAssertEqual(a.token, "T2")
        XCTAssertEqual(changes, ["T1", nil, "T2"])
    }

    func testKeychainRoundTrip() throws {
        let k = KeychainSecretStore(service: "io.github.aaronsuns.melarka.tests")
        k.set("t", "v"); defer { k.set("t", nil) }
        guard k.lastStatus != errSecMissingEntitlement else { throw XCTSkip("no keychain in this simulator host") }
        XCTAssertEqual(k.get("t"), "v")
        k.set("t", "w")
        XCTAssertEqual(k.get("t"), "w")
        k.set("t", nil)
        XCTAssertNil(k.get("t"))
    }
}
