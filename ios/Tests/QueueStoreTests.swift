import XCTest
@testable import Lark

final class QueueStoreTests: EngineTestCase {
    var queueFile: URL { dir.appendingPathComponent("queues-1.json") }

    func testSetQueueWritesThePerUserFile() throws {
        XCTAssertFalse(FileManager.default.fileExists(atPath: queueFile.path))
        engine.handle(.setQueue(q([7, 8], index: 1, pos: 3_000, play: false)))
        XCTAssertTrue(FileManager.default.fileExists(atPath: queueFile.path))
        XCTAssertEqual(store.load()?.music.items.map(\.id), ["7", "8"])
    }

    func testANewEngineRestoresBothQueuesTheActiveKindAndThePositionPaused() {
        engine.handle(.setQueue(q([7, 8], index: 1, pos: 3_000, play: true)))
        backend.start()
        engine.handle(.setQueue(.init(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 60_000, play: true, source: .list)))
        backend.start(); backend.play(to: 70_000)
        engine.tick()
        makeEngine()
        XCTAssertEqual(engine.music.items.map(\.id), ["7", "8"]); XCTAssertEqual(engine.music.index, 1)
        XCTAssertEqual(engine.music.positionMs, 3_000)
        XCTAssertEqual(engine.episodes.items.map(\.id), ["abcdefghijk"]); XCTAssertEqual(engine.episodes.positionMs, 70_000)
        XCTAssertEqual(engine.episodes.items.first?.meta["video_id"], .string("abcdefghijk"))
        XCTAssertEqual(engine.active, .episode)
        XCTAssertFalse(engine.playing)
        XCTAssertTrue(backend.loads.isEmpty)       // nothing touches the network until play
    }

    func testTickWhilePlayingSavesThePosition() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 21_000)
        XCTAssertEqual(store.load()?.music.positionMs, 0)
        engine.tick()
        XCTAssertEqual(store.load()?.music.positionMs, 21_000)
    }

    func testPauseSavesThePosition() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 8_000)
        engine.pause()
        XCTAssertEqual(store.load()?.music.positionMs, 8_000)
    }

    func testResetStopsAndDeletesTheFiles() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: true)))
        backend.start(); backend.play(to: 40_000)
        engine.next()                                       // a pending play event
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("events-1.json").path))
        engine.reset()
        XCTAssertFalse(FileManager.default.fileExists(atPath: queueFile.path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("events-1.json").path))
        XCTAssertTrue(engine.music.items.isEmpty); XCTAssertTrue(engine.episodes.items.isEmpty)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.calls.last, "stop")
        XCTAssertTrue(events.pending.isEmpty)
        XCTAssertEqual(queues(.track).last?.items.count, 0)
    }

    func testEachUserHasTheirOwnFile() {
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))
        store.user = 2
        engine.restore()
        XCTAssertTrue(engine.music.items.isEmpty)
        store.user = 1
        engine.restore()
        XCTAssertEqual(engine.music.items.map(\.id), ["7"])
    }

    func testACorruptFileIsIgnored() throws {
        try Data("{not json".utf8).write(to: queueFile)
        makeEngine()
        XCTAssertTrue(engine.music.items.isEmpty)
    }

    func testNoUserNoFile() {
        store.user = nil
        engine.handle(.setQueue(q([7], index: 0, pos: 0, play: false)))
        let files = (try? FileManager.default.contentsOfDirectory(atPath: dir.path)) ?? []
        XCTAssertTrue(files.isEmpty)
    }

    /// A queue file written before shuffle and repeat existed (v0.1.0, no `modes`) loads, with both off.
    func testAQueueFileWithoutModesLoadsWithModesOff() throws {
        let item = #"{"kind":"track","id":"7","title":"T7","artist":"A","album":"B","durationMs":1000,"meta":{"id":7}}"#
        let q = #"{"items":[\#(item)],"index":0,"positionMs":0,"source":"list"}"#
        let empty = #"{"items":[],"index":0,"positionMs":0,"source":"list"}"#
        let file = #"{"version":1,"music":\#(q),"episodes":\#(empty),"active":"track","savedAt":1}"#
        try Data(file.utf8).write(to: queueFile)
        let s = try XCTUnwrap(store.load())
        XCTAssertNil(s.modes)
        makeEngine()
        XCTAssertEqual(engine.music.items.map(\.id), ["7"])
        XCTAssertEqual(engine.modes, PlayModes())
    }

    func testModesAreSavedWithTheQueues() {
        engine.handle(.setQueue(q([7, 8], index: 0, pos: 0, play: false)))
        engine.handle(.setModes(shuffle: false, repeatMode: .one))
        XCTAssertEqual(store.load()?.modes, PlayModes(repeatMode: .one))
        XCTAssertEqual(store.load()?.version, 1)
    }

    /// A malformed `modes` field: the modes fall back to off, the saved queue is kept.
    func testAMalformedModesFieldKeepsTheQueue() throws {
        let item = #"{"kind":"track","id":"7","title":"T7","artist":"A","album":"B","durationMs":1000,"meta":{"id":7}}"#
        let q = #"{"items":[\#(item)],"index":0,"positionMs":0,"source":"list"}"#
        let empty = #"{"items":[],"index":0,"positionMs":0,"source":"list"}"#
        for bad in ["5", "\"all\"", "[1]"] {
            let file = #"{"version":1,"music":\#(q),"episodes":\#(empty),"active":"track","savedAt":1,"modes":\#(bad)}"#
            try Data(file.utf8).write(to: queueFile)
            makeEngine()
            XCTAssertEqual(engine.music.items.map(\.id), ["7"], bad)
            XCTAssertEqual(engine.modes, PlayModes(), bad)
        }
    }
}
