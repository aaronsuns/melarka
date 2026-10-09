import Foundation

/// The `state` event: where one kind's player is. Sent on every change and on `hello`.
struct StateEvent: Encodable, Equatable {
    let kind: Item.Kind
    let itemId: String?
    let index: Int
    let playing: Bool
    let positionMs: Int
    let durationMs: Int
    let buffering: Bool
    let error: String?
    let rate: Double
    /// The music queue's modes; an episode's state always says false and "off".
    var shuffle = false
    var repeatMode: RepeatMode = .off

    private enum CodingKeys: String, CodingKey {
        case type, kind, itemId, index, playing, positionMs, durationMs, buffering, error, rate, shuffle
        case repeatMode = "repeat"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode("state", forKey: .type)
        try c.encode(kind, forKey: .kind)
        try c.encode(itemId, forKey: .itemId)       // explicit null, not an absent key
        try c.encode(index, forKey: .index)
        try c.encode(playing, forKey: .playing)
        try c.encode(positionMs, forKey: .positionMs)
        try c.encode(durationMs, forKey: .durationMs)
        try c.encode(buffering, forKey: .buffering)
        try c.encode(error, forKey: .error)
        try c.encode(rate.isFinite ? rate : 1, forKey: .rate)
        try c.encode(shuffle, forKey: .shuffle)
        try c.encode(repeatMode, forKey: .repeatMode)
    }
}

/// An event native dispatches to the page as `window` `lark-native` CustomEvent; `detail` is the encoded event:
/// - `{type:"state", kind, itemId, index, playing, positionMs, durationMs, buffering, error, rate, shuffle, repeat}`
///   (`repeat`: "off" | "all" | "one"; a web without modes ignores both, a web with them shows the buttons)
/// - `{type:"queue", kind, items, index, source}`
/// - `{type:"flushed", id}`, `{type:"authRequired"}`, `{type:"notice", text}`
enum NativeEvent: Encodable, Equatable {
    case state(StateEvent)
    case queue(kind: Item.Kind, items: [Item], index: Int, source: QueueSource)
    case flushed(id: String)
    case authRequired
    case notice(String)      // e.g. PlayerEngine.offlineNotice, in the phone's language

    private enum CodingKeys: String, CodingKey { case type, kind, items, index, source, id, text }

    func encode(to encoder: Encoder) throws {
        if case .state(let s) = self { return try s.encode(to: encoder) }
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .state: break
        case .queue(let kind, let items, let index, let source):
            try c.encode("queue", forKey: .type); try c.encode(kind, forKey: .kind)
            try c.encode(items, forKey: .items); try c.encode(index, forKey: .index); try c.encode(source, forKey: .source)
        case .flushed(let id):
            try c.encode("flushed", forKey: .type); try c.encode(id, forKey: .id)
        case .authRequired:
            try c.encode("authRequired", forKey: .type)
        case .notice(let text):
            try c.encode("notice", forKey: .type); try c.encode(text, forKey: .text)
        }
    }

    /// `window.dispatchEvent(new CustomEvent("lark-native",{detail:<json>}))`, safe to evaluate as a script:
    /// U+2028/U+2029 and `</` are escaped.
    func javaScript() -> String {
        let enc = JSONEncoder()
        enc.nonConformingFloatEncodingStrategy = .convertToString(positiveInfinity: "Infinity", negativeInfinity: "-Infinity", nan: "NaN")
        guard let data = try? enc.encode(self), var json = String(data: data, encoding: .utf8) else { return "void 0" }
        json = json.replacingOccurrences(of: "\u{2028}", with: "\\u2028")
            .replacingOccurrences(of: "\u{2029}", with: "\\u2029")
            .replacingOccurrences(of: "</", with: "<\\/")
        return "window.dispatchEvent(new CustomEvent(\"lark-native\",{detail:\(json)}))"
    }
}
