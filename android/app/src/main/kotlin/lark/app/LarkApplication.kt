package lark.app

import android.app.Application
import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import lark.auth.KeystoreSecretStore
import lark.auth.WebViewData
import lark.cache.CacheLayout
import lark.cache.FavoritesRefresh
import lark.cache.StorageManagerFreeSpace
import lark.car.CarAutoplay
import lark.car.CarAutoplaySettings
import lark.car.SystemCompanion
import lark.core.AndroidNetworkStatus
import lark.core.Clock
import lark.core.MainScheduler
import lark.core.loggingMainScope
import lark.player.EngineTexts
import lark.service.Playback
import lark.service.PlaybackService
import java.io.File

/**
 * The process: it sets the process-wide seams at once (the HTTP client the player streams with, the background sync's
 * provider, the playback host's hook) and builds the [services] on first use: the Activity, the media service's first
 * start (a Bluetooth play with no Activity), or the background work. Nothing heavy runs in `onCreate`, so a process
 * started for the background work alone builds only what that needs.
 */
class LarkApplication : Application() {
    private var built: AppServices? = null
    private var building = false

    /** The services exist (tests; nothing else needs to ask). */
    val servicesBuilt: Boolean get() = built != null

    /** The app services, built on first use (main thread). */
    val services: AppServices get() = built ?: build()

    /** Car autoplay: the associated car's appearance runs Shuffle Favorites on the services' engine (main thread). */
    @OptIn(UnstableApi::class)
    val carAutoplay: CarAutoplay by lazy {
        CarAutoplay(
            CarAutoplaySettings(getSharedPreferences(PREFS, Context.MODE_PRIVATE)),
            player = { services.intentPlayer },
            startService = { PlaybackService.startInForeground(this) },
            run = { body -> services.runIntent(body) },
            companion = SystemCompanion(this),
            refusedText = getString(R.string.intent_background_start_refused),
        )
    }

    @OptIn(UnstableApi::class)
    override fun onCreate() {
        super.onCreate()
        Playback.httpClient = { LarkHttp.client }
        // The host built first (the media service): the services come up with it and hand it their engine.
        Playback.onCreated = { if (!building && built == null) services }
        FavoritesRefresh.provider = { services.syncRun() }
    }

    @OptIn(UnstableApi::class)
    private fun build(): AppServices {
        building = true
        try {
            val host = Playback.host(this)
            val scope = loggingMainScope(TAG)                   // a failed task logged by its class only
            val s = AppServices(
                prefs = getSharedPreferences(PREFS, Context.MODE_PRIVATE),
                secrets = KeystoreSecretStore(File(filesDir, "secrets.bin")),
                webData = WebViewData(),
                http = LarkHttp.client,
                dataDirectory = filesDir,
                backend = checkNotNull(host.backend) { "the playback host has no backend" },
                network = AndroidNetworkStatus(this),
                cacheRoot = CacheLayout.defaultRoot(this),
                scope = scope,
                scheduler = MainScheduler(),
                clock = Clock.system,
                texts = EngineTexts.from(this),
                nowPlaying = host.nowPlayingSink,
                onEngine = { host.engine = it },
                refresh = WorkRefresh(this),
                freeSpace = { StorageManagerFreeSpace(this, it) },
            )
            s.serviceRunning = { PlaybackService.running }
            host.engineProvider = { s.engine }
            host.intentStarting = { s.intentRunning }
            built = s
            s.start()
            return s
        } finally {
            building = false
        }
    }

    /** The background favorites sync, through WorkManager. */
    private class WorkRefresh(private val context: Context) : RefreshScheduler {
        override fun schedule() = FavoritesRefresh.schedule(context)
        override fun cancel() = FavoritesRefresh.cancel(context)
    }

    private companion object {
        const val TAG = "LarkApplication"
        const val PREFS = "lark"
    }
}
