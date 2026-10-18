package lark.app

import android.view.View
import android.view.ViewGroup
import androidx.compose.ui.platform.ComposeView
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Activity shows the native screens over the page: with no server, the server screen (not an empty Activity); over a
 * failed page, the can't-connect screen (not the browser's error page); and nothing in the page's way otherwise.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MainActivityScreensTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    private val services: AppServices get() = (compose.activity.application as LarkApplication).services

    private fun nativeView(): ComposeView {
        fun find(v: View): ComposeView? = when (v) {
            is ComposeView -> v
            is ViewGroup -> (0 until v.childCount).firstNotNullOfOrNull { find(v.getChildAt(it)) }
            else -> null
        }
        return find(compose.activity.window.decorView)!!
    }

    @Test fun withNoServerTheServerScreenShows() {
        compose.onNodeWithText("Server Address").assertIsDisplayed()
        assertEquals(View.VISIBLE, nativeView().visibility)
    }

    @Test fun theSettingsOpenOverTheServerScreenAndBackClosesThem() {
        compose.runOnUiThread { services.openSettings() }
        compose.onNodeWithText("Melarka Settings").assertIsDisplayed()
        compose.onNodeWithText("Not set").assertIsDisplayed()
        compose.runOnUiThread { compose.activity.onKeyDown(android.view.KeyEvent.KEYCODE_BACK, null) }
        compose.waitForIdle()
        assertEquals(false, services.showSettings)
    }
}
