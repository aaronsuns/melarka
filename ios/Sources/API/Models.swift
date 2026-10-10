import Foundation

// Shapes of the Melarka server's /api/v1 JSON (internal/library, internal/personal, internal/lyrics).
// Decoding ignores unknown fields; the raw object is kept next to each Track as `Item.meta`.

struct Track: Codable, Equatable {
    let id: Int; let title, artist, album: String; let duration_ms: Int; let favorite: Bool
    /// The loudness gain the server measured (dB, never above 0); nil until measured. Players read it from
    /// `Item.meta` (`Gain.factor`), so a queued or cached track keeps it.
    var gain_db: Double? = nil
}

struct LyricsDoc: Decodable {
    let found: Bool; let synced: Bool; let instrumental: Bool?; let lines: [Line]?; let offset_ms: Int?
    /// Plain (unsynced) lyrics: the server sends `text` when the lyrics have no timestamps.
    let text: String?
    struct Line: Decodable, Equatable { let t_ms: Int; let text: String }
}

struct ServerQueue: Decodable { let track_ids: [Int]; let current_index: Int; let position_ms: Int; let version: Int; let updated_by: String; let updated_at: Int }

struct PlayEvent: Codable, Equatable { let client_event_id: String; let track_id: Int; let started_at: Int; let played_seconds: Int; let skipped: Bool; let quality: String }

enum LarkError: Error, Equatable {
    case signedOut                              // no token: nothing was sent
    case unauthorized                           // the server answered 401
    case badRequest                             // refused locally, nothing was sent
    case http(status: Int, code: String?)       // any other non-2xx; `code` is the server's stable error code
    case badResponse                            // 2xx with a body or type we cannot use
}

/// Decodes one JSON object twice from the same decoder: as `T` and verbatim as `JSONValue`.
struct Raw<T: Decodable>: Decodable {
    let value: T
    let json: JSONValue
    init(from decoder: Decoder) throws {
        value = try T(from: decoder)
        json = try JSONValue(from: decoder)
    }
}
