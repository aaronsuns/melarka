import XCTest
import MediaPlayer
@testable import Lark

func nowPlayingTrack(_ id: Int, title: String, artist: String, album: String = "叶惠美") -> Item {
    Item(kind: .track, id: String(id), title: title, artist: artist, album: album, durationMs: 269_000, meta: .object(["id": .int(id)]))
}

/// The lock screen and the car display: `NowPlayingController` on a counting sink, driven by the engine.
@MainActor final class NowPlayingTests: EngineTestCase {
    var sink: FakeSink!
    var np: NowPlayingController!
    var artworkCalls: [String] = []
    var artworkImage: UIImage?
    var artworkError: Error?
    var remote: RemoteCommands?

    override func setUp() async throws {
        sink = FakeSink()
        artworkCalls = []
        artworkImage = nil
        artworkError = nil
        np = NowPlayingController(sink: sink, artwork: { [unowned self] item in
            self.artworkCalls.append(item.id)
            if let e = self.artworkError { throw e }
            return self.artworkImage
        }, now: { [unowned self] in self.clock })
        nowPlaying = np
        try await super.setUp()
    }

    override func tearDown() async throws {
        remote?.detach(); remote = nil
        try await super.tearDown()
    }

    var info: [String: Any]? { sink.nowPlayingInfo }

    // MARK: Controller

    func testCarLyricsPutsLineInTitleAndSongInArtist() {
        np.show(item: nowPlayingTrack(7, title: "晴天", artist: "周杰伦"), positionMs: 1600, durationMs: 269000, rate: 1, playing: true, lyricLine: "第一句")
        XCTAssertEqual(info?[MPMediaItemPropertyTitle] as? String, "第一句")
        XCTAssertEqual(info?[MPMediaItemPropertyArtist] as? String, "晴天 · 周杰伦")
        XCTAssertEqual(info?[MPMediaItemPropertyAlbumTitle] as? String, "叶惠美")
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double, 1.6)
        XCTAssertEqual(info?[MPMediaItemPropertyPlaybackDuration] as? Double, 269)
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyPlaybackRate] as? Double, 1)
        XCTAssertEqual((info?[MPNowPlayingInfoPropertyMediaType] as? NSNumber)?.uintValue, MPNowPlayingInfoMediaType.audio.rawValue)
    }

    func testNoLineShowsTheSong() {
        np.show(item: nowPlayingTrack(7, title: "晴天", artist: "周杰伦"), positionMs: 0, durationMs: nil, rate: 1, playing: false, lyricLine: nil)
        XCTAssertEqual(sink.title, "晴天")
        XCTAssertEqual(sink.artist, "周杰伦")
        XCTAssertEqual(info?[MPMediaItemPropertyPlaybackDuration] as? Double, 269)    // the item's own duration
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyPlaybackRate] as? Double, 0)     // paused
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyDefaultPlaybackRate] as? Double, 1)
    }

    func testLineWithNoArtistIsTheSongAlone() {
        np.show(item: nowPlayingTrack(7, title: "晴天", artist: ""), positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "第一句")
        XCTAssertEqual(sink.artist, "晴天")
    }

    func testArtworkIsFetchedOncePerItemAndCached() async throws {
        artworkImage = UIGraphicsImageRenderer(size: CGSize(width: 4, height: 4)).image { _ in }
        let item = nowPlayingTrack(7, title: "晴天", artist: "周杰伦")
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        try await waitUntil { self.info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork }
        np.show(item: item, positionMs: 1000, durationMs: nil, rate: 1, playing: true, lyricLine: "一")
        XCTAssertTrue(info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork)       // kept on the next write
        np.show(item: nowPlayingTrack(8, title: "x", artist: "y"), positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        XCTAssertNil(info?[MPMediaItemPropertyArtwork])                               // never another item's
        try await waitUntil { self.info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork }
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        XCTAssertTrue(info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork)       // from the memory cache
        try await Task.sleep(nanoseconds: 100_000_000)
        XCTAssertEqual(artworkCalls, ["7", "8"])
    }

    /// The same artwork object on every write: some head units send the cover again when it changes.
    func testArtworkObjectIsReused() async throws {
        artworkImage = UIGraphicsImageRenderer(size: CGSize(width: 4, height: 4)).image { _ in }
        let item = nowPlayingTrack(7, title: "晴天", artist: "周杰伦")
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        try await waitUntil { self.info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork }
        let first = info?[MPMediaItemPropertyArtwork] as AnyObject
        np.show(item: item, positionMs: 1000, durationMs: nil, rate: 1, playing: true, lyricLine: "一")
        XCTAssertTrue(info?[MPMediaItemPropertyArtwork] as AnyObject === first)
    }

    /// A fetch that failed (offline) is asked again after a backoff; only "no cover" is remembered.
    func testFailedArtworkIsAskedAgainAfterABackoff() async throws {
        artworkError = LarkError.http(status: 503, code: nil)
        let item = nowPlayingTrack(7, title: "晴天", artist: "周杰伦")
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        try await waitUntil { self.artworkCalls.count == 1 }
        try await Task.sleep(nanoseconds: 50_000_000)
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "一")
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(artworkCalls.count, 1)                       // not at once
        advanceClock(NowPlayingController.artworkRetryS)
        artworkError = nil
        artworkImage = UIGraphicsImageRenderer(size: CGSize(width: 4, height: 4)).image { _ in }
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "二")
        try await waitUntil { self.info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork }
        XCTAssertEqual(artworkCalls.count, 2)
    }

    func testFailedArtworkBacksOffLonger() async throws {
        artworkError = LarkError.http(status: 503, code: nil)
        let item = nowPlayingTrack(7, title: "晴天", artist: "周杰伦")
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        try await waitUntil { self.artworkCalls.count == 1 }
        try await Task.sleep(nanoseconds: 50_000_000)
        advanceClock(NowPlayingController.artworkRetryS)
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "一")
        try await waitUntil { self.artworkCalls.count == 2 }
        try await Task.sleep(nanoseconds: 50_000_000)
        advanceClock(NowPlayingController.artworkRetryS)              // the second wait is twice as long
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "二")
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(artworkCalls.count, 2)
        advanceClock(NowPlayingController.artworkRetryS)
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "三")
        try await waitUntil { self.artworkCalls.count == 3 }
    }

    func testNoCoverIsRemembered() async throws {
        let item = nowPlayingTrack(7, title: "晴天", artist: "周杰伦")
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        try await waitUntil { self.artworkCalls.count == 1 }
        try await Task.sleep(nanoseconds: 50_000_000)
        advanceClock(3600)
        np.show(item: item, positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: "一")
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(artworkCalls.count, 1)
    }

    func testArtworkCacheKeepsTwentyItems() async throws {
        artworkImage = UIGraphicsImageRenderer(size: CGSize(width: 4, height: 4)).image { _ in }
        for i in 1...21 {
            np.show(item: nowPlayingTrack(i, title: "t", artist: "a"), positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
            try await waitUntil { self.info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork }
        }
        np.show(item: nowPlayingTrack(21, title: "t", artist: "a"), positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        np.show(item: nowPlayingTrack(2, title: "t", artist: "a"), positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        np.show(item: nowPlayingTrack(1, title: "t", artist: "a"), positionMs: 0, durationMs: nil, rate: 1, playing: true, lyricLine: nil)
        try await waitUntil { self.info?[MPMediaItemPropertyArtwork] is MPMediaItemArtwork }
        XCTAssertEqual(artworkCalls.count, 22)        // 1 was dropped (the oldest) and fetched again; 2 and 21 were kept
        XCTAssertEqual(artworkCalls.last, "1")
    }

    // MARK: Engine

    func playLyricsTrack(carLyrics: Bool, keepAnswer: Bool = false) async {
        engine.prefs = NativePrefs(quality: "high", carLyrics: carLyrics)
        if !keepAnswer { api.lyricsAnswers[7] = lyricsDoc(lines: [(0, "前奏"), (1000, "第一句"), (5000, "第二句")], offset: 500) }
        engine.handle(.setQueue(SetQueue(kind: .track, items: [nowPlayingTrack(7, title: "晴天", artist: "周杰伦")], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        await engine.idle()
        backend.start()
    }

    func tick(_ ms: Int) {
        backend.positionMs = ms
        backend.delegate?.backendTick(positionMs: ms, generation: backend.generation)
    }

    func testEngineUpdatesTitleOnlyWhenLineChanges() async {
        await playLyricsTrack(carLyrics: true)
        XCTAssertEqual(api.lyricsCalls, [7])
        XCTAssertEqual(sink.title, "晴天")
        let before = sink.writes
        tick(1400)                                 // still the marker: no write
        XCTAssertEqual(sink.writes, before)
        tick(1600); tick(1700); tick(1800)
        XCTAssertEqual(sink.writes, before + 1)
        XCTAssertEqual(sink.title, "第一句")
        XCTAssertEqual(sink.artist, "晴天 · 周杰伦")
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double, 1.6)
        tick(5600)
        XCTAssertEqual(sink.writes, before + 2)
        XCTAssertEqual(sink.title, "第二句")
    }

    func testCarLyricsOffShowsTheSongAndNeverAsks() async {
        await playLyricsTrack(carLyrics: false)
        XCTAssertEqual(api.lyricsCalls, [])
        let before = sink.writes
        tick(1600); tick(5600)
        XCTAssertEqual(sink.writes, before)
        XCTAssertEqual(sink.title, "晴天")
        XCTAssertEqual(sink.artist, "周杰伦")
    }

    func testTurningCarLyricsOffPutsTheSongBack() async {
        await playLyricsTrack(carLyrics: true)
        tick(1600)
        XCTAssertEqual(sink.title, "第一句")
        engine.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: false)))
        XCTAssertEqual(sink.title, "晴天")
        engine.handle(.setPrefs(NativePrefs(quality: "high", carLyrics: true)))
        await engine.idle()
        XCTAssertEqual(sink.title, "第一句")
    }

    func testLyricsFailureLeavesTheTitle() async {
        api.lyricsAnswers = [:]
        engine.prefs = NativePrefs(quality: "high", carLyrics: true)
        engine.handle(.setQueue(SetQueue(kind: .track, items: [nowPlayingTrack(9, title: "雨", artist: "B")], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        await engine.idle()
        backend.start()
        tick(3000)
        XCTAssertEqual(sink.title, "雨")
        XCTAssertEqual(api.lyricsCalls, [9])
    }

    /// Lyrics that could not be fetched (offline) are asked again after a backoff, while the track plays.
    func testFailedLyricsAreAskedAgainAfterABackoff() async {
        engine.prefs = NativePrefs(quality: "high", carLyrics: true)
        engine.handle(.setQueue(SetQueue(kind: .track, items: [nowPlayingTrack(7, title: "晴天", artist: "周杰伦")], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        await engine.idle()
        backend.start()
        XCTAssertEqual(api.lyricsCalls, [7])                    // no answer: it threw
        tick(1600)
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [7])                    // not at once
        advanceClock(PlayerEngine.lyricsRetryS)
        tick(1700)
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [7, 7])                 // failed again: the next wait is twice as long
        advanceClock(PlayerEngine.lyricsRetryS)
        tick(1800)
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [7, 7])
        api.lyricsAnswers[7] = lyricsDoc(lines: [(0, "前奏"), (1000, "第一句"), (5000, "第二句")], offset: 500)
        advanceClock(PlayerEngine.lyricsRetryS)
        tick(1900)
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [7, 7, 7])
        XCTAssertEqual(sink.title, "第一句")
        tick(2000)
        advanceClock(3600)
        tick(2100)
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [7, 7, 7])              // found: never asked again
    }

    /// "Not found" is an answer: it is not asked again for this track.
    func testLyricsNotFoundIsNotRetried() async {
        api.lyricsAnswers[7] = lyricsDoc(found: false)
        await playLyricsTrack(carLyrics: true, keepAnswer: true)
        advanceClock(3600)
        tick(1600)
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [7])
    }

    func testPlayPauseAndSeekWriteTheSink() async {
        await playLyricsTrack(carLyrics: true)
        var before = sink.writes
        backend.positionMs = 2000
        engine.handle(.pause(.track))
        XCTAssertEqual(sink.writes, before + 1)
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyPlaybackRate] as? Double, 0)
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double, 2)
        before = sink.writes
        engine.handle(.play(.track))
        XCTAssertEqual(sink.writes, before + 1)
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyPlaybackRate] as? Double, 1)
        before = sink.writes
        engine.handle(.seek(.track, ms: 1800))     // the same line: still written once, for the elapsed time
        XCTAssertEqual(sink.writes, before + 1)
        XCTAssertEqual(info?[MPNowPlayingInfoPropertyElapsedPlaybackTime] as? Double, 1.8)
        before = sink.writes
        engine.handle(.seek(.track, ms: 5600))     // another line: one write, not two
        XCTAssertEqual(sink.writes, before + 1)
        XCTAssertEqual(sink.title, "第二句")
    }

    func testStopClearsNowPlaying() async {
        await playLyricsTrack(carLyrics: true)
        XCTAssertNotNil(info)
        engine.handle(.stop(.track))
        XCTAssertNil(info)
    }

    func testANewTrackDropsTheOldLyrics() async {
        await playLyricsTrack(carLyrics: true)
        tick(1600)
        XCTAssertEqual(sink.title, "第一句")
        engine.handle(.setQueue(SetQueue(kind: .track, items: [nowPlayingTrack(8, title: "七里香", artist: "周杰伦")], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        XCTAssertEqual(sink.title, "七里香")
        await engine.idle()
        backend.start()
        tick(1600)
        XCTAssertEqual(sink.title, "七里香")
    }

    func testEpisodeShowsChannelAndSkipCommands() async {
        let ep = Item(kind: .episode, id: "abcdefghijk", title: "第 12 期", artist: "某频道", album: "频道", durationMs: 3_600_000,
                      meta: .object(["video_id": .string("abcdefghijk")]))
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [ep], index: 0, positionMs: 0, play: true, source: .list)))
        await engine.idle()
        XCTAssertEqual(api.lyricsCalls, [])                                   // never lyrics for an episode
        XCTAssertEqual(sink.title, "第 12 期")
        XCTAssertEqual(sink.artist, "某频道")

        let center = MPRemoteCommandCenter.shared()
        remote = RemoteCommands(center: center, engine: engine)
        remote!.update(for: .episode)
        XCTAssertTrue(center.skipForwardCommand.isEnabled)
        XCTAssertTrue(center.skipBackwardCommand.isEnabled)
        XCTAssertEqual(center.skipForwardCommand.preferredIntervals, [30])
        XCTAssertEqual(center.skipBackwardCommand.preferredIntervals, [15])
        XCTAssertTrue(center.nextTrackCommand.isEnabled)
        XCTAssertTrue(center.previousTrackCommand.isEnabled)
        XCTAssertTrue(center.changePlaybackPositionCommand.isEnabled)
        remote!.update(for: .track)
        XCTAssertFalse(center.skipForwardCommand.isEnabled)
        XCTAssertFalse(center.skipBackwardCommand.isEnabled)
        XCTAssertTrue(center.nextTrackCommand.isEnabled)
        XCTAssertTrue(center.playCommand.isEnabled)
        XCTAssertTrue(center.togglePlayPauseCommand.isEnabled)
    }

    func testRemoteCommandsFollowTheActiveKind() async {
        let center = MPRemoteCommandCenter.shared()
        remote = RemoteCommands(center: center, engine: engine)
        XCTAssertFalse(center.skipForwardCommand.isEnabled)
        engine.handle(.setQueue(SetQueue(kind: .episode, items: [episode("abcdefghijk")], index: 0, positionMs: 0, play: true, source: .list)))
        XCTAssertTrue(center.skipForwardCommand.isEnabled)
        engine.handle(.stop(.episode))
        XCTAssertFalse(center.skipForwardCommand.isEnabled)
    }

    // MARK: Remote command handlers

    func testRemoteHandlers() async {
        remote = RemoteCommands(center: .shared(), engine: engine)
        let r = remote!
        XCTAssertEqual(r.pauseNow(), .noActionableNowPlayingItem)
        XCTAssertEqual(r.nextNow(), .noActionableNowPlayingItem)
        XCTAssertEqual(r.seekNow(seconds: 3), .noActionableNowPlayingItem)

        engine.handle(.setQueue(q([1, 2, 3], index: 0, pos: 0, play: true)))
        backend.start()
        XCTAssertEqual(r.pauseNow(), .success)
        XCTAssertFalse(engine.playing)
        var activations: [Int] = []
        engine.willStartPlayback = { [unowned self] in activations.append(self.backend.calls.count) }
        let calls = backend.calls.count
        XCTAssertEqual(r.toggleNow(), .success)
        XCTAssertTrue(engine.playing)
        XCTAssertEqual(activations, [calls])                  // once, before backend.play
        XCTAssertEqual(backend.calls.last, "play")
        engine.willStartPlayback = nil
        XCTAssertEqual(r.seekNow(seconds: 42.5), .success)
        XCTAssertEqual(backend.calls.last, "seek:42500")
        XCTAssertEqual(r.nextNow(), .success)
        XCTAssertEqual(engine.current?.id, "2")
        backend.positionMs = 0
        XCTAssertEqual(r.previousNow(), .success)
        XCTAssertEqual(engine.current?.id, "1")
        XCTAssertEqual(r.skipNow(seconds: 30), .success)
        XCTAssertEqual(r.skipNow(seconds: -15), .success)
        XCTAssertEqual(backend.calls.last, "seek:15000")
    }

    /// The car's play with nothing loaded (a cold start): the saved queue plays, or cached favorites shuffle.
    func testRemotePlayWithNothingLoadedShufflesFavorites() async throws {
        cache.favorites = [trackModel(11), trackModel(12)]
        cache.local = [11: tmp("11.m4a"), 12: tmp("12.m4a")]
        remote = RemoteCommands(center: .shared(), engine: engine)
        var activations: [Int] = []
        engine.willStartPlayback = { [unowned self] in activations.append(self.backend.loads.count) }
        XCTAssertEqual(remote!.playNow(), .success)
        // R1: the session is active before the handler returns, before the cold start's Task runs.
        XCTAssertEqual(activations, [0])
        await engine.idle()
        try await waitUntil { self.engine.playing }
        XCTAssertEqual(engine.music.source, .favorites)
        XCTAssertEqual(backend.loads.count, 1)
        if case .file = backend.loads[0].0 {} else { XCTFail("a cached favorite plays from the file") }
    }

    func testRemotePlayResumesTheRestoredQueue() async throws {
        engine.handle(.setQueue(q([5, 6], index: 1, pos: 7000, play: false)))
        makeEngine()                      // a relaunch: restored, nothing loaded
        XCTAssertNil(backend.loads.first)
        remote = RemoteCommands(center: .shared(), engine: engine)
        XCTAssertEqual(remote!.playNow(), .success)
        try await waitUntil { self.backend.loads.count == 1 }
        XCTAssertEqual(backend.loads[0].1, 7000)
        XCTAssertEqual(engine.current?.id, "6")
        XCTAssertEqual(sink.title, "T6")
    }
}
