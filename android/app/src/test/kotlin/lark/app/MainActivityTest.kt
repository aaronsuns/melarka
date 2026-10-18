package lark.app

import android.app.AlertDialog
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.shadows.ShadowDialog

/** The Activity's own UI around the page (no server set: no web view). */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MainActivityTest {
    /** A page's `confirm()` open when the Activity goes is dismissed and answered (false), never leaked. */
    @Test fun anOpenDialogIsDismissedWithTheActivity() {
        val controller = Robolectric.buildActivity(MainActivity::class.java).setup()
        val answers = mutableListOf<Boolean>()
        controller.get().dialog("Unfollow?", cancellable = true) { answers += it }
        val dialog = ShadowDialog.getLatestDialog() as AlertDialog
        assertTrue(dialog.isShowing)
        controller.pause().stop().destroy()
        assertFalse(dialog.isShowing)
        assertEquals(listOf(false), answers)
    }
}
