package lark.app

import android.os.Build
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import lark.Instrumented
import lark.Instrumented.onMain
import lark.Instrumented.waitFor
import org.junit.Assert.assertFalse
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Back with nothing to close (the server screen, no page history), Android 12+: the system's Back moves the task back
 * and the Activity is only stopped, not finished, so coming back is a warm start (and playback is untouched either way).
 */
@RunWith(AndroidJUnit4::class)
class BackTest {
    @Test fun backWithNothingToCloseMovesTheTaskBack() {
        assumeTrue("Android 12+ behaviour", Build.VERSION.SDK_INT >= 31)
        val ctx = Instrumented.context
        val device = UiDevice.getInstance(Instrumented.instrumentation)
        // Started from the launcher's icon: Android moves the task back (instead of finishing) only for a task the home
        // screen started.
        openFromLauncher(device)
        checkNotNull(device.wait(Until.findObject(By.text(ctx.getString(R.string.server_connect))), 15_000)) { "the server screen" }
        val activity = onMain { ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED).single() as MainActivity }

        device.pressBack()

        waitFor("the activity is stopped") { onMain { ActivityLifecycleMonitorRegistry.getInstance().getLifecycleStageOf(activity) } == Stage.STOPPED }
        assertFalse("not finished", onMain { activity.isFinishing })
        // From the launcher again: the same Activity comes back.
        openFromLauncher(device)
        waitFor("the same activity resumes") { onMain { ActivityLifecycleMonitorRegistry.getInstance().getLifecycleStageOf(activity) } == Stage.RESUMED }
        onMain { activity.finish() }
    }

    /** Home, all apps, Melarka's icon. */
    private fun openFromLauncher(device: UiDevice) {
        device.pressHome()
        device.waitForIdle()
        val name = Instrumented.context.getString(R.string.app_name)
        if (!device.hasObject(By.text(name))) {
            device.swipe(device.displayWidth / 2, device.displayHeight * 9 / 10, device.displayWidth / 2, device.displayHeight / 5, 20)
        }
        checkNotNull(device.wait(Until.findObject(By.text(name)), 10_000)) { "Melarka in the launcher" }.click()
    }
}
