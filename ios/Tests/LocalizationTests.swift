import XCTest
import AppIntents
@testable import Lark

/// One string of the app's compiled catalogs in `lang`, read from that `.lproj` (nil when it has no such key).
func localized(_ key: String, _ lang: String, table: String = "Localizable") -> String? {
    guard let path = Bundle.main.path(forResource: lang, ofType: "lproj"), let bundle = Bundle(path: path) else { return nil }
    let missing = "\u{1}missing"
    let s = bundle.localizedString(forKey: key, value: missing, table: table)
    return s == missing ? nil : s
}

/// The native screens follow the phone's language: Chinese phones get Chinese, every other phone English.
final class LocalizationTests: XCTestCase {
    func testKeyStringsResolveInEnglishAndChinese() {
        let cases: [(String, String, String)] = [
            ("Melarka Settings", "Melarka Settings", "Melarka 设置"),
            ("Change Server", "Change Server", "更换服务器"),
            ("Offline Cache", "Offline Cache", "离线缓存"),
            ("Can't Connect to the Server", "Can't Connect to the Server", "无法连接服务器"),
            ("Play Offline Favorites", "Play Offline Favorites", "播放离线收藏"),
            ("Server Address", "Server Address", "服务器地址"),
            ("OK", "OK", "好"),
            ("Shuffle Favorites", "Shuffle Favorites", "随机播放收藏"),
            ("Resume", "Resume", "继续播放"),
            ("Used %@ / limit %@", "Used %@ / limit %@", "已用 %@ / 上限 %@"),
            ("Offline: playing cached favorites", "Offline: playing cached favorites", "离线：正在播放已缓存的收藏"),
        ]
        for (key, en, zh) in cases {
            XCTAssertEqual(localized(key, "en"), en, key)
            XCTAssertEqual(localized(key, "zh-Hans"), zh, key)
            XCTAssertEqual(localized(key, "zh-Hant"), zh, "zh-Hant shows the Simplified Chinese text: \(key)")
        }
    }

    func testAppShortcutPhrasesExistInBothLanguagesWithTheAppName() {
        let phrases: [(String, String)] = [
            ("Shuffle favorites in ${applicationName}", "用${applicationName}随机播放收藏"),
            ("${applicationName} shuffle favorites", "${applicationName} 随机播放收藏"),
            ("Resume in ${applicationName}", "用${applicationName}继续播放"),
        ]
        for (key, zh) in phrases {
            XCTAssertEqual(localized(key, "en", table: "AppShortcuts"), key)
            XCTAssertEqual(localized(key, "zh-Hans", table: "AppShortcuts"), zh)
        }
    }

    func testThePermissionPromptIsTranslated() {
        XCTAssertEqual(localized("NSLocalNetworkUsageDescription", "zh-Hans", table: "InfoPlist"), "开发时连接局域网中的 Melarka 服务器")
        XCTAssertEqual(localized("NSLocalNetworkUsageDescription", "en", table: "InfoPlist"),
                       "Connects to a Melarka server on your local network, for development")
    }

    /// What iOS picks for a phone's language list: Chinese for either script, English for everything else
    /// (English is the development language, the fallback when nothing matches).
    func testThePhonesLanguageChoosesTheLocalization() throws {
        let dev = try XCTUnwrap(Bundle.main.developmentLocalization)
        XCTAssertEqual(dev, "en")
        let available = [dev] + Bundle.main.localizations.filter { $0 != dev && $0 != "Base" }
        func pick(_ prefs: [String]) -> String? { Bundle.preferredLocalizations(from: available, forPreferences: prefs).first }
        XCTAssertEqual(pick(["zh-Hans-CN"]), "zh-Hans")
        XCTAssertEqual(pick(["zh-Hans-SE", "sv-SE"]), "zh-Hans")
        XCTAssertEqual(pick(["zh-Hant-TW"]), "zh-Hant")
        XCTAssertEqual(pick(["zh-Hant-HK"]), "zh-Hant")
        XCTAssertEqual(pick(["en-US"]), "en")
        XCTAssertEqual(pick(["en-SE", "zh-Hans-SE"]), "en")
        XCTAssertEqual(pick(["sv-SE"]), "en")
        XCTAssertEqual(pick(["de-DE", "fr-FR"]), "en")
    }

    /// The guide quotes the Shortcuts action by the name Shortcuts shows in the same language.
    func testTheCarGuideNamesTheActionsInItsOwnLanguage() throws {
        for (lang, shuffle, resume) in [("en", "Shuffle Favorites", "Resume"), ("zh-Hans", "随机播放收藏", "继续播放")] {
            let guide = try XCTUnwrap(localized("shortcutsGuide.text", lang), lang)
            XCTAssertEqual(localized("Shuffle Favorites", lang), shuffle)
            XCTAssertEqual(localized("Resume", lang), resume)
            XCTAssertTrue(guide.contains("\"\(shuffle)\""), lang)
            XCTAssertTrue(guide.contains("\"\(resume)\""), lang)
        }
    }

    /// The running app shows its own language's text, never the key.
    func testTheRunningLanguageResolves() {
        XCTAssertNotEqual(ShortcutsGuide.text, "shortcutsGuide.text")
        let lang = Bundle.main.preferredLocalizations.first ?? "en"
        XCTAssertEqual(ShortcutsGuide.text, localized("shortcutsGuide.text", lang))
        XCTAssertEqual(PlayerEngine.cannotPlay, localized("Can't play", lang))
    }
}
