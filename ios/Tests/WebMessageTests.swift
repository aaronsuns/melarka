import XCTest
@testable import Lark

final class WebMessageTests: XCTestCase {
    func testDecodesSetQueueWithMeta() throws {
        let body: [String: Any] = ["type": "setQueue", "kind": "track", "index": 1, "positionMs": 5000, "play": true, "source": "favorites",
            "items": [["kind": "track", "id": "7", "title": "晴天", "artist": "周杰伦", "album": "叶惠美", "durationMs": 269000,
                       "meta": ["id": 7, "favorite": true, "title": "晴天"]],
                      ["kind": "track", "id": "8", "title": "稻香", "artist": "周杰伦", "album": "魔杰座", "durationMs": 223000, "meta": ["id": 8]]]]
        guard case .setQueue(let q) = try WebMessage.decode(body) else { return XCTFail("not setQueue") }
        XCTAssertEqual(q.items.map(\.id), ["7", "8"]); XCTAssertEqual(q.positionMs, 5000); XCTAssertEqual(q.source, .favorites)
        XCTAssertEqual(q.items[0].meta["favorite"], .bool(true))
    }

    func testSetQueueWithoutPositionMeansKeep() throws {
        guard case .setQueue(let q) = try WebMessage.decode(["type": "setQueue", "kind": "episode", "index": 0, "play": false,
            "source": "list", "items": []]) else { return XCTFail() }
        XCTAssertNil(q.positionMs)
    }

    func testUnknownTypeThrows() { XCTAssertThrowsError(try WebMessage.decode(["type": "rm -rf"])) }

    func testAuthCarriesNoToken() throws {
        XCTAssertEqual(try WebMessage.decode(["type": "auth", "signedIn": true, "userId": 3, "token": "x"]), .auth(signedIn: true, userId: 3))
    }

    func testEventJavaScriptEscapesLineSeparators() {
        let js = NativeEvent.notice("a\u{2028}b</script>").javaScript()
        XCTAssertFalse(js.contains("\u{2028}")); XCTAssertTrue(js.hasPrefix("window.dispatchEvent(new CustomEvent(\"lark-native\""))
        XCTAssertFalse(js.contains("</"))
    }

    // Every message shape the web app posts, and the lenient numbers it may send.
    func testDecodesEveryMessage() throws {
        XCTAssertEqual(try WebMessage.decode(["type": "hello", "onOpen": "resume"]), .hello(onOpen: "resume"))
        XCTAssertEqual(try WebMessage.decode(["type": "play", "kind": "episode"]), .play(.episode))
        XCTAssertEqual(try WebMessage.decode(["type": "pause"]), .pause(nil))
        XCTAssertEqual(try WebMessage.decode(["type": "pause", "kind": "track"]), .pause(.track))
        XCTAssertEqual(try WebMessage.decode(["type": "next", "kind": "track"]), .next(.track))
        XCTAssertEqual(try WebMessage.decode(["type": "prev", "kind": "track"]), .prev(.track))
        XCTAssertEqual(try WebMessage.decode(["type": "seek", "kind": "track", "ms": 1234.6]), .seek(.track, ms: 1235))
        XCTAssertEqual(try WebMessage.decode(["type": "skip", "kind": "episode", "ms": -15000]), .skip(.episode, ms: -15000))
        XCTAssertEqual(try WebMessage.decode(["type": "setRate", "rate": 1.5]), .setRate(1.5))
        XCTAssertEqual(try WebMessage.decode(["type": "stop", "kind": "episode"]), .stop(.episode))
        XCTAssertEqual(try WebMessage.decode(["type": "setPrefs", "quality": "high", "carLyrics": false]),
                       .setPrefs(NativePrefs(quality: "high", carLyrics: false)))
        XCTAssertEqual(try WebMessage.decode(["type": "auth", "signedIn": false]), .auth(signedIn: false, userId: nil))
        XCTAssertEqual(try WebMessage.decode(["type": "pauseForWeb"]), .pauseForWeb)
        XCTAssertEqual(try WebMessage.decode(["type": "favoriteChanged", "trackId": 7, "on": true]), .favoriteChanged(trackId: 7, on: true))
        XCTAssertEqual(try WebMessage.decode(["type": "flushEvents", "id": "f1"]), .flushEvents(id: "f1"))
        XCTAssertEqual(try WebMessage.decode(["type": "openSettings"]), .openSettings)
    }

    func testDecodesSetModes() throws {
        XCTAssertEqual(try WebMessage.decode(["type": "setModes", "shuffle": true, "repeat": "one"]), .setModes(shuffle: true, repeatMode: .one))
        XCTAssertEqual(try WebMessage.decode(["type": "setModes", "shuffle": false, "repeat": "all"]), .setModes(shuffle: false, repeatMode: .all))
        XCTAssertEqual(try WebMessage.decode(["type": "setModes", "shuffle": false, "repeat": "off", "extra": 1]), .setModes(shuffle: false, repeatMode: .off))
        XCTAssertThrowsError(try WebMessage.decode(["type": "setModes", "shuffle": true, "repeat": "sometimes"]))
        XCTAssertThrowsError(try WebMessage.decode(["type": "setModes", "repeat": "all"]))       // shuffle missing
        XCTAssertThrowsError(try WebMessage.decode(["type": "setModes", "shuffle": "yes", "repeat": "all"]))
        // setPrefs from a web that knows nothing newer still decodes
        XCTAssertEqual(try WebMessage.decode(["type": "setPrefs", "quality": "saver", "carLyrics": true]),
                       .setPrefs(NativePrefs(quality: "saver", carLyrics: true)))
    }

    func testStateCarriesTheModes() throws {
        let s = try detail(.state(StateEvent(kind: .track, itemId: "7", index: 0, playing: true, positionMs: 0, durationMs: 1,
                                             buffering: false, error: nil, rate: 1, shuffle: true, repeatMode: .all)))
        XCTAssertEqual(s["shuffle"] as? Bool, true)
        XCTAssertEqual(s["repeat"] as? String, "all")
        XCTAssertNil(s["repeatMode"])
        let d = try detail(.state(StateEvent(kind: .episode, itemId: nil, index: 0, playing: false, positionMs: 0, durationMs: 0,
                                             buffering: false, error: nil, rate: 1)))
        XCTAssertEqual(d["shuffle"] as? Bool, false)
        XCTAssertEqual(d["repeat"] as? String, "off")
    }

    func testRejectsMalformedBodies() {
        XCTAssertThrowsError(try WebMessage.decode("just a string"))
        XCTAssertThrowsError(try WebMessage.decode(["type": "play"]))                    // kind missing
        XCTAssertThrowsError(try WebMessage.decode(["type": "play", "kind": "video"]))   // unknown kind
        XCTAssertThrowsError(try WebMessage.decode(["kind": "track"]))                   // type missing
    }

    func testEventsEncodeTheirShapes() throws {
        let item = Item(kind: .track, id: "7", title: "晴天", artist: "周杰伦", album: "叶惠美", durationMs: 269000,
                        meta: .object(["id": .int(7), "favorite": .bool(true), "gain": .double(-3.5), "lyrics": .null]))
        let q = try detail(NativeEvent.queue(kind: .track, items: [item], index: 0, source: .radio))
        XCTAssertEqual(q["type"] as? String, "queue"); XCTAssertEqual(q["source"] as? String, "radio")
        let first = try XCTUnwrap((q["items"] as? [[String: Any]])?.first)
        XCTAssertEqual(first["durationMs"] as? Int, 269000)
        let meta = try XCTUnwrap(first["meta"] as? [String: Any])
        XCTAssertEqual(meta["id"] as? Int, 7); XCTAssertEqual(meta["favorite"] as? Bool, true)
        XCTAssertEqual(meta["gain"] as? Double, -3.5); XCTAssertTrue(meta["lyrics"] is NSNull)

        let s = try detail(.state(StateEvent(kind: .episode, itemId: nil, index: 0, playing: false, positionMs: 10, durationMs: 20,
                                             buffering: true, error: nil, rate: 1.25)))
        XCTAssertEqual(s["type"] as? String, "state"); XCTAssertEqual(s["kind"] as? String, "episode")
        XCTAssertTrue(s["itemId"] is NSNull); XCTAssertTrue(s["error"] is NSNull); XCTAssertEqual(s["rate"] as? Double, 1.25)
        XCTAssertEqual(try detail(.authRequired)["type"] as? String, "authRequired")
        XCTAssertEqual(try detail(.flushed(id: "f1"))["id"] as? String, "f1")
        XCTAssertEqual(try detail(.notice("离线"))["text"] as? String, "离线")
    }

    func testItemMetaRoundTripsUnchanged() throws {
        let raw: [String: Any] = ["kind": "episode", "id": "dQw4w9WgXcQ", "title": "t", "artist": "c", "album": "频道", "durationMs": 1000,
                                  "meta": ["id": 12, "nested": ["a": [1, 2.5, "x", false]], "none": NSNull()]]
        let item = try JSONDecoder().decode(Item.self, from: JSONSerialization.data(withJSONObject: raw))
        let again = try JSONDecoder().decode(Item.self, from: JSONEncoder().encode(item))
        XCTAssertEqual(again, item)
        XCTAssertEqual(item.meta["nested"]?["a"], .array([.int(1), .double(2.5), .string("x"), .bool(false)]))
    }

    /// The `detail` object inside `javaScript()`'s CustomEvent, parsed back.
    private func detail(_ e: NativeEvent) throws -> [String: Any] {
        let js = e.javaScript()
        let start = try XCTUnwrap(js.range(of: "{detail:")).upperBound
        let json = js[start..<js.index(js.endIndex, offsetBy: -3)]   // drop "}))"
        return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any])
    }
}

// MARK: - Sleep timer and loudness

extension WebMessageTests {
    func testDecodesSleepTimer() throws {
        XCTAssertEqual(try WebMessage.decode(["type": "sleepTimer", "minutes": 15]), .sleepTimer(.minutes(15)))
        XCTAssertEqual(try WebMessage.decode(["type": "sleepTimer", "minutes": 60]), .sleepTimer(.minutes(60)))
        XCTAssertEqual(try WebMessage.decode(["type": "sleepTimer", "endOfTrack": true]), .sleepTimer(.endOfTrack))
        XCTAssertEqual(try WebMessage.decode(["type": "sleepTimer", "cancel": true]), .sleepTimer(.cancel))
        XCTAssertEqual(try WebMessage.decode(["type": "sleepTimer", "minutes": 30, "extra": 1]), .sleepTimer(.minutes(30)))
    }

    func testRejectsAMalformedSleepTimer() {
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer"]))                          // says nothing
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer", "minutes": 0]))
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer", "minutes": -15]))
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer", "minutes": "15"]))
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer", "minutes": 100_000]))
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer", "endOfTrack": false]))
        XCTAssertThrowsError(try WebMessage.decode(["type": "sleepTimer", "cancel": "yes"]))
    }

    func testSetPrefsCarriesLoudness() throws {
        XCTAssertEqual(try WebMessage.decode(["type": "setPrefs", "quality": "high", "carLyrics": true, "loudness": false]),
                       .setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: false)))
        XCTAssertEqual(try WebMessage.decode(["type": "setPrefs", "quality": "high", "carLyrics": true, "loudness": true]),
                       .setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: true)))
        // A web from before the switch: on, as the server's default.
        XCTAssertEqual(try WebMessage.decode(["type": "setPrefs", "quality": "high", "carLyrics": true]),
                       .setPrefs(NativePrefs(quality: "high", carLyrics: true, loudness: true)))
        XCTAssertThrowsError(try WebMessage.decode(["type": "setPrefs", "quality": "high", "carLyrics": true, "loudness": "off"]))
    }

    func testStateCarriesSleepRemaining() throws {
        let s = try detail(.state(StateEvent(kind: .track, itemId: "7", index: 0, playing: true, positionMs: 0, durationMs: 1,
                                             buffering: false, error: nil, rate: 1, sleepRemainingMs: 61_000)))
        XCTAssertEqual(s["sleepRemainingMs"] as? Int, 61_000)
        let none = try detail(.state(StateEvent(kind: .episode, itemId: nil, index: 0, playing: false, positionMs: 0, durationMs: 0,
                                                buffering: false, error: nil, rate: 1)))
        XCTAssertTrue(none["sleepRemainingMs"] is NSNull, "an explicit null, never an absent key")
    }
}
