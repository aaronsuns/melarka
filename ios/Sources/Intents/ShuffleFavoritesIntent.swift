import AppIntents

/// What the Shortcuts intents drive: the engine in the app, a fake in tests (`AppServices.intentPlayer`).
@MainActor protocol IntentPlayer: AnyObject {
    /// Why nothing can start (signed out; offline with nothing on the phone), asked before the session is
    /// activated; nil when something may play. `resume`: the saved queue counts too.
    func intentBlocker(resume: Bool) -> LarkIntentError?
    /// Activates the audio session, synchronously, before anything is awaited.
    func prepareForPlayback()
    func shuffleFavorites() async
    func resumeOrShuffleFavorites() async
    /// Playing, or about to (loading, buffering).
    var playing: Bool { get }
    /// Why the last attempt did not start.
    var failureText: String? { get }
}

extension PlayerEngine: IntentPlayer {}

/// An intent that cannot play. Its text is the dialog Shortcuts shows (the automation reports a failure).
enum LarkIntentError: Error, Equatable, CustomLocalizedStringResourceConvertible {
    /// No server yet: Lark has never been set up on this phone.
    case notSetUp
    /// No token: signed out (a logout, or a 401).
    case notSignedIn
    /// No network, and nothing on the phone to play.
    case offlineNothingCached
    /// It was tried and did not start (the server could not be reached, …): the engine's reason.
    case nothingToPlay(String)

    var localizedStringResource: LocalizedStringResource {
        switch self {
        case .notSetUp: return "请先打开 Melarka 并设置服务器"
        case .notSignedIn: return "未登录"
        case .offlineNothingCached: return "离线且没有缓存的歌曲"
        case .nothingToPlay(let text): return "\(text)"
        }
    }

    /// The dialog text, in the app's language.
    var text: String { String(localized: localizedStringResource) }
}

/// What a shuffle intent did: started, or nothing because Lark was already playing.
enum IntentOutcome: Equatable {
    case started
    /// Already playing (the car's own play resumed the queue first): the shortcut leaves it alone.
    case alreadyPlaying

    var dialog: LocalizedStringResource {
        switch self {
        case .started: return "正在随机播放收藏"
        case .alreadyPlaying: return "已在播放"
        }
    }

    var text: String { String(localized: dialog) }
}

/// The intents' bodies. Nothing that cannot play activates the session (it would stop the car's other audio
/// for nothing); otherwise the session is activated before the first await (as the car's play command does),
/// because iOS may suspend a background-launched app while it waits. A start that fails throws its reason.
enum IntentRun {
    /// Already playing: nothing at all (no session, no new queue). When the car connects, its own play resumes
    /// the queue and the Bluetooth automation's 随机播放收藏 arrives a moment later; it must not cut that song.
    @discardableResult
    @MainActor static func shuffleFavorites(_ player: IntentPlayer?) async throws -> IntentOutcome {
        if let player, player.playing { return .alreadyPlaying }
        let player = try ready(player, resume: false)
        player.prepareForPlayback()
        await player.shuffleFavorites()
        try check(player)
        return .started
    }

    @MainActor static func resume(_ player: IntentPlayer?) async throws {
        let player = try ready(player, resume: true)
        player.prepareForPlayback()
        await player.resumeOrShuffleFavorites()
        try check(player)
    }

    @MainActor private static func ready(_ player: IntentPlayer?, resume: Bool) throws -> IntentPlayer {
        guard let player else { throw LarkIntentError.notSetUp }
        if let blocker = player.intentBlocker(resume: resume) { throw blocker }
        return player
    }

    @MainActor private static func check(_ player: IntentPlayer) throws {
        guard !player.playing else { return }
        throw LarkIntentError.nothingToPlay(player.failureText ?? PlayerEngine.cannotPlay)
    }
}

/// 随机播放收藏: the Bluetooth automation's action. It runs in the background (no UI) and plays cached
/// favorites at once, with or without a network.
struct ShuffleFavoritesIntent: AudioPlaybackIntent {
    static let title: LocalizedStringResource = "随机播放收藏"
    static let description = IntentDescription("在后台随机播放收藏的歌曲；没有网络时播放已缓存的收藏。")
    static let openAppWhenRun = false

    @MainActor func perform() async throws -> some IntentResult & ProvidesDialog {
        let outcome = try await IntentRun.shuffleFavorites(AppServices.shared.intentPlayer)
        return .result(dialog: IntentDialog(outcome.dialog))
    }
}
