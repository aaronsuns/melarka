import XCTest
@testable import Lark

/// The loudness gain of a music item: the server's `gain_db` as a volume factor, attenuation only.
final class GainTests: XCTestCase {
    func music(_ gain: JSONValue?) -> Item {
        var meta: [String: JSONValue] = ["id": .int(7)]
        if let gain { meta["gain_db"] = gain }
        return Item(kind: .track, id: "7", title: "T", artist: "A", album: "B", durationMs: 1000, meta: .object(meta))
    }

    func testMinusSixDecibelsIsAboutHalf() {
        XCTAssertEqual(Gain.factor(music(.double(-6)), enabled: true), 0.501, accuracy: 0.001)
        XCTAssertEqual(Gain.factor(music(.int(-6)), enabled: true), 0.501, accuracy: 0.001)   // a whole number on the wire
        XCTAssertEqual(Gain.factor(music(.double(-3.5)), enabled: true), 0.668, accuracy: 0.001)
    }

    func testUnmeasuredIsUnity() {
        XCTAssertEqual(Gain.factor(music(nil), enabled: true), 1)
        XCTAssertEqual(Gain.factor(music(.null), enabled: true), 1)
        XCTAssertEqual(Gain.factor(music(.string("-6")), enabled: true), 1)
    }

    func testNeverABoost() {
        XCTAssertEqual(Gain.factor(music(.int(3)), enabled: true), 1)
        XCTAssertEqual(Gain.factor(music(.double(0)), enabled: true), 1)
    }

    func testSwitchedOffIsUnity() {
        XCTAssertEqual(Gain.factor(music(.int(-6)), enabled: false), 1)
    }

    func testEpisodesAreNeverAttenuated() {
        let ep = Item(kind: .episode, id: "v1", title: "E", artist: "C", album: "频道", durationMs: 1000,
                      meta: .object(["video_id": .string("v1"), "gain_db": .int(-6)]))
        XCTAssertEqual(Gain.factor(ep, enabled: true), 1)
    }

    func testTrackDecodesGainDB() throws {
        let t = try JSONDecoder().decode(Track.self, from: Data(#"{"id":1,"title":"t","artist":"a","album":"b","duration_ms":1,"favorite":false,"gain_db":-4.5}"#.utf8))
        XCTAssertEqual(t.gain_db, -4.5)
        let u = try JSONDecoder().decode(Track.self, from: Data(#"{"id":1,"title":"t","artist":"a","album":"b","duration_ms":1,"favorite":false,"gain_db":null}"#.utf8))
        XCTAssertNil(u.gain_db)
    }
}
