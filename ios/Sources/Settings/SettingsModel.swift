import Foundation

/// What the settings sheet shows and does. Everything goes through `AppServices`: the cap
/// (`setCacheCap(gb:)`), the cache (`cacheStore`), the sync (`syncFavorites()`), the server change
/// (`changeServer()`), and the test button through the same path as the 随机播放收藏 intent.
@MainActor final class SettingsModel: ObservableObject {
    static let capChoices = [1, 2, 5, 10]
    static let defaultCapGB = 2

    private let services: AppServices
    @Published private(set) var capGB: Int
    @Published private(set) var usedBytes: Int64 = 0
    /// A one-line answer to the last button (sync started, the test could not run).
    @Published var message: String?

    init(services: AppServices) {
        self.services = services
        capGB = services.cacheCapGB
        refresh()
    }

    var server: String { services.serverURL?.absoluteString ?? "未设置" }
    var capBytes: Int64 { Int64(capGB) << 30 }
    var signedIn: Bool { services.cacheStore != nil }

    /// "已用 12.3 MB / 上限 2 GB"; signed out, only the cap.
    var usageText: String {
        let cap = "\(capGB) GB"
        guard signedIn else { return "未登录 · 上限 \(cap)" }
        return "已用 \(ByteCountFormatter.string(fromByteCount: usedBytes, countStyle: .binary)) / 上限 \(cap)"
    }

    /// "0.1.0 (42)": the version and the build.
    static var versionText: String {
        let info = Bundle.main.infoDictionary ?? [:]
        return "\(info["CFBundleShortVersionString"] as? String ?? "?") (\(info["CFBundleVersion"] as? String ?? "?"))"
    }

    func refresh() {
        usedBytes = services.cacheStore?.usedBytes ?? 0
        capGB = services.cacheCapGB
    }

    /// One of `capChoices`; anything else is ignored. Saved, and applied to the cache at once (it evicts).
    func setCap(_ gb: Int) {
        services.setCacheCap(gb: gb)
        refresh()
    }

    func syncNow() {
        services.syncFavorites()
        message = "正在同步收藏（仅在 Wi‑Fi 下下载）"
    }

    func clearCache() {
        services.cacheStore?.clear()
        refresh()
        message = "缓存已清空"
    }

    /// The 测试 button: exactly what the Shortcuts automation runs.
    func testShuffle() async {
        do {
            let outcome = try await IntentRun.shuffleFavorites(services.intentPlayer)
            message = outcome == .alreadyPlaying ? outcome.text : nil
        } catch let e as LarkIntentError {
            message = e.text                  // the same reason the Shortcuts automation shows
        } catch {
            message = PlayerEngine.cannotPlay
        }
    }

    /// 更换服务器: signs out of this server and goes back to the server screen.
    func changeServer() async {
        await services.changeServer()
    }
}
