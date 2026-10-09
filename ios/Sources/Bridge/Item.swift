import Foundation

/// One queue entry, a music track or a 频道 episode. `meta` is the web's own Track or Episode object,
/// stored and echoed back unchanged so the web renders its existing components from it.
struct Item: Codable, Equatable, Identifiable {
    enum Kind: String, Codable { case track, episode }
    let kind: Kind
    let id: String            // track: "123"; episode: the YouTube video id
    let title: String
    let artist: String        // track artist / episode channel
    let album: String         // track album / "频道"
    let durationMs: Int
    let meta: JSONValue

    init(kind: Kind, id: String, title: String, artist: String, album: String, durationMs: Int, meta: JSONValue) {
        self.kind = kind; self.id = id; self.title = title; self.artist = artist; self.album = album
        self.durationMs = durationMs; self.meta = meta
    }

    private enum CodingKeys: String, CodingKey { case kind, id, title, artist, album, durationMs, meta }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decode(Kind.self, forKey: .kind)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        artist = try c.decodeIfPresent(String.self, forKey: .artist) ?? ""
        album = try c.decodeIfPresent(String.self, forKey: .album) ?? ""
        durationMs = try c.decodeLenientIntIfPresent(forKey: .durationMs) ?? 0
        meta = try c.decodeIfPresent(JSONValue.self, forKey: .meta) ?? .null
    }
}

enum QueueSource: String, Codable { case list, radio, restored, shuffle, favorites }

extension KeyedDecodingContainer {
    /// Milliseconds from JS may be fractional (`currentTime * 1000`); round them instead of failing.
    func decodeLenientIntIfPresent(forKey key: Key) throws -> Int? {
        guard contains(key), try !decodeNil(forKey: key) else { return nil }
        if let i = try? decode(Int.self, forKey: key) { return i }
        let d = try decode(Double.self, forKey: key)
        guard d.isFinite, abs(d) < 1e15 else {
            throw DecodingError.dataCorruptedError(forKey: key, in: self, debugDescription: "number out of range")
        }
        return Int(d.rounded())
    }

    func decodeLenientInt(forKey key: Key) throws -> Int {
        guard let v = try decodeLenientIntIfPresent(forKey: key) else {
            throw DecodingError.keyNotFound(key, .init(codingPath: codingPath, debugDescription: "missing \(key.stringValue)"))
        }
        return v
    }
}
