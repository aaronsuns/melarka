package lark.car

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import lark.app.LarkApplication
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * The automation entry point (Tasker, Samsung Modes & Routines, …): an exported activity with no permission that takes
 * exactly two public actions, ignores extras and data, and runs them as the launcher shortcuts do. Anything else does
 * nothing.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class AutomationActivityTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val shuffle = "io.github.aaronsuns.melarka.action.SHUFFLE_FAVORITES"
    private val resume = "io.github.aaronsuns.melarka.action.RESUME"

    @Test fun thePublicActionsAreTheShortcutsAndNothingElse() {
        assertEquals(shuffle, AutomationActivity.ACTION_SHUFFLE_FAVORITES)
        assertEquals(resume, AutomationActivity.ACTION_RESUME)
        assertEquals(ShortcutActivity.ACTION_SHUFFLE_FAVORITES, AutomationActivity.shortcutAction(Intent(shuffle)))
        assertEquals(ShortcutActivity.ACTION_RESUME, AutomationActivity.shortcutAction(Intent(resume)))
        // Extras and data change nothing.
        assertEquals(ShortcutActivity.ACTION_RESUME, AutomationActivity.shortcutAction(Intent(resume, Uri.parse("https://x.test/"))
            .putExtra("server", "https://evil.test/").putExtra("token", "x")))
        // Anything else is nothing, the internal shortcut actions too (they are for the launcher's shortcuts only).
        for (a in listOf(null, "", ShortcutActivity.ACTION_SHUFFLE_FAVORITES, ShortcutActivity.ACTION_RESUME, Intent.ACTION_MAIN,
            "io.github.aaronsuns.melarka.action.SIGN_OUT", "$shuffle "))
            assertNull("$a", AutomationActivity.shortcutAction(Intent().also { i -> a?.let { i.action = it } }))
    }

    @Suppress("DEPRECATION")      // Robolectric has no flags-object variants of these
    @Test fun theManifestExportsItWithNoPermissionForTheTwoActions() {
        val pm = context.packageManager
        val info = pm.getActivityInfo(ComponentName(context, AutomationActivity::class.java), 0)
        assertTrue(info.exported)
        assertNull(info.permission)
        for (a in listOf(shuffle, resume)) {
            val found = pm.queryIntentActivities(Intent(a).setPackage(context.packageName), 0)
            assertEquals(a, listOf(AutomationActivity::class.java.name), found.map { it.activityInfo.name })
        }
        val shortcut = pm.getActivityInfo(ComponentName(context, ShortcutActivity::class.java), 0)
        assertFalse("the launcher shortcuts' activity stays private", shortcut.exported)
    }

    /** An unknown action finishes at once and builds nothing (no services, no media service). */
    @Test fun anUnknownActionDoesNothing() {
        val app = context as LarkApplication
        val activity = Robolectric.buildActivity(AutomationActivity::class.java,
            Intent("io.github.aaronsuns.melarka.action.SIGN_OUT").setClassName(context, AutomationActivity::class.java.name)).create().get()
        assertTrue(activity.isFinishing)
        assertFalse(app.servicesBuilt)
        assertNull(shadowOf(app).nextStartedService)
    }
}
