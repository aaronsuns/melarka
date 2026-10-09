import Foundation

/// What the settings sheet shows and does. Everything goes through `AppServices`: the cap
/// (`setCacheCap(gb:)`), the cache (`cacheStore`), the sync (`syncFavorites()`), the server change
/// (`changeServer()`), and the test button through the same path as the Shuffle Favorites intent.
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

    var server: String { services.serverURL?.absoluteString ?? String(localized: "Not set") }
    var capBytes: Int64 { Int64(capGB) << 30 }
    var signedIn: Bool { services.cacheStore != nil }

    /// "Used 12.3 MB / limit 2 GB" (in the phone's language); signed out, only the cap.
    var usageText: String {
        let cap = "\(capGB) GB"
        guard signedIn else { return String(localized: "Not signed in · limit \(cap)") }
        let used = ByteCountFormatter.string(fromByteCount: usedBytes, countStyle: .binary)
        return String(localized: "Used \(used) / limit \(cap)")
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
        message = String(localized: "Syncing favorites (downloads only on Wi‑Fi)")
    }

    func clearCache() {
        services.cacheStore?.clear()
        refresh()
        message = String(localized: "Cache cleared")
    }

    /// The Test button: exactly what the Shortcuts automation runs.
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

    /// Change Server: signs out of this server and goes back to the server screen.
    func changeServer() async {
        await services.changeServer()
    }
}
