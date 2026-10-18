package lark.app

import android.app.AlertDialog
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Bundle
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.widget.FrameLayout
import androidx.activity.ComponentActivity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.ComposeView
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch
import lark.bridge.WebShell
import lark.car.CarAutoplay
import lark.ui.BatteryGuide
import lark.ui.CantConnectScreen
import lark.ui.LarkTheme
import lark.ui.OfflinePlayerModel
import lark.ui.OfflinePlayerScreen
import lark.ui.ServerScreen
import lark.ui.SettingsHost
import lark.ui.SettingsScreen

/**
 * The one Activity: it only shows the app services. The server's web app fills it ([WebShell]); the native screens
 * (Compose, `lark.ui`) cover it when they are up: the server screen with no server, the can't-connect screen over a page
 * that failed, the offline player, and Settings. Starting and stopping is the services' scene phase (events to the page,
 * the cache index, a resume held back in the background). The web view lives as long as the Activity, which is not
 * recreated on rotation or resizing (`configChanges`); playback never depends on either.
 */
class MainActivity : ComponentActivity(), WebShell.Host {
    private val services: AppServices get() = (application as LarkApplication).services
    private lateinit var root: FrameLayout
    private lateinit var native: ComposeView
    /** What the native screens show, from the services (they are not Compose state; [render] copies it here). */
    private var ui by mutableStateOf(NativeUi())
    private val settingsHost = SettingsHost(
        requestCarPermissions = { CarAutoplay.requestPermissions(this) },
        associate = { CarAutoplay.associate(this, it) },
        pairedCars = { CarAutoplay.pairedCars(this) },
        requestBatteryExemption = { BatteryGuide.request(this) },
        batteryExempt = { BatteryGuide.isExempt(this) },
    )
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
        native = ComposeView(this).apply { setContent { LarkTheme { NativeScreens() } } }
        root.addView(native, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        setContentView(root)
        services.observe(render)
        render()
    }

    override fun onStart() {
        super.onStart()
        services.scenePhaseChanged(ScenePhase.active)
    }

    override fun onResume() {
        super.onResume()
        settingsHost.refresh++         // back from the battery dialog or the system's settings
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

    /**
     * The web view for the current server: made when there is one, dropped (with its data) when the server changes. The
     * native screens are shown over it, and only while one is up (otherwise the page gets every touch).
     */
    private fun render() {
        val app = services
        val server = app.serverUrl
        val now = NativeUi(server, app.offlinePlayer as? OfflinePlayerModel, app.pageFailed, app.showSettings, app.stayOffline,
            app.changingServer, app.pageFailed && app.hasOfflineFavorites)
        ui = now
        native.visibility = if (now.shown) View.VISIBLE else View.GONE
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

    /**
     * The system's car picker answered (car autoplay, from Settings). `CarAutoplay.associate` starts it with the
     * platform's `startIntentSenderForResult`, so its answer comes here.
     */
    @Suppress("OVERRIDE_DEPRECATION", "DEPRECATION")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (CarAutoplay.onActivityResult(this, requestCode, resultCode, data)) { settingsHost.refresh++; return }
        super.onActivityResult(requestCode, resultCode, data)
    }

    /** The Nearby devices permission's answer (car autoplay, asked by `CarAutoplay.requestPermissions`). */
    @Suppress("OVERRIDE_DEPRECATION", "DEPRECATION")
    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<String>, grantResults: IntArray) {
        if (requestCode == CarAutoplay.REQUEST_PERMISSIONS) {
            settingsHost.permissionsAnswered(grantResults.isNotEmpty() && grantResults.all { it == PackageManager.PERMISSION_GRANTED })
            return
        }
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
    }

    /**
     * Back leaves full screen, then closes Settings, then the offline player (back to the can't-connect screen), then
     * goes back in the page, then leaves the app (playback goes on).
     */
    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        if (keyCode == KeyEvent.KEYCODE_BACK) {
            if (fullscreen != null) { hideFullscreen(); return true }
            if (services.showSettings) { if (!services.changingServer) services.closeSettings(); return true }
            if (services.offlinePlayer != null && services.serverUrl != null) { services.closeOfflinePlayer(); return true }
            if (!ui.shown && shell?.goBack() == true) return true
        }
        return super.onKeyDown(keyCode, event)
    }

    // MARK: - The native screens

    /** A copy of what the native screens show (see [render]). */
    private data class NativeUi(
        val server: String? = null,
        val offlinePlayer: OfflinePlayerModel? = null,
        val pageFailed: Boolean = false,
        val showSettings: Boolean = false,
        val stayOffline: Boolean = false,
        val changingServer: Boolean = false,
        val hasOfflineFavorites: Boolean = false,
    ) {
        /** A native screen covers the page. */
        val shown: Boolean get() = server == null || offlinePlayer != null || pageFailed || showSettings
    }

    @Composable
    private fun NativeScreens() {
        val u = ui
        val s = services
        val server = u.server
        when {
            server == null -> ServerScreen(onConnected = { url -> lifecycleScope.launch { s.setServerUrl(url) } })
            u.offlinePlayer != null -> OfflinePlayerScreen(u.offlinePlayer, server, u.stayOffline, { s.setStayOffline(it) },
                onClose = { s.closeOfflinePlayer() })
            u.pageFailed -> CantConnectScreen(server, u.stayOffline, u.hasOfflineFavorites,
                onRetry = { s.retryPage() }, onSettings = { s.openSettings() },
                onPlayOfflineFavorites = { lifecycleScope.launch { s.playOfflineFavorites() } },
                onStayOffline = { s.setStayOffline(it) })
        }
        if (u.showSettings) {
            SettingsScreen(s, CarAutoplay.of(this), settingsHost, u.stayOffline, u.changingServer, onDone = { s.closeSettings() })
        }
    }
}
