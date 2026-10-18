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
    /** A notice from the services (a shortcut's late failure) shows in a dialog, once; OK dismisses it for good. */
    @Test fun aNoticeShowsInADialogUntilDismissed() {
        val controller = Robolectric.buildActivity(MainActivity::class.java).setup()
        val services = (controller.get().application as LarkApplication).services
        services.showNotice("The server could not be reached")
        val dialog = ShadowDialog.getLatestDialog() as AlertDialog
        assertTrue(dialog.isShowing)
        assertEquals("The server could not be reached", org.robolectric.Shadows.shadowOf(dialog).message.toString())
        services.showNotice("The server could not be reached")      // the same notice again: still one dialog
        assertEquals(dialog, ShadowDialog.getLatestDialog())
        dialog.getButton(android.content.DialogInterface.BUTTON_POSITIVE).performClick()
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle()
        assertFalse(dialog.isShowing)
        assertEquals(null, services.notice)
        controller.pause().stop().destroy()
    }

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
