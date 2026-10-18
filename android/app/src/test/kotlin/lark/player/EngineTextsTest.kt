package lark.player

import androidx.test.core.app.ApplicationProvider
import android.content.Context
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** The engine's notices come from the string resources, in each of the four locales; English matches [EngineTexts.english]. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [30])
class EngineTextsTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private fun all(t: EngineTexts) = listOf(t.offlineNotice, t.noFavoritesNotice, t.cannotPlay, t.stallNotice, t.offlineStopped,
        t.episodeFallbackNotice, t.nothingCachedNotice, t.shuffleFailedNotice)

    @Test @Config(qualifiers = "en") fun englishIsTheDefault() {
        assertEquals(all(EngineTexts.english), all(EngineTexts.from(context)))
    }

    @Test @Config(qualifiers = "zh-rCN") fun simplifiedChinese() {
        assertEquals("离线：正在播放已缓存的收藏", EngineTexts.from(context).offlineNotice)
        assertEquals("无法播放", EngineTexts.from(context).cannotPlay)
    }

    @Test @Config(qualifiers = "zh-rTW") fun traditionalChineseHasItsOwnTexts() {
        assertEquals("無法取得收藏，請稍後再試", EngineTexts.from(context).shuffleFailedNotice)
        assertEquals("離線：正在播放已快取的收藏", EngineTexts.from(context).offlineNotice)
    }

    /** Every string of the Traditional Chinese locale is written in Traditional characters (the iOS zh-Hant entries are not). */
    @Test @Config(qualifiers = "zh-rTW") fun traditionalChineseUsesNoSimplifiedCharacters() {
        val simplified = lark.ui.LocalizationTest.SIMPLIFIED_ONLY
        val texts = lark.app.R.string::class.java.fields.map { it.name to context.getString(it.getInt(null)) }
        for ((name, text) in texts) {
            val bad = text.filter { it in simplified }
            assertEquals("$name: $text", "", bad)
        }
    }

    @Test @Config(qualifiers = "sv") fun swedish() {
        assertEquals("Kan inte spela upp", EngineTexts.from(context).cannotPlay)
        assertEquals("Offline: spelar cachade favoriter", EngineTexts.from(context).offlineNotice)
    }
}
