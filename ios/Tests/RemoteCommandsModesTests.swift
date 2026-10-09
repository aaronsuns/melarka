import XCTest
import MediaPlayer
@testable import Lark

/// The lock screen's and the car's repeat and shuffle buttons: mapped onto the engine's modes, showing them,
/// and off while an episode plays (episodes ignore the modes).
@MainActor final class RemoteCommandsModesTests: EngineTestCase {
    var remote: RemoteCommands!
    let center = MPRemoteCommandCenter.shared()

    override func setUp() async throws {
        try await super.setUp()
        remote = RemoteCommands(center: center, engine: engine)
    }

    override func tearDown() async throws {
        remote.detach(); remote = nil
        try await super.tearDown()
    }

    func testRepeatCommandSetsTheEngineMode() {
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        XCTAssertEqual(remote.changeRepeatNow(.all), .success)
        XCTAssertEqual(engine.modes.repeatMode, .all)
        XCTAssertEqual(center.changeRepeatModeCommand.currentRepeatType, .all)
        XCTAssertEqual(remote.changeRepeatNow(.one), .success)
        XCTAssertEqual(engine.modes.repeatMode, .one)
        XCTAssertEqual(remote.changeRepeatNow(.off), .success)
        XCTAssertEqual(engine.modes.repeatMode, .off)
        XCTAssertFalse(engine.modes.shuffle)                   // the other mode is left alone
    }

    func testShuffleCommandSetsTheEngineMode() {
        engine.handle(.setQueue(q([1, 2, 3, 4], index: 0, pos: 0, play: true)))
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        let loads = backend.loads.count
        XCTAssertEqual(remote.changeShuffleNow(.items), .success)
        XCTAssertTrue(engine.modes.shuffle)
        XCTAssertEqual(engine.modes.repeatMode, .all)
        XCTAssertEqual(center.changeShuffleModeCommand.currentShuffleType, .items)
        XCTAssertEqual(backend.loads.count, loads)              // the current item plays on
        XCTAssertEqual(remote.changeShuffleNow(.off), .success)
        XCTAssertFalse(engine.modes.shuffle)
        XCTAssertEqual(center.changeShuffleModeCommand.currentShuffleType, .off)
        XCTAssertEqual(remote.changeShuffleNow(.collections), .success)
        XCTAssertTrue(engine.modes.shuffle)
    }

    /// The page's buttons (`setModes`) show on the lock screen too, and a restored engine's modes on attach.
    func testTheCommandsShowTheEngineModes() {
        engine.handle(.setModes(shuffle: true, repeatMode: .one))
        XCTAssertEqual(center.changeRepeatModeCommand.currentRepeatType, .one)
        XCTAssertEqual(center.changeShuffleModeCommand.currentShuffleType, .items)
        remote.detach()
        makeEngine()                                           // restored with the saved modes
        remote = RemoteCommands(center: center, engine: engine)
        XCTAssertEqual(center.changeRepeatModeCommand.currentRepeatType, .one)
        engine.handle(.setModes(shuffle: false, repeatMode: .off))
        XCTAssertEqual(center.changeRepeatModeCommand.currentRepeatType, .off)
        XCTAssertEqual(center.changeShuffleModeCommand.currentShuffleType, .off)
    }

    func testBothAreOffWhileAnEpisodePlays() {
        XCTAssertTrue(center.changeRepeatModeCommand.isEnabled)
        XCTAssertTrue(center.changeShuffleModeCommand.isEnabled)
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertFalse(center.changeRepeatModeCommand.isEnabled)
        XCTAssertFalse(center.changeShuffleModeCommand.isEnabled)
        XCTAssertEqual(remote.changeRepeatNow(.one), .noActionableNowPlayingItem)
        XCTAssertEqual(remote.changeShuffleNow(.items), .noActionableNowPlayingItem)
        XCTAssertEqual(engine.modes, PlayModes())
        engine.handle(.stop(.episode))
        XCTAssertTrue(center.changeRepeatModeCommand.isEnabled)
        XCTAssertTrue(center.changeShuffleModeCommand.isEnabled)
    }
}
