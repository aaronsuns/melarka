package lark

import android.content.ComponentName
import android.content.Context
import android.graphics.Bitmap
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import android.util.Log
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import androidx.test.espresso.web.model.Atoms
import androidx.test.espresso.web.sugar.Web.onWebView
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.services.storage.TestStorage
import lark.app.AppServices
import lark.app.LarkApplication
import lark.service.PlaybackService
import java.util.concurrent.TimeUnit

/** What the instrumented tests share: the app, the main thread, condition waits, shell commands. */
object Instrumented {
    val instrumentation get() = InstrumentationRegistry.getInstrumentation()

    /** The app under test (the tests run in its process). */
    val app: LarkApplication get() = instrumentation.targetContext.applicationContext as LarkApplication
    val context: Context get() = instrumentation.targetContext

    /** An instrumentation argument (`-Pandroid.testInstrumentationRunnerArguments.<name>=…`). */
    fun argument(name: String): String? = InstrumentationRegistry.getArguments().getString(name)

    /** Runs [fn] on the main thread and returns its value (the app services and the player live there). */
    fun <T> onMain(fn: () -> T): T {
        if (Looper.myLooper() == Looper.getMainLooper()) return fn()
        var result: Result<T>? = null
        instrumentation.runOnMainSync { result = runCatching(fn) }
        return result!!.getOrThrow()
    }

    fun services(): AppServices = onMain { app.services }

    /**
     * Waits until [condition] holds (checked every [everyMs]), or fails with [what] after [timeoutMs]. A condition,
     * not a fixed sleep: it returns as soon as the app gets there.
     */
    fun waitFor(what: String, timeoutMs: Long = 20_000, everyMs: Long = 100, condition: () -> Boolean) {
        val end = SystemClock.uptimeMillis() + timeoutMs
        var last: Throwable? = null
        while (SystemClock.uptimeMillis() < end) {
            try {
                if (condition()) return
                last = null
            } catch (e: Throwable) {
                last = e
            }
            SystemClock.sleep(everyMs)
        }
        throw AssertionError("timed out after $timeoutMs ms waiting for: $what", last)
    }

    /** [condition] on the main thread. */
    fun waitOnMain(what: String, timeoutMs: Long = 20_000, condition: () -> Boolean) =
        waitFor(what, timeoutMs) { onMain(condition) }

    /** A shell command as the shell user (`svc wifi disable`, `input keyevent`); returns its output. */
    fun shell(command: String): String {
        val pfd = instrumentation.uiAutomation.executeShellCommand(command)
        return ParcelFileDescriptor.AutoCloseInputStream(pfd).use { it.readBytes().toString(Charsets.UTF_8) }
    }

    /** Wi-Fi and mobile data off (true) or on. */
    fun setNetwork(on: Boolean) {
        val verb = if (on) "enable" else "disable"
        shell("svc wifi $verb")
        shell("svc data $verb")
    }

    /**
     * After a failure: a screenshot and the page's text in the test outputs (`/sdcard/googletest/test_outputfiles`, kept
     * by the test services when the app's data is wiped; emulator-smoke.sh copies them out), so a CI run shows what
     * was on screen.
     */
    fun recordFailure(name: String) {
        val storage = TestStorage()
        try {
            val shot = instrumentation.uiAutomation.takeScreenshot()
            storage.openOutputFile("$name.png").use { shot.compress(Bitmap.CompressFormat.PNG, 100, it) }
            val text = onWebView().perform(Atoms.script("return document.body ? document.body.innerText : '';", Atoms.castOrDie(String::class.java))).get()
            storage.openOutputFile("$name-page.txt").use { it.write(text.toByteArray()) }
        } catch (e: Throwable) {
            Log.w("Instrumented", "could not record the failure: ${e.javaClass.simpleName}")
        }
    }

    /** [text] as a file in the test outputs (kept and copied out as [recordFailure]'s are). */
    fun recordText(fileName: String, text: String) {
        try {
            TestStorage().openOutputFile(fileName).use { it.write(text.toByteArray()) }
        } catch (e: Throwable) {
            Log.w("Instrumented", "could not record $fileName: ${e.javaClass.simpleName}")
        }
    }

    /** A controller of the media service, as a car or the lock screen sees it. Use it on the main thread ([onMain]). */
    @UnstableApi
    fun mediaController(): MediaController {
        val token = SessionToken(context, ComponentName(context, PlaybackService::class.java))
        val future = MediaController.Builder(context, token).setApplicationLooper(Looper.getMainLooper()).buildAsync()
        return future.get(20, TimeUnit.SECONDS)
    }
}
