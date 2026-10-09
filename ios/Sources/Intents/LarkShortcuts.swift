import AppIntents

/// The App Shortcuts Lark offers in Shortcuts and Siri, with no setup.
struct LarkShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(intent: ShuffleFavoritesIntent(), phrases: ["用\(.applicationName)随机播放收藏", "\(.applicationName) 随机播放收藏"],
                    shortTitle: "随机播放收藏", systemImageName: "shuffle")
        AppShortcut(intent: ResumeIntent(), phrases: ["用\(.applicationName)继续播放"], shortTitle: "继续播放", systemImageName: "play.fill")
    }
}
