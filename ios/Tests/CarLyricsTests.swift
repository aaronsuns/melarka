import XCTest
@testable import Lark

/// The same rules as the web's `lyricsCache.ts` (`activeLine`, `isBlankLine`) and the car-lyrics `take`.
final class CarLyricsTests: XCTestCase {
    func doc(lines: [(Int, String)] = [(0, "a"), (1000, "b")], offset: Int? = nil, synced: Bool = true,
             instrumental: Bool? = nil, found: Bool = true) -> LyricsDoc {
        lyricsDoc(lines: lines, offset: offset, synced: synced, instrumental: instrumental, found: found)
    }

    func testLineAtAppliesOffset() {
        let c = CarLyrics(doc(lines: [(0, "前奏"), (1000, "第一句"), (5000, "第二句")], offset: 500))!
        XCTAssertNil(c.line(atMs: 1400))          // 1400 − 500 = 900 → "前奏", a marker → nil (title shows)
        XCTAssertEqual(c.line(atMs: 1600), "第一句")
        XCTAssertEqual(c.line(atMs: 5600), "第二句")
    }

    func testBeforeTheFirstLineAndNegativeOffset() {
        let c = CarLyrics(doc(lines: [(2000, "一"), (4000, "二")], offset: -1000))!
        XCTAssertNil(c.line(atMs: 900))           // 900 + 1000 = 1900: before the first line
        XCTAssertEqual(c.line(atMs: 1000), "一")  // exactly at t_ms + offset
        XCTAssertEqual(c.line(atMs: 3000), "二")
        XCTAssertEqual(c.line(atMs: 999_999), "二")
    }

    func testLineIsTrimmed() {
        let c = CarLyrics(doc(lines: [(0, "  晴天  ")]))!
        XCTAssertEqual(c.line(atMs: 10), "晴天")
    }

    func testBlankAndMarkerLines() {
        for s in ["", "  ", "♪♪", "(Instrumental)", "【间奏】", "（純音樂）", "尾奏", "MUSIC", " [Interlude] ", "...", "纯音乐"] {
            XCTAssertTrue(CarLyrics.isBlank(s), s)
        }
        for s in ["Music is life", "间奏之后", "1", "a"] { XCTAssertFalse(CarLyrics.isBlank(s), s) }
    }

    func testABlankLineShowsTheTitle() {
        let c = CarLyrics(doc(lines: [(0, "一"), (1000, "♪"), (2000, "二")]))!
        XCTAssertEqual(c.line(atMs: 500), "一")
        XCTAssertNil(c.line(atMs: 1500))
        XCTAssertEqual(c.line(atMs: 2500), "二")
    }

    func testUnsyncedOrInstrumentalGivesNil() {
        XCTAssertNil(CarLyrics(doc(synced: false)))
        XCTAssertNil(CarLyrics(doc(instrumental: true)))
        XCTAssertNil(CarLyrics(doc(found: false)))
        XCTAssertNil(CarLyrics(doc(lines: [])))
        XCTAssertNil(CarLyrics(LyricsDoc(found: true, synced: true, instrumental: nil, lines: nil, offset_ms: nil, text: nil)))
        XCTAssertNotNil(CarLyrics(doc(instrumental: false)))
    }
}
