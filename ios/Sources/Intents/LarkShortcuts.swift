import AppIntents

/// The App Shortcuts Lark offers in Shortcuts and Siri, with no setup. The phrases are English here and translated
/// in AppShortcuts.xcstrings; every translation must keep `${applicationName}`.
struct LarkShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(intent: ShuffleFavoritesIntent(), phrases: ["Shuffle favorites in \(.applicationName)", "\(.applicationName) shuffle favorites"],
                    shortTitle: "Shuffle Favorites", systemImageName: "shuffle")
        AppShortcut(intent: ResumeIntent(), phrases: ["Resume in \(.applicationName)"], shortTitle: "Resume", systemImageName: "play.fill")
    }
}
