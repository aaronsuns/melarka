package lark

import android.net.ConnectivityManager
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import androidx.lifecycle.Lifecycle
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaController
import androidx.test.core.app.ActivityScenario
import androidx.test.espresso.web.model.Atoms
import androidx.test.espresso.web.sugar.Web.onWebView
import androidx.test.espresso.web.webdriver.DriverAtoms.findElement
import androidx.test.espresso.web.webdriver.DriverAtoms.webClick
import androidx.test.espresso.web.webdriver.Locator
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import lark.Instrumented.onMain
import lark.Instrumented.services
import lark.Instrumented.waitFor
import lark.Instrumented.waitOnMain
import lark.app.MainActivity
import lark.app.R
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The whole app on an emulator against a real server (android/scripts/emulator-smoke.sh: the demo library at
 * `larkServer`, signed in as `admin` with the run's `larkPassword`): the server screen, sign-in in the page, a
 * favorite played from the page, the page's bridge (`window.larkNative` frozen, its `post` reaching native), the
 * media session as a car or the lock screen sees it (playing on through the page's renderer dying and with the app in
 * the background, pause, next), and with the network gone the cached favorites playing on.
 */
@UnstableApi
@RunWith(AndroidJUnit4::class)
class SmokeFlowTest {
    private val server = DemoServer()
    private val device = UiDevice.getInstance(Instrumented.instrumentation)
    private var scenario: ActivityScenario<MainActivity>? = null
    private var controller: MediaController? = null

    @Before fun setUp() = Instrumented.setNetwork(true)

    @After fun tearDown() {
        Instrumented.setNetwork(true)
        controller?.let { c -> onMain { c.release() } }
        scenario?.close()
    }

    /** A failure leaves a screenshot and the page's text, taken there and then (emulator-smoke.sh copies them out). */
    @Test fun signInPlayAFavoriteControlItAndPlayOnOffline() = try {
        flow()
    } catch (e: Throwable) {
        Instrumented.recordFailure("SmokeFlowTest")
        throw e
    }

    private fun flow() {
        val favorites = server.seedFavorites()

        // The server screen: the emulator's host loopback (plain http is allowed there in debug builds).
        scenario = ActivityScenario.launch(MainActivity::class.java)
        val field = checkNotNull(device.wait(Until.findObject(By.clazz("android.widget.EditText")), 15_000)) { "the server field" }
        field.text = server.url
        device.findObject(By.text(Instrumented.context.getString(R.string.server_connect))).click()
        waitOnMain("the server is kept") { services().serverUrl == server.url + "/" }

        // Sign-in in the page; native picks the session up (the cookie harvest after the page's auth message).
        eventually("the sign-in form", 30_000) { onWebView().withElement(findElement(Locator.CSS_SELECTOR, "input[autocomplete=username]")) }
        fill("input[autocomplete=username]", "admin")
        fill("input[autocomplete=current-password]", server.password)
        onWebView().withElement(findElement(Locator.CSS_SELECTOR, "button[type=submit]")).perform(webClick())
        waitOnMain("native has the token", 30_000) { services().auth.token != null }

        checkBridge()

        // A favorite, played from the page (Library → Favorites, the first song).
        eventually("the library tab") { onWebView().withElement(findElement(Locator.CSS_SELECTOR, "nav.tabs a[href='/library']")) }
            .perform(webClick())
        eventually("a favorite in the list", 30_000) { onWebView().withElement(findElement(Locator.CSS_SELECTOR, "ul.tracks button.track-main")) }
            .perform(webClick())
        waitOnMain("the engine plays", 30_000) { services().engine?.playing == true && services().engine?.current != null }

        val c = Instrumented.mediaController().also { controller = it }
        waitOnMain("the session says playing") { c.isPlaying }
        val title = onMain { c.mediaMetadata.title?.toString() }
        assertTrue("the session shows a favorite: $title of $favorites", title in favorites.values)

        // The page's renderer dies: the music plays on, and a new web view loads the page again (signed in: the cookie).
        val crashed = checkNotNull(webView()) { "the page's web view" }
        onMain { crashed.loadUrl("chrome://kill") }
        waitFor("a new web view with the page", 30_000) { webView().let { it != null && it !== crashed } }
        eventually("the page again, signed in", 30_000) { onWebView().withElement(findElement(Locator.CSS_SELECTOR, "nav.tabs a[href='/library']")) }
        assertTrue("playing through the renderer's death", onMain { c.isPlaying && services().engine?.playing == true })

        // The app in the background: the service plays on.
        device.pressHome()
        waitFor("the activity is stopped") { scenario!!.state == Lifecycle.State.CREATED }
        val before = onMain { c.currentPosition }
        SystemClock.sleep(10_000)          // the requirement itself: still playing 10 s later
        assertTrue("still playing in the background", onMain { c.isPlaying })
        val after = onMain { c.currentPosition }
        assertTrue("the position moved on in the background: $before → $after ms", after - before >= 8_000)

        // Pause, play and next, as a car or the lock screen sends them.
        onMain { c.pause() }
        waitOnMain("paused through the session") { !c.isPlaying && services().engine?.playing == false }
        onMain { c.play() }
        waitOnMain("playing again through the session") { c.isPlaying && services().engine?.playing == true }
        val first = onMain { c.currentMediaItem?.mediaId }
        onMain { c.seekToNext() }
        waitOnMain("the next song through the session", 30_000) { c.currentMediaItem?.mediaId != first && c.isPlaying }
        assertTrue("the next song is a favorite too", onMain { c.mediaMetadata.title?.toString() } in favorites.values)

        // Offline: the favorites the sync cached play on.
        waitOnMain("every favorite is cached", 120_000) { services().cache.cachedFavorites().size == favorites.size }
        Instrumented.setNetwork(false)
        waitFor("the phone is offline", 30_000) {
            Instrumented.context.getSystemService(ConnectivityManager::class.java).activeNetwork == null
        }
        assertTrue("the server must be out of reach offline (so what plays now comes from the phone)", !server.reachable())
        val online = onMain { c.currentMediaItem?.mediaId }
        onMain { c.seekToNext() }
        waitOnMain("another song offline", 30_000) { c.currentMediaItem?.mediaId != online && c.isPlaying }
        val id = onMain { services().engine?.current?.id }
        assertNotNull("the engine has a current song", id)
        assertNotNull("it plays from the cache", onMain { services().cache.localFile(id!!.toLong()) })
        val start = onMain { c.currentPosition }
        waitOnMain("it plays on offline", 15_000) { c.isPlaying && c.currentPosition - start >= 3_000 }
    }

    /**
     * `window.larkNative` in the real page: frozen and not replaceable (a page script can't take over `post`), and
     * `post` reaches native (openSettings opens the native settings, closed again here).
     */
    private fun checkBridge() {
        val frozen: String = onWebView().perform(Atoms.script("""
            var n = window.larkNative;
            try { window.larkNative = { post: function () {} }; } catch (e) {}
            try { n.post = function () {}; } catch (e) {}
            try { delete window.larkNative; } catch (e) {}
            return [Object.isFrozen(n), window.larkNative === n, n.platform, n.version, String(n.post).indexOf("postMessage") >= 0].join(",");
        """.trimIndent(), Atoms.castOrDie(String::class.java))).get()
        assertEquals("true,true,android,1,true", frozen)
        onWebView().perform(Atoms.script("window.larkNative.post({ type: \"openSettings\" }); return \"sent\";", Atoms.castOrDie(String::class.java)))
        waitOnMain("the page's post reached native") { services().showSettings }
        onMain { services().closeSettings() }
    }

    /**
     * Types [value] into the page's field as the browser's autofill does: the input's own value setter and an `input`
     * event, which React's controlled fields take (WebDriver's synthetic keys did not reach React's state here).
     */
    private fun fill(selector: String, value: String) {
        val js = "var el = document.querySelector(" + JSONObject.quote(selector) + ");" +
            "Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(el, " + JSONObject.quote(value) + ");" +
            "el.dispatchEvent(new Event('input', { bubbles: true })); return el.value.length;"
        assertEquals("the field took the value", value.length, onWebView().perform(Atoms.script(js, Atoms.castOrDie(Int::class.javaObjectType))).get())
    }

    /** The page's web view (the Activity's only one), or null. */
    private fun webView(): WebView? {
        var found: WebView? = null
        scenario!!.onActivity { found = find(it.window.decorView) }
        return found
    }

    private fun find(v: View): WebView? = when (v) {
        is WebView -> v
        is ViewGroup -> (0 until v.childCount).firstNotNullOfOrNull { find(v.getChildAt(it)) }
        else -> null
    }

    /** Retries [fn] (an element that is not there yet throws) until it works. */
    private fun <T> eventually(what: String, timeoutMs: Long = 20_000, fn: () -> T): T {
        var result: T? = null
        waitFor(what, timeoutMs, everyMs = 250) { result = fn(); true }
        @Suppress("UNCHECKED_CAST")
        return result as T
    }
}
