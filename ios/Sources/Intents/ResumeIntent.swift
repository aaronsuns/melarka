import AppIntents

/// Resume (继续播放): the saved queue where it stopped (from the cache first); with no queue, shuffled favorites.
struct ResumeIntent: AudioPlaybackIntent {
    static let title: LocalizedStringResource = "Resume"
    static let description = IntentDescription("Resumes where you left off, in the background; with an empty queue, shuffles your favorites.")
    static let openAppWhenRun = false

    @MainActor func perform() async throws -> some IntentResult {
        try await IntentRun.resume(AppServices.shared.intentPlayer)
        return .result()
    }
}
