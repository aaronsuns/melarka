import Foundation

struct SetQueue: Equatable {
    let kind: Item.Kind; let items: [Item]; let index: Int
    let positionMs: Int?      // nil: if items[index] is the loaded item, keep playing it where it is (edits)
    let play: Bool; let source: QueueSource
}

struct NativePrefs: Equatable { let quality: String; let carLyrics: Bool }

/// A message the web posts through `window.larkNative.post(msg)`. Every message is an object with a `type`.
/// Field names on the wire (binding for the web's `nativeBridge.ts`):
/// - `hello {onOpen}`; `setQueue {kind, items, index, positionMs?, play, source}`
/// - `play {kind}`, `pause {kind?}`, `next {kind}`, `prev {kind}`, `stop {kind}`
/// - `seek {kind, ms}`, `skip {kind, ms}` (ms may be negative), `setRate {rate}`
/// - `setPrefs {quality, carLyrics}`, `auth {signedIn, userId?}`, `pauseForWeb`
/// - `favoriteChanged {trackId, on}`, `flushEvents {id}`, `openSettings`
/// Unknown fields are ignored. `auth` never carries the token: native reads it from the HttpOnly cookie.
enum WebMessage: Equatable {
    case hello(onOpen: String)                    // web mounted; native answers with queue+state for both kinds
    case setQueue(SetQueue)
    case play(Item.Kind), pause(Item.Kind?), next(Item.Kind), prev(Item.Kind)
    case seek(Item.Kind, ms: Int), skip(Item.Kind, ms: Int), setRate(Double)
    case stop(Item.Kind)                          // episodes' close(): clear that queue
    case setPrefs(NativePrefs)
    case auth(signedIn: Bool, userId: Int?)
    case pauseForWeb
    case favoriteChanged(trackId: Int, on: Bool)
    case flushEvents(id: String)
    case openSettings

    struct InvalidBody: Error {}

    /// Decodes a `WKScriptMessage.body` (an `NSDictionary` from the page).
    static func decode(_ body: Any) throws -> WebMessage {
        guard body is [String: Any], JSONSerialization.isValidJSONObject(body) else { throw InvalidBody() }
        let data = try JSONSerialization.data(withJSONObject: body)
        return try JSONDecoder().decode(Wire.self, from: data).message
    }

    private struct Wire: Decodable {
        let message: WebMessage

        private enum Keys: String, CodingKey {
            case type, onOpen, kind, items, index, positionMs, play, source, ms, rate, quality, carLyrics
            case signedIn, userId, trackId, on, id
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: Keys.self)
            let type = try c.decode(String.self, forKey: .type)
            func kind() throws -> Item.Kind { try c.decode(Item.Kind.self, forKey: .kind) }
            switch type {
            case "hello": message = .hello(onOpen: try c.decode(String.self, forKey: .onOpen))
            case "setQueue":
                message = .setQueue(SetQueue(
                    kind: try kind(), items: try c.decode([Item].self, forKey: .items),
                    index: try c.decodeLenientInt(forKey: .index), positionMs: try c.decodeLenientIntIfPresent(forKey: .positionMs),
                    play: try c.decode(Bool.self, forKey: .play), source: try c.decode(QueueSource.self, forKey: .source)))
            case "play": message = .play(try kind())
            case "pause": message = .pause(try c.decodeIfPresent(Item.Kind.self, forKey: .kind))
            case "next": message = .next(try kind())
            case "prev": message = .prev(try kind())
            case "seek": message = .seek(try kind(), ms: try c.decodeLenientInt(forKey: .ms))
            case "skip": message = .skip(try kind(), ms: try c.decodeLenientInt(forKey: .ms))
            case "setRate":
                let rate = try c.decode(Double.self, forKey: .rate)
                guard rate.isFinite, rate > 0 else {
                    throw DecodingError.dataCorruptedError(forKey: .rate, in: c, debugDescription: "bad rate")
                }
                message = .setRate(rate)
            case "stop": message = .stop(try kind())
            case "setPrefs":
                message = .setPrefs(NativePrefs(quality: try c.decode(String.self, forKey: .quality),
                                                carLyrics: try c.decode(Bool.self, forKey: .carLyrics)))
            case "auth":   // a stray `token` field is dropped on purpose
                message = .auth(signedIn: try c.decode(Bool.self, forKey: .signedIn), userId: try c.decodeIfPresent(Int.self, forKey: .userId))
            case "pauseForWeb": message = .pauseForWeb
            case "favoriteChanged":
                message = .favoriteChanged(trackId: try c.decode(Int.self, forKey: .trackId), on: try c.decode(Bool.self, forKey: .on))
            case "flushEvents": message = .flushEvents(id: try c.decode(String.self, forKey: .id))
            case "openSettings": message = .openSettings
            default:
                throw DecodingError.dataCorruptedError(forKey: .type, in: c, debugDescription: "unknown message type")
            }
        }
    }
}
