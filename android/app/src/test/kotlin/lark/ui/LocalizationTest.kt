package lark.ui

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import lark.app.R
import lark.player.EngineTexts
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.w3c.dom.Element
import java.io.File
import java.util.Locale
import javax.xml.parsers.DocumentBuilderFactory

/**
 * iOS `LocalizationTests`: the native screens follow the phone's language. Android has four: English (the default and
 * the fallback for every other language), Simplified Chinese, Traditional Chinese (Taiwan wording) and Swedish.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class LocalizationTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private fun s(id: Int, vararg args: Any) = context.getString(id, *args)

    // MARK: The files themselves

    /** Every translatable key of `values/` is in each locale, nothing more, with the same placeholders. */
    @Test fun everyKeyIsInAllFourLocalesWithTheSamePlaceholders() {
        val en = strings("values")
        assertTrue(en.size > 50)
        for (locale in LOCALES) {
            val other = strings(locale)
            assertEquals("$locale: missing", emptySet<String>(), en.keys - other.keys)
            assertEquals("$locale: not in values/", emptySet<String>(), other.keys - en.keys)
            for ((key, text) in en) {
                assertEquals("$locale/$key placeholders", placeholders(text), placeholders(other.getValue(key)))
            }
        }
    }

    /** Two or more placeholders are numbered, so a translation may reorder them. */
    @Test fun severalPlaceholdersAreNumbered() {
        for ((key, text) in strings("values")) {
            val all = Regex("%[^%]").findAll(text).count()
            if (all > 1) assertEquals(key, all, Regex("%\\d+\\$[sd]").findAll(text).count())
        }
    }

    /** The Traditional Chinese texts are written in Traditional characters (the iOS zh-Hant entries are not). */
    @Test fun traditionalChineseUsesNoSimplifiedCharacters() {
        for ((key, text) in strings("values-zh-rTW")) {
            assertEquals("$key: $text", "", text.filter { it in SIMPLIFIED_ONLY })
        }
    }

    /** The guard's list holds no character that is also written in Traditional Chinese (it would forbid good text). */
    @Test fun theSimplifiedListIsOnlySimplified() {
        for (c in "后种置存机歌曲听助用池号台行接牙示屏幕播放停退首目道下除清空化窗广术无挂极适复") {
            assertTrue("$c is also a Traditional character", c !in SIMPLIFIED_ONLY)
        }
        assertTrue(SIMPLIFIED_ONLY.length > 150)
    }

    /** The product's name is Melarka wherever the app names itself (never the code name). */
    @Test fun theProductIsCalledMelarka() {
        for (locale in listOf("values") + LOCALES) {
            for ((key, text) in strings(locale)) assertTrue("$locale/$key", !Regex("\\b[Ll]ark\\b").containsMatchIn(text))
        }
        assertEquals("Melarka", s(R.string.app_name))
    }

    /** Android 13's per-app language setting offers exactly the four languages the app has. */
    @Test fun thePerAppLanguageListIsTheFourLocales() {
        val doc = DocumentBuilderFactory.newInstance().apply { isNamespaceAware = true }.newDocumentBuilder()
            .parse(File("src/main/res/xml/locales_config.xml"))
        val nodes = doc.getElementsByTagName("locale")
        val names = (0 until nodes.length).map { (nodes.item(it) as Element).getAttributeNS("http://schemas.android.com/apk/res/android", "name") }
        assertEquals(listOf("en", "zh-CN", "zh-TW", "sv"), names)
    }

    // MARK: What the running app shows (iOS testKeyStringsResolveInEnglishAndChinese)

    private val keyStrings = listOf(
        Row(R.string.settings_title, "Melarka Settings", "Melarka 设置", "Melarka 設定", "Melarka-inställningar"),
        Row(R.string.change_server, "Change Server", "更换服务器", "更換伺服器", "Byt server"),
        Row(R.string.offline_cache, "Offline Cache", "离线缓存", "離線快取", "Offlinecache"),
        Row(R.string.cant_connect_title, "Can't Connect to the Server", "无法连接服务器", "無法連線到伺服器", "Kan inte ansluta till servern"),
        Row(R.string.play_offline_favorites, "Play Offline Favorites", "播放离线收藏", "播放離線收藏", "Spela offlinefavoriter"),
        Row(R.string.server_address, "Server Address", "服务器地址", "伺服器位址", "Serveradress"),
        Row(R.string.ok, "OK", "好", "好", "OK"),
        Row(R.string.shortcut_shuffle_favorites, "Shuffle Favorites", "随机播放收藏", "隨機播放收藏", "Blanda favoriter"),
        Row(R.string.shortcut_resume, "Resume", "继续播放", "繼續播放", "Fortsätt spela"),
        Row(R.string.engine_offline_notice, "Offline: playing cached favorites", "离线：正在播放已缓存的收藏", "離線：正在播放已快取的收藏",
            "Offline: spelar cachade favoriter"),
        // iOS OfflinePlayerAppTests.testTheOfflineStringsAreTranslated
        Row(R.string.offline_mode, "Offline Mode", "离线模式", "離線模式", "Offlineläge"),
        Row(R.string.retry_connection, "Retry Connection", "重试连接", "重新連線", "Försök ansluta igen"),
        Row(R.string.up_next, "Up Next", "播放队列", "播放佇列", "Näst på tur"),
        Row(R.string.offline_favorites, "Offline Favorites", "离线收藏", "離線收藏", "Offlinefavoriter"),
        Row(R.string.media_shuffle, "Shuffle", "随机播放", "隨機播放", "Blanda"),
        Row(R.string.media_repeat_one, "Repeat One", "单曲循环", "單曲循環", "Upprepa en"),
        Row(R.string.previous, "Previous", "上一首", "上一首", "Föregående"),
        Row(R.string.next, "Next", "下一首", "下一首", "Nästa"),
        Row(R.string.battery_title, "Background Playback", "后台播放", "背景播放", "Uppspelning i bakgrunden"),
    )

    data class Row(val id: Int, val en: String, val zhCN: String, val zhTW: String, val sv: String)

    @Test @Config(qualifiers = "en") fun keyStringsInEnglish() { for (r in keyStrings) assertEquals(r.en, s(r.id)) }
    @Test @Config(qualifiers = "zh-rCN") fun keyStringsInSimplifiedChinese() { for (r in keyStrings) assertEquals(r.zhCN, s(r.id)) }
    @Test @Config(qualifiers = "zh-rTW") fun keyStringsInTraditionalChinese() { for (r in keyStrings) assertEquals(r.zhTW, s(r.id)) }
    @Test @Config(qualifiers = "sv") fun keyStringsInSwedish() { for (r in keyStrings) assertEquals(r.sv, s(r.id)) }

    /** iOS SettingsModelTests.testUsageTextPerLanguage: the texts SettingsModel builds. */
    @Test @Config(qualifiers = "zh-rCN") fun usageTextInSimplifiedChinese() {
        assertEquals("已用 1 MB / 上限 2 GB", s(R.string.cache_used, "1 MB", "2 GB"))
        assertEquals("未登录 · 上限 2 GB", s(R.string.cache_not_signed_in, "2 GB"))
        assertEquals("缓存已清空", s(R.string.cache_cleared))
        assertEquals("正在同步收藏（仅在 Wi‑Fi 下下载）", s(R.string.syncing))
    }

    @Test @Config(qualifiers = "en") fun usageTextInEnglish() {
        assertEquals("Used 1 MB / limit 2 GB", s(R.string.cache_used, "1 MB", "2 GB"))
        assertEquals("Not signed in · limit 2 GB", s(R.string.cache_not_signed_in, "2 GB"))
    }

    // MARK: Which language a phone gets (iOS testThePhonesLanguageChoosesTheLocalization)

    @Test @Config(qualifiers = "zh-rCN") fun mainlandChinaGetsSimplified() = assertEquals("离线模式", s(R.string.offline_mode))
    @Test @Config(qualifiers = "b+zh+Hans+SE") fun simplifiedChineseInSwedenGetsSimplified() = assertEquals("离线模式", s(R.string.offline_mode))
    @Test @Config(qualifiers = "zh-rTW") fun taiwanGetsTraditional() = assertEquals("離線模式", s(R.string.offline_mode))
    @Test @Config(qualifiers = "b+zh+Hant+HK") fun hongKongGetsTraditional() = assertEquals("離線模式", s(R.string.offline_mode))
    @Test @Config(qualifiers = "sv-rSE") fun swedenGetsSwedish() = assertEquals("Offlineläge", s(R.string.offline_mode))
    @Test @Config(qualifiers = "sv-rFI") fun swedishInFinlandGetsSwedish() = assertEquals("Offlineläge", s(R.string.offline_mode))
    @Test @Config(qualifiers = "en-rSE") fun englishInSwedenGetsEnglish() = assertEquals("Offline Mode", s(R.string.offline_mode))
    @Test @Config(qualifiers = "de-rDE") fun otherLanguagesGetEnglish() = assertEquals("Offline Mode", s(R.string.offline_mode))

    /** The running app shows its own language's text, never the key (iOS testTheRunningLanguageResolves). */
    @Test @Config(qualifiers = "sv") fun theRunningLanguageResolves() {
        assertEquals(s(R.string.engine_cannot_play), EngineTexts.from(context).cannotPlay)
        assertEquals("Kan inte spela upp", EngineTexts.from(context).cannotPlay)
        assertEquals(Locale("sv").language, context.resources.configuration.locales[0].language)
    }

    companion object {
        val LOCALES = listOf("values-zh-rCN", "values-zh-rTW", "values-sv")

        /**
         * Characters written only in Simplified Chinese: each has another Traditional form, and none is itself a character
         * of Traditional text (后 皇后, 种 a surname, 机 a tree, 台 台灣 and the like are left out on purpose).
         */
        val SIMPLIFIED_ONLY: String = (
            "线说该这们来时会个为与发开关门问题点务网络载设请获离缓单随环选择认证录码错误帮应电运连车蓝显锁暂继续进页资讯节频订阅" +
            "删储级还没确对钮读写间长现话语识让给经过头东动华国学实业产当气乐观欢见觉记讲访评诉译试询详谁调谢输达远递邮释钟银链" +
            "键闭闲闻队阶际陆险隐难顶项顺须预领颜风飞饭马驱验鱼鸟齐龙帐币岁归态总拥换损摄数断晓杂权条样标档桥梦检毕汇浏测济灵热" +
            "爱状独画畅盖础积称稳笔简类纪组细终结绝绍统维编缩联职脑补装视览触计讨许论词课谈财货质购费赛软轻较辑边迟遗铁闪阳饮鸣齿"
            ).toSet().joinToString("")

        /** `res/<dir>/strings.xml`: translatable name → text (the test runs in the module's directory). */
        fun strings(dir: String): Map<String, String> {
            val doc = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(File("src/main/res/$dir/strings.xml"))
            val nodes = doc.getElementsByTagName("string")
            return (0 until nodes.length).map { nodes.item(it) as Element }
                .filter { it.getAttribute("translatable") != "false" }
                .associate { it.getAttribute("name") to it.textContent }
        }

        /** The placeholders of a text, in order (`%1$s`, `%d`, …); `%%` is not one. */
        fun placeholders(text: String): List<String> = Regex("%(\\d+\\$)?[sd]").findAll(text).map { it.value }.toList()
    }
}
