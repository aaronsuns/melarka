import XCTest
import MediaPlayer
@testable import Lark

/// Holding a car's or headphones' fast-forward or rewind (seek forward/backward, begin … end): the position moves
/// while it is held, faster after a while, and stops at the item's edges, on release, or on any other command.
@MainActor final class RemoteScanTests: EngineTestCase {
    var remote: RemoteCommands!
    let center = MPRemoteCommandCenter.shared()

    override func setUp() async throws {
        try await super.setUp()
        remote = RemoteCommands(center: center, scheduler: scheduler, engine: engine)
        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        backend.durationMs = 200_000
    }

    override func tearDown() async throws {
        remote.detach(); remote = nil
        try await super.tearDown()
    }

    func testTheSeekCommandsAreOn() {
        XCTAssertTrue(center.seekForwardCommand.isEnabled)
        XCTAssertTrue(center.seekBackwardCommand.isEnabled)
    }

    func testHoldingForwardStepsThenSpeedsUpUntilReleased() {
        XCTAssertEqual(remote.beginScan(forward: true), .success)
        XCTAssertEqual(backend.positionMs, 5000)                     // a step at once
        scheduler.advance(RemoteCommands.scanTickS)
        XCTAssertEqual(backend.positionMs, 10_000)
        scheduler.advance(RemoteCommands.scanTickS * 4)              // steps 0…5 are the slow ones
        XCTAssertEqual(backend.positionMs, 30_000)
        scheduler.advance(RemoteCommands.scanTickS)
        XCTAssertEqual(backend.positionMs, 45_000)                   // then 15 s a step
        remote.endScan()
        scheduler.advance(5)
        XCTAssertEqual(backend.positionMs, 45_000)
        XCTAssertFalse(remote.isScanning)
        XCTAssertTrue(engine.playing)                                // playing stays as it was
    }

    func testRewindStopsAtTheStartWithoutGoingBack() {
        engine.seek(ms: 7000)
        let loads = backend.loads.count
        XCTAssertEqual(remote.beginScan(forward: false), .success)
        XCTAssertEqual(backend.positionMs, 2000)
        scheduler.advance(RemoteCommands.scanTickS)
        XCTAssertEqual(backend.positionMs, 0)
        XCTAssertFalse(remote.isScanning)
        scheduler.advance(5)
        XCTAssertEqual(backend.loads.count, loads)                   // still the same item
        XCTAssertEqual(engine.current?.id, "1")
    }

    func testForwardStopsJustBeforeTheEnd() {
        engine.seek(ms: 196_000)
        XCTAssertEqual(remote.beginScan(forward: true), .success)
        XCTAssertEqual(backend.positionMs, 200_000 - PlayerEngine.scanEndMarginMs)
        XCTAssertFalse(remote.isScanning)
        XCTAssertEqual(engine.current?.id, "1")                      // the item finishes by itself
    }

    func testAnotherCommandEndsTheScan() {
        XCTAssertEqual(remote.beginScan(forward: true), .success)
        XCTAssertEqual(remote.pauseNow(), .success)
        XCTAssertFalse(remote.isScanning)
        let at = backend.positionMs
        scheduler.advance(5)
        XCTAssertEqual(backend.positionMs, at)
    }

    func testALostReleaseEndsTheScanByItself() {
        backend.durationMs = 10_000_000
        XCTAssertEqual(remote.beginScan(forward: true), .success)
        scheduler.advance(RemoteCommands.scanTickS * Double(RemoteCommands.scanMaxSteps + 10))
        XCTAssertFalse(remote.isScanning)
        let steps = RemoteCommands.scanMaxSteps
        let fast = steps - RemoteCommands.scanFastAfter
        XCTAssertEqual(backend.positionMs, RemoteCommands.scanFastAfter * RemoteCommands.scanStepMs + fast * RemoteCommands.scanFastStepMs)
    }

    func testNothingPlayingIsNotActionable() {
        remote.detach()
        remote = RemoteCommands(center: center, scheduler: scheduler, resolve: { nil })
        XCTAssertEqual(remote.beginScan(forward: true), .noActionableNowPlayingItem)
        XCTAssertFalse(remote.isScanning)
    }
}
