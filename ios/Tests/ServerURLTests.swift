import XCTest
@testable import Lark

final class ServerURLTests: XCTestCase {
    func testNormalizesAndValidates() {
        XCTAssertEqual(ServerURLValidator.normalize(" https://music.example.com/ ")?.absoluteString, "https://music.example.com/")
        XCTAssertEqual(ServerURLValidator.normalize("https://music.example.com:8443/x?y")?.absoluteString, "https://music.example.com:8443/")
        XCTAssertEqual(ServerURLValidator.normalize("http://mac.local:8080")?.absoluteString, "http://mac.local:8080/")
        XCTAssertEqual(ServerURLValidator.normalize("http://localhost:8080")?.absoluteString, "http://localhost:8080/")
        XCTAssertNil(ServerURLValidator.normalize("http://music.example.com"))   // plain http only for development hosts
        XCTAssertNil(ServerURLValidator.normalize("ftp://music.example.com"))
        XCTAssertNil(ServerURLValidator.normalize("music.example.com"))
        XCTAssertNil(ServerURLValidator.normalize(""))
        XCTAssertNil(ServerURLValidator.normalize("https://user:pw@example.com"))
    }

    func testFirstLaunchFieldStartsEmpty() {
        XCTAssertEqual(ServerURLValidator.initialText, "")   // no pre-filled server
        XCTAssertEqual(ServerURLValidator.placeholder, "https://music.example.com")
    }

    func testInfoURL() {
        XCTAssertEqual(ServerURLValidator.infoURL(URL(string: "https://music.example.com/")!).absoluteString, "https://music.example.com/api/v1/info")
    }
}
