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
@MainActor final class LocalizationTests: XCTestCase {
    func testKeyStringsResolveInEnglishAndChinese() {
        let cases: [(String, String, String, String)] = [
            ("Melarka Settings", "Melarka Settings", "Melarka 设置", "Melarka 設定"),
            ("Change Server", "Change Server", "更换服务器", "更換伺服器"),
            ("Offline Cache", "Offline Cache", "离线缓存", "離線快取"),
            ("Can't Connect to the Server", "Can't Connect to the Server", "无法连接服务器", "無法連線至伺服器"),
            ("Play Offline Favorites", "Play Offline Favorites", "播放离线收藏", "播放離線收藏"),
            ("Server Address", "Server Address", "服务器地址", "伺服器位址"),
            ("OK", "OK", "好", "好"),
            ("Shuffle Favorites", "Shuffle Favorites", "随机播放收藏", "隨機播放收藏"),
            ("Resume", "Resume", "继续播放", "繼續播放"),
            ("Used %@ / limit %@", "Used %@ / limit %@", "已用 %@ / 上限 %@", "已用 %@ / 上限 %@"),
            ("Offline: playing cached favorites", "Offline: playing cached favorites", "离线：正在播放已缓存的收藏", "離線：正在播放已快取的收藏"),
            ("Settings", "Settings", "设置", "設定"),
            ("Up Next", "Up Next", "播放队列", "播放佇列"),
        ]
        for (key, en, hans, hant) in cases {
            XCTAssertEqual(localized(key, "en"), en, key)
            XCTAssertEqual(localized(key, "zh-Hans"), hans, key)
            XCTAssertEqual(localized(key, "zh-Hant"), hant, "zh-Hant is Traditional Chinese (Taiwan wording): \(key)")
        }
    }

    /// Every zh-Hant value of the three catalogs is written in Traditional characters: none of the characters that exist
    /// only in the Simplified form (the same list as Android's `LocalizationTest.SIMPLIFIED_ONLY`) appears in it.
    func testZhHantHoldsNoSimplifiedCharacters() throws {
        let sources = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("Sources")
        var checked = 0
        for file in ["App/Localizable", "App/InfoPlist", "Intents/AppShortcuts"] {
            let data = try Data(contentsOf: sources.appendingPathComponent(file + ".xcstrings"))
            let catalog = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
            let strings = try XCTUnwrap(catalog["strings"] as? [String: [String: Any]])
            for (key, entry) in strings {
                let loc = entry["localizations"] as? [String: [String: Any]]
                guard let unit = loc?["zh-Hant"]?["stringUnit"] as? [String: Any], let value = unit["value"] as? String else { continue }
                XCTAssertEqual(value.filter { Self.simplifiedOnly.contains($0) }, "", "\(file): \(key)")
                checked += 1
            }
        }
        XCTAssertGreaterThan(checked, 80)
    }

    /// Characters that exist only in Simplified Chinese (copied from Android's `LocalizationTest.SIMPLIFIED_ONLY`).
    private static let simplifiedOnly = Set(
        "线说该这们来时会个为与发开关门问题点务网络载设请获离缓单随环选择认证录码错误帮应电运连车蓝显锁暂继续进页资讯节频订阅" +
        "删储级还没确对钮读写间长现话语识让给经过头东动华国学实业产当气乐观欢见觉记讲访评诉译试询详谁调谢输达远递邮释钟银链" +
        "键闭闲闻队阶际陆险隐难顶项顺须预领颜风飞饭马驱验鱼鸟齐龙帐币岁归态总拥换损摄数断晓杂权条样标档桥梦检毕汇浏测济灵热" +
        "爱状独画畅盖础积称稳笔简类纪组细终结绝绍统维编缩联职脑补装视览触计讨许论词课谈财货质购费赛软轻较辑边迟遗铁闪阳饮鸣齿" +
        "无机听复号种广术极")

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
        for (lang, shuffle, resume) in [("en", "Shuffle Favorites", "Resume"), ("zh-Hans", "随机播放收藏", "继续播放"), ("zh-Hant", "隨機播放收藏", "繼續播放")] {
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
