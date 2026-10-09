import Foundation

/// The current lyric line for the car display and the lock screen: it becomes the Now Playing title, and the
/// song moves to the artist line. The same rules as the web's car lyrics (`PlayerProvider`'s `take` and
/// `lyricsCache.ts` `activeLine` / `isBlankLine`): only found, synced, non-instrumental lyrics with lines; a
/// line shows at `t_ms + offset_ms`; a line with nothing to read shows the song instead.
struct CarLyrics: Equatable {
    let lines: [LyricsDoc.Line]
    let offsetMs: Int

    /// nil unless found && synced && !instrumental && lines non-empty.
    init?(_ doc: LyricsDoc) {
        guard doc.found, doc.synced, doc.instrumental != true, let lines = doc.lines, !lines.isEmpty else { return nil }
        self.lines = lines
        offsetMs = doc.offset_ms ?? 0
    }

    /// The last line whose time has come, trimmed; nil before the first line or on a blank one (the song title shows).
    func line(atMs position: Int) -> String? {
        let ms = position - offsetMs
        var lo = 0, hi = lines.count            // hi: the first index with t_ms > ms
        while lo < hi {
            let mid = (lo + hi) / 2
            if lines[mid].t_ms <= ms { lo = mid + 1 } else { hi = mid }
        }
        guard lo > 0 else { return nil }
        let text = lines[lo - 1].text
        return Self.isBlank(text) ? nil : text.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// A literal port of the web's `MARKER` (case-insensitive): an "(Instrumental)"-style line.
    private static let marker = try! NSRegularExpression(
        pattern: #"^[\s(（\[【]*(instrumental|music|interlude|间奏|間奏|纯音乐|純音樂|前奏|尾奏)[\s)）\]】]*$"#,
        options: [.caseInsensitive])
    private static let readable = try! NSRegularExpression(pattern: #"[\p{L}\p{N}]"#)

    /// Nothing to read on a car display: empty, only symbols or punctuation, or a marker.
    static func isBlank(_ s: String) -> Bool {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        let r = NSRange(t.startIndex..., in: t)
        return t.isEmpty || readable.firstMatch(in: t, range: r) == nil || marker.firstMatch(in: t, range: r) != nil
    }
}
