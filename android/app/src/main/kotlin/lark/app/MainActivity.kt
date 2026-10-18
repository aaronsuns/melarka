package lark.app

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.os.Bundle
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.widget.FrameLayout
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import lark.bridge.WebShell
import lark.car.CarAutoplay

/**
 * The one Activity: it only shows the app services. The server's web app fills it ([WebShell]); the native screens
 * (server, can't connect, offline player, settings) come in Task 12. Starting and stopping is the services' scene phase
 * (events to the page, the cache index, a resume held back in the background). The web view lives as long as the
 * Activity, which is not recreated on rotation or resizing (`configChanges`); playback never depends on either.
 */
class MainActivity : Activity(), WebShell.Host {
    private val services: AppServices get() = (application as LarkApplication).services
    private lateinit var root: FrameLayout
    private var shell: WebShell? = null
    private var fullscreen: Pair<View, WebChromeClient.CustomViewCallback>? = null
    /** The page's open `alert`/`confirm` dialogs. */
    private val dialogs = LinkedHashMap<AlertDialog, () -> Unit>()
    private val render: () -> Unit = { render() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        root = FrameLayout(this)
        // Edge to edge (Android 15): the page sits inside the system bars, the cutout and the keyboard.
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val b = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout() or WindowInsetsCompat.Type.ime())
            v.setPadding(b.left, b.top, b.right, b.bottom)
            WindowInsetsCompat.CONSUMED
        }
        setContentView(root)
        services.observe(render)
        render()
    }

    override fun onStart() {
        super.onStart()
        services.scenePhaseChanged(ScenePhase.active)
    }

    override fun onStop() {
        services.scenePhaseChanged(ScenePhase.background)
        super.onStop()
    }

    override fun onDestroy() {
        // Answered (false) now, as the dismiss listener runs only later: the page's alert()/confirm() never waits forever.
        dialogs.toList().forEach { (dialog, cancel) -> cancel(); dialog.dismiss() }
        dialogs.clear()
        services.removeObserver(render)
        dropShell(clearData = false)
        super.onDestroy()
    }

    /** The web view for the current server: made when there is one, dropped (with its data) when the server changes. */
    private fun render() {
        val server = services.serverUrl
        if (shell != null && shell?.server != server) dropShell(clearData = true)
        if (server == null || shell != null) return
        val s = WebShell(this, server, services.bridge(server), this)
        shell = s
        root.addView(s.view, 0, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        services.page = PageControl { s.load(it) }
        if (!services.stayOffline) s.load(server)       // Stay Offline: not loaded until it is turned off
    }

    private fun dropShell(clearData: Boolean) {
        val s = shell ?: return
        shell = null
        services.page = null
        s.destroy(clearData)
    }

    // MARK: - WebShell.Host

    override fun loadFailed(errorCode: Int, description: String?) = services.pageDidFail(errorCode, description)

    override fun httpFailed(status: Int) = services.pageDidFail(status)

    override fun loaded(url: String) {
        services.pageDidLoad()
        services.pageCookiesChanged()
    }

    override fun openExternally(url: String) {
        ExternalLink.open(url) { startActivity(it) }
    }

    /** A new web view and the server's page again (iOS reloads; Android needs a new web view after a renderer crash). */
    override fun rendererGone() {
        dropShell(clearData = false)
        render()
    }

    override fun dialog(message: String, cancellable: Boolean, done: (Boolean) -> Unit) {
        if (isFinishing || isDestroyed) return done(false)
        var answered = false
        fun answer(ok: Boolean) { if (!answered) { answered = true; done(ok) } }
        lateinit var shown: AlertDialog
        shown = AlertDialog.Builder(this)
            .setMessage(message)
            .setPositiveButton(android.R.string.ok) { _, _ -> answer(true) }
            .apply { if (cancellable) setNegativeButton(android.R.string.cancel) { _, _ -> answer(false) } }
            .setOnDismissListener { dialogs.remove(shown); answer(false) }
            .show()
        dialogs[shown] = { answer(false) }
    }

    override fun showFullscreen(view: View, callback: WebChromeClient.CustomViewCallback) {
        hideFullscreen()
        fullscreen = view to callback
        root.addView(view, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
    }

    override fun hideFullscreen() {
        val (view, callback) = fullscreen ?: return
        fullscreen = null
        root.removeView(view)
        callback.onCustomViewHidden()
    }

    /** Back leaves full screen, then goes back in the page, then leaves the app (playback goes on). */
    /** The system's car picker answered (car autoplay, from Settings). */
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (CarAutoplay.onActivityResult(this, requestCode, resultCode, data)) return
        super.onActivityResult(requestCode, resultCode, data)
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        if (keyCode == KeyEvent.KEYCODE_BACK) {
            if (fullscreen != null) { hideFullscreen(); return true }
            if (shell?.goBack() == true) return true
        }
        return super.onKeyDown(keyCode, event)
    }
}
