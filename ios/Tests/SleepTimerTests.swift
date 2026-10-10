import XCTest
@testable import Lark

/// The sleep timer runs in the engine, on its scheduler, so it keeps time with the phone locked. At the
/// deadline the volume fades to 0 over 10 s, playback pauses and the volume is back at 1 for the next play.
/// "End of this track" fades over the item's last 10 s and stops at its end.
final class SleepTimerTests: EngineTestCase {
    func lastState(_ k: Item.Kind) -> StateEvent? { states.last { $0.kind == k } }

    func playMusic(_ ids: [Int] = [1, 2, 3]) {
        engine.handle(.setQueue(q(ids, index: 0, pos: 0, play: true)))
        backend.start()
    }

    func testMinutesFadesThenPausesAndRestoresVolume() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        scheduler.advance(15 * 60 - 10)
        XCTAssertTrue(engine.playing)
        XCTAssertTrue(backend.volumes.allSatisfy { $0 == 1 }, "no fade before the last 10 s: \(backend.volumes)")
        scheduler.advance(5)
        XCTAssertLessThan(try XCTUnwrap(backend.volumes.last), 1)
        XCTAssertGreaterThan(try XCTUnwrap(backend.volumes.last), 0)
        XCTAssertTrue(engine.playing)
        scheduler.advance(5)
        XCTAssertFalse(engine.playing)
        XCTAssertTrue(backend.calls.contains("pause"))
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertTrue(backend.volumes.contains(0), "the fade reaches silence before the pause")
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
        XCTAssertNil(lastState(.episode)?.sleepRemainingMs)
    }

    /// The fade is 40 steps of 0.25 s, each quieter than the one before, and the pause comes after silence.
    func testFadeStepsGoDown() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        scheduler.advance(15 * 60)
        let fade = backend.volumes.dropLast()                  // the last one is the restore
        XCTAssertEqual(fade.count, 40)
        XCTAssertEqual(fade.first ?? 0, 39.0 / 40, accuracy: 0.0001)
        XCTAssertEqual(fade.last, 0)
        XCTAssertEqual(Array(fade), fade.sorted(by: >))
        let pause = backend.calls.lastIndex(of: "pause")
        XCTAssertNotNil(pause)
    }

    func testCancelDuringFadeRestoresVolumeAndKeepsPlaying() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        scheduler.advance(15 * 60 - 5)
        XCTAssertLessThan(try XCTUnwrap(backend.volumes.last), 1)
        engine.handle(.sleepTimer(.cancel))
        XCTAssertEqual(backend.volumes.last, 1)
        let volumes = backend.volumes.count
        scheduler.advance(60)
        XCTAssertTrue(engine.playing)
        XCTAssertFalse(backend.calls.contains("pause"))
        XCTAssertEqual(backend.volumes.count, volumes, "no step of the old fade runs after a cancel")
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
    }

    /// A new choice replaces the one set: only one timer runs.
    func testANewChoiceReplacesTheOldOne() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        engine.handle(.sleepTimer(.minutes(30)))
        scheduler.advance(15 * 60 + 1)
        XCTAssertTrue(engine.playing)
        XCTAssertTrue(backend.volumes.allSatisfy { $0 == 1 })
        scheduler.advance(15 * 60)
        XCTAssertFalse(engine.playing)
    }

    func testStateReportsRemaining() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        XCTAssertEqual(lastState(.track)?.sleepRemainingMs, 900_000)
        XCTAssertEqual(lastState(.episode)?.sleepRemainingMs, 900_000)          // engine-wide: both kinds say it
        advanceClock(60); scheduler.advance(60)
        backend.play(to: 1_000)                                                 // a tick sends a state
        XCTAssertEqual(lastState(.track)?.sleepRemainingMs, 840_000)
    }

    func testNoTimerSaysNull() throws {
        playMusic()
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(NativeEvent.state(XCTUnwrap(lastState(.track))))) as? [String: Any]
        XCTAssertTrue(json?["sleepRemainingMs"] is NSNull, "an explicit null: the web tells a new app from an old one by the key")
    }

    /// The minutes timer runs whether or not anything plays: paused at the deadline, it stays paused.
    func testDeadlineWhilePausedStaysPaused() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        engine.pause()
        scheduler.advance(15 * 60)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
        engine.play()
        XCTAssertTrue(engine.playing)
    }

    // MARK: - End of this track

    func testEndOfTrackStopsAtBoundaryWithNextLoadedPaused() {
        playMusic()
        XCTAssertNotNil(backend.preloads.last ?? nil)
        engine.handle(.sleepTimer(.endOfTrack))
        XCTAssertEqual(backend.preloads.last, .some(nil), "nothing preloaded: the queue player must not run into the next item")
        XCTAssertEqual(lastState(.track)?.sleepRemainingMs, 200_000)
        backend.play(to: 189_000)
        XCTAssertTrue(backend.volumes.allSatisfy { $0 == 1 })
        backend.play(to: 195_000)
        XCTAssertEqual(try XCTUnwrap(backend.volumes.last), 0.5, accuracy: 0.001)
        XCTAssertEqual(lastState(.track)?.sleepRemainingMs, 5_000)
        let loads = backend.loads.count
        backend.finish()
        XCTAssertEqual(backend.loads.count, loads + 1)
        XCTAssertEqual(engine.current?.id, "2")
        XCTAssertEqual(backend.loads.last?.2, false, "the next track is loaded, not played")
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
        XCTAssertNotNil(backend.preloads.last ?? nil, "disarmed: the next item is preloaded again")
        engine.play()
        XCTAssertTrue(engine.playing)
    }

    /// Repeat one would replay the track: the sleep timer wins.
    func testEndOfTrackBeatsRepeatOne() {
        engine.handle(.setModes(shuffle: false, repeatMode: .one))
        playMusic()
        engine.handle(.sleepTimer(.endOfTrack))
        XCTAssertEqual(backend.preloads.last, .some(nil))
        backend.finish()
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.loads.last?.2, false)
    }

    /// Skipping to another track carries the timer over to it, at full volume again.
    func testEndOfTrackFollowsASkip() {
        playMusic()
        engine.handle(.sleepTimer(.endOfTrack))
        backend.play(to: 195_000)
        XCTAssertLessThan(try XCTUnwrap(backend.volumes.last), 1)
        engine.next()
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertEqual(backend.preloads.last, .some(nil))
        XCTAssertEqual(lastState(.track)?.sleepRemainingMs, 200_000)
        backend.start(); backend.finish()
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(engine.current?.id, "3")
    }

    func testCancelEndOfTrackPreloadsAgain() {
        playMusic()
        engine.handle(.sleepTimer(.endOfTrack))
        backend.play(to: 195_000)
        engine.handle(.sleepTimer(.cancel))
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNotNil(backend.preloads.last ?? nil)
        backend.finish()
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(backend.loads.last?.2, true)
    }

    /// The last track of a list: it simply stops, as it would anyway, and the volume is back.
    func testEndOfTrackOnTheLastTrack() {
        playMusic([1])
        engine.handle(.sleepTimer(.endOfTrack))
        backend.play(to: 199_500)
        backend.finish()
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
    }

    // MARK: - Episodes

    func testSleepAppliesToEpisodes() {
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [episode("a"), episode("b")], index: 0, positionMs: 0, play: true, source: .list)))
        backend.start()
        engine.handle(.sleepTimer(.minutes(15)))
        XCTAssertEqual(lastState(.episode)?.sleepRemainingMs, 900_000)
        scheduler.advance(15 * 60)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertTrue(backend.volumes.contains(0))
    }

    /// "End of this episode" stops on the finished episode (as the web player does): nothing more plays.
    func testEndOfEpisodeStaysOnTheFinishedEpisode() {
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [episode("a", durationS: 100), episode("b", durationS: 100)], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        backend.start()
        engine.handle(.sleepTimer(.endOfTrack))
        XCTAssertEqual(backend.preloads.last, .some(nil))
        XCTAssertEqual(lastState(.episode)?.sleepRemainingMs, 100_000)
        backend.play(to: 95_000)
        XCTAssertEqual(try XCTUnwrap(backend.volumes.last), 0.5, accuracy: 0.001)
        let loads = backend.loads.count
        backend.finish()
        XCTAssertEqual(backend.loads.count, loads, "the next episode does not start")
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(engine.current?.id, "a")
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNil(lastState(.episode)?.sleepRemainingMs)
    }

    // MARK: - The phone asleep

    /// The scheduler's clock stops while the phone sleeps (paused, locked); the deadline is wall time. Played
    /// again after the deadline passed: the timer is over (it would have paused a paused player), not a pause now.
    func testADeadlinePassedWhileAsleepIsOverOnPlay() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        engine.pause()
        advanceClock(20 * 60)                                  // asleep: the scheduler did not move
        engine.play()
        XCTAssertTrue(engine.playing)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs, "never 0:00 while it plays on")
        XCTAssertTrue(backend.volumes.allSatisfy { $0 == 1 })
        scheduler.advance(20 * 60)                             // the late job, once awake: cancelled
        XCTAssertTrue(engine.playing)
        XCTAssertFalse(backend.volumes.contains { $0 < 1 })
    }

    /// Woken inside the last 10 s (by wall time): the fade starts at once, on the next play or tick.
    func testTheFadeStartsByWallTimeWhenTheSchedulerIsLate() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        engine.pause()
        advanceClock(15 * 60 - 5)
        engine.play()
        XCTAssertTrue(engine.playing)
        scheduler.advance(10)
        XCTAssertFalse(engine.playing)
        XCTAssertTrue(backend.volumes.contains(0))
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
        let volumes = backend.volumes.count
        scheduler.advance(15 * 60)                             // the original job never runs a second fade
        XCTAssertEqual(backend.volumes.count, volumes)
    }

    func testATickPastTheFadeStartStartsIt() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        advanceClock(15 * 60 - 8)
        backend.play(to: 500)
        scheduler.advance(10)
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.volumes.last, 1)
    }

    // MARK: - End of track with the modes and never-stop

    /// Repeat all at the last item: the new pass starts with the first item, loaded but not played.
    func testEndOfTrackUnderRepeatAllWrapsPaused() {
        engine.handle(.setModes(shuffle: false, repeatMode: .all))
        engine.handle(.setQueue(q([1, 2], index: 1, pos: 0, play: true)))
        backend.start()
        engine.handle(.sleepTimer(.endOfTrack))
        backend.finish()
        XCTAssertEqual(engine.current?.id, "1")
        XCTAssertEqual(backend.loads.last?.2, false)
        XCTAssertFalse(engine.playing)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
    }

    /// Never-stop at the end of a list: the cached favorite it picks is loaded, not played.
    func testEndOfTrackWithANeverStopFavoriteLoadsItPaused() {
        cache.favorites = [trackModel(9)]
        playMusic([1])
        engine.handle(.sleepTimer(.endOfTrack))
        backend.finish()
        XCTAssertEqual(engine.current?.id, "9")
        XCTAssertEqual(backend.loads.last?.2, false)
        XCTAssertFalse(engine.playing)
    }

    /// A favorites queue whose refill only arrives after the stop: the new tracks are queued, nothing plays.
    func testARefillAfterTheStopStaysPaused() async {
        api.refillError = LarkError.badResponse
        engine.handle(.setQueue(q([1], index: 0, pos: 0, play: true, source: .favorites)))
        backend.start()
        await engine.idle()
        engine.handle(.sleepTimer(.endOfTrack))
        backend.finish()
        XCTAssertFalse(engine.playing)
        let loads = backend.loads.count
        api.refillError = nil
        api.randomFavoritesAnswer = ("favorites", [trackModel(5), trackModel(6)])
        scheduler.advance(PlayerEngine.refillRetry)
        await engine.idle()
        XCTAssertTrue(engine.music.items.contains { $0.id == "5" }, "the refill arrived")
        XCTAssertFalse(engine.playing)
        XCTAssertEqual(backend.loads.count, loads)
    }

    // MARK: - Sign-out

    func testResetDropsTheTimer() {
        playMusic()
        engine.handle(.sleepTimer(.minutes(15)))
        scheduler.advance(15 * 60 - 5)
        engine.reset()
        XCTAssertEqual(backend.volumes.last, 1)
        XCTAssertNil(lastState(.track)?.sleepRemainingMs)
        let volumes = backend.volumes.count
        scheduler.advance(60)
        XCTAssertEqual(backend.volumes.count, volumes, "no step of the fade runs after a sign-out")
    }
}
