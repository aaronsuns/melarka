import XCTest
@testable import Lark

/// Lyrics kept beside each cached track, like its cover: the offline player and the lock screen read them offline.
final class CacheLyricsTests: CacheTestCase {
    func testADownloadAlsoKeepsTheLyrics() async throws {
        api.lyricsAnswers[1] = lyricsDoc(lines: [(0, "一"), (2000, "二")], offset: 300)
        let (t, json) = trackModel(1)
        cache.download(t, meta: json, favorite: true)
        try await settle(downloads: 1)
        try await waitUntil { self.cache.cachedLyrics(trackID: 1) != nil }
        XCTAssertEqual(cache.cachedLyrics(trackID: 1), api.lyricsAnswers[1])
        XCTAssertEqual(api.lyricsCalls, [1])
    }

    func testNoLyricsAreAskedOnceAndAgainAfterAWeek() async throws {
        api.lyricsAnswers[1] = lyricsDoc(found: false)
        try put(1, bytes: 10, fav: true, at: t0)
        await cache.cacheLyrics(trackID: 1)
        await cache.cacheLyrics(trackID: 1)
        XCTAssertNil(cache.cachedLyrics(trackID: 1))
        XCTAssertEqual(api.lyricsCalls, [1])                          // remembered: not asked on every sync
        XCTAssertEqual(cache.missingLyrics([1]), [])
    }

    func testANetworkErrorIsAskedAgainLater() async throws {
        try put(1, bytes: 10, fav: true, at: t0)
        await cache.cacheLyrics(trackID: 1)                           // the fake throws: no answer for 1
        XCTAssertEqual(cache.missingLyrics([1]), [1])
        api.lyricsAnswers[1] = lyricsDoc()
        await cache.cacheLyrics(trackID: 1)
        XCTAssertNotNil(cache.cachedLyrics(trackID: 1))
        XCTAssertEqual(api.lyricsCalls, [1, 1])
    }

    func testAnUncachedTrackHasNoLyricsAndFetchesNone() async {
        api.lyricsAnswers[9] = lyricsDoc()
        await cache.cacheLyrics(trackID: 9)
        XCTAssertNil(cache.cachedLyrics(trackID: 9))
        XCTAssertTrue(api.lyricsCalls.isEmpty)
    }

    /// A fresh answer (an offset changed) replaces the kept lyrics of a cached track; an uncached one keeps none.
    func testUpdateLyricsReplacesTheKeptCopy() async throws {
        api.lyricsAnswers[1] = lyricsDoc(offset: 300)
        try put(1, bytes: 10, fav: true, at: t0)
        await cache.cacheLyrics(trackID: 1)
        cache.updateLyrics(trackID: 1, lyricsDoc(offset: 4500))
        XCTAssertEqual(cache.cachedLyrics(trackID: 1)?.offset_ms, 4500)
        cache.updateLyrics(trackID: 1, lyricsDoc(found: false))     // lyrics gone: kept as "none"
        XCTAssertNil(cache.cachedLyrics(trackID: 1))
        XCTAssertEqual(cache.missingLyrics([1]), [])
        cache.updateLyrics(trackID: 2, lyricsDoc())
        XCTAssertNil(cache.cachedLyrics(trackID: 2))
    }

    func testTheLyricsGoWithTheirTrack() async throws {
        api.lyricsAnswers[1] = lyricsDoc()
        try put(1, bytes: 10, fav: false, at: t0)
        await cache.cacheLyrics(trackID: 1)
        XCTAssertNotNil(cache.cachedLyrics(trackID: 1))
        cache.clear()
        XCTAssertNil(cache.cachedLyrics(trackID: 1))
        try put(1, bytes: 10, fav: false, at: t0)
        XCTAssertNil(cache.cachedLyrics(trackID: 1))                  // the old file went with the clear
    }
}

/// Lyrics as the offline player shows them.
final class OfflineLyricsModelTests: XCTestCase {
    func testTimedLinesFollowThePositionWithTheOffset() throws {
        let l = try XCTUnwrap(OfflineLyrics(lyricsDoc(lines: [(1000, "一"), (3000, " "), (5000, "三")], offset: 500)))
        XCTAssertTrue(l.synced)
        XCTAssertNil(l.index(atMs: 1400))                              // before the first line (1000 + 500)
        XCTAssertEqual(l.index(atMs: 1500), 0)
        XCTAssertEqual(l.index(atMs: 4000), 1)
        XCTAssertEqual(l.nextWords(after: nil), 0)
        XCTAssertEqual(l.nextWords(after: 0), 2)                       // the blank line is passed over
        XCTAssertNil(l.nextWords(after: 2))
        XCTAssertEqual(l.startMs(of: 2), 5500)
    }

    func testPlainLyricsAreText() throws {
        let doc = LyricsDoc(found: true, synced: false, instrumental: nil, lines: nil, offset_ms: nil, text: "第一行\n第二行")
        let l = try XCTUnwrap(OfflineLyrics(doc))
        XCTAssertFalse(l.synced)
        XCTAssertEqual(l.text, "第一行\n第二行")
    }

    func testNothingToShow() {
        XCTAssertNil(OfflineLyrics(lyricsDoc(found: false)))
        XCTAssertNil(OfflineLyrics(lyricsDoc(instrumental: true)))
        XCTAssertNil(OfflineLyrics(LyricsDoc(found: true, synced: false, instrumental: nil, lines: [], offset_ms: nil, text: " ")))
    }
}

/// The engine's car lyrics: a cached track's come from the phone, with no request.
final class CachedCarLyricsTests: EngineTestCase {
    var sink: FakeSink!

    override func setUp() async throws {
        sink = FakeSink()
        nowPlaying = NowPlayingController(sink: sink, artwork: { _ in nil })
        try await super.setUp()
    }

    func testCachedLyricsNeedNoRequestAndWorkOffline() async {
        network.isOnline = false
        cache.local = [7: tmp("7.m4a")]
        cache.lyrics[7] = lyricsDoc(lines: [(0, "前奏"), (1000, "第一句")])
        engine.prefs = NativePrefs(quality: "high", carLyrics: true)
        engine.handle(.setQueue(SetQueue(kind: .track, items: [nowPlayingTrack(7, title: "晴天", artist: "周杰伦")], index: 0,
                                         positionMs: 0, play: true, source: .list)))
        await engine.idle()
        backend.start()
        backend.positionMs = 1200
        backend.delegate?.backendTick(positionMs: 1200, generation: backend.generation)
        XCTAssertEqual(sink.title, "第一句")
        XCTAssertEqual(api.lyricsCalls, [])
    }
}

/// The offline player reads the current song's cached lyrics (once per song).
final class OfflinePlayerLyricsTests: EngineTestCase {
    func testTheCurrentSongsLyrics() {
        cache.local = [1: tmp("1.m4a"), 2: tmp("2.m4a")]
        var reads: [Int] = []
        let docs = [1: lyricsDoc(lines: [(0, "一")])]
        let model = OfflinePlayerModel(player: engine, favorites: { [] }, retry: {}, lyrics: { reads.append($0); return docs[$0] })
        engine.onEvent = { model.receive($0) }
        engine.handle(.setQueue(q([1, 2], index: 0, pos: 0, play: true)))
        XCTAssertEqual(model.lyrics?.lines.first?.text, "一")
        _ = model.lyrics; _ = model.lyrics
        XCTAssertEqual(reads, [1])                                  // read once while the song plays
        engine.handle(.next(.track))
        XCTAssertNil(model.lyrics)                                  // 2 has none cached
    }
}
