import AppIntents

/// 继续播放: the saved queue where it stopped (from the cache first); with no queue, shuffled favorites.
struct ResumeIntent: AudioPlaybackIntent {
    static let title: LocalizedStringResource = "继续播放"
    static let description = IntentDescription("在后台从上次的位置继续播放；队列为空时随机播放收藏。")
    static let openAppWhenRun = false

    @MainActor func perform() async throws -> some IntentResult {
        try await IntentRun.resume(AppServices.shared.intentPlayer)
        return .result()
    }
}
