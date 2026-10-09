import XCTest
@testable import Lark
final class SmokeTests: XCTestCase {
    func testBundleIdentifier() {
        XCTAssertEqual(Bundle.main.bundleIdentifier, BuildInfo.bundleID) // hosted: main bundle is the app
        XCTAssertEqual(Bundle.main.bundleIdentifier, "io.github.aaronsuns.melarka")
    }

    func testDisplayName() {
        XCTAssertEqual(Bundle.main.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String, "Melarka")
    }
}
