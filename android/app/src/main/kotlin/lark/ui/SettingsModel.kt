package lark.ui

import android.content.Context
import android.os.Build
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import lark.app.AppServices
import lark.app.BuildConfig
import lark.app.R
import lark.cache.CacheStore
import lark.car.CarAutoplay
import lark.car.IntentResult
import lark.car.IntentRun
import java.util.Locale

/**
 * What the settings screen shows and does (port of iOS `SettingsModel`). Everything goes through [AppServices]: the cap
 * (`setCacheCap`), the cache (`cacheStore`), the sync (`syncFavorites`), the server change (`changeServer`), and the
 * test button through the same path as the Shuffle Favorites shortcut. Car autoplay is [car]'s (null in tests that
 * don't need it). Texts come from [context]'s resources, in the phone's language.
 */
class SettingsModel(
    private val services: AppServices,
    private val context: Context,
    private val car: CarAutoplay? = null,
) {
    var capGB: Int by mutableIntStateOf(services.cacheCapGB); private set
    var usedBytes: Long by mutableLongStateOf(0L); private set
    /** A one-line answer to the last button (sync started, the test could not run). */
    var message: String? by mutableStateOf(null)

    /** Car autoplay is on, for [carName]. */
    var carEnabled: Boolean by mutableStateOf(false); private set
    var carName: String? by mutableStateOf(null); private set
    /** What the car's last connection did, in words; null before it first connected. */
    var carResult: String? by mutableStateOf(null); private set

    init { refresh() }

    val server: String get() = services.serverUrl ?: context.getString(R.string.settings_not_set)
    val capBytes: Long get() = capGB.toLong() shl 30
    val signedIn: Boolean get() = services.cacheStore != null

    /** "Used 12.3 MB / limit 2 GB" (in the phone's language); signed out, only the cap. */
    val usageText: String
        get() {
            val cap = "$capGB GB"
            if (!signedIn) return context.getString(R.string.cache_not_signed_in, cap)
            return context.getString(R.string.cache_used, bytes(usedBytes), cap)
        }

    fun refresh() {
        usedBytes = services.cacheStore?.usedBytes ?: 0
        capGB = services.cacheCapGB
        val settings = car?.settings
        carEnabled = settings?.enabled == true
        carName = if (carEnabled) settings?.name ?: settings?.address else null
        carResult = car?.lastResult?.let { r ->
            if (r == IntentResult.played) context.getString(R.string.car_autoplay_played) else r.text(context)
        }
    }

    /** One of [capChoices]; anything else is ignored. Saved, and applied to the cache at once (it evicts). */
    fun setCap(gb: Int) {
        services.setCacheCap(gb)
        refresh()
    }

    fun syncNow() {
        services.syncFavorites()
        message = context.getString(R.string.syncing)
    }

    fun clearCache() {
        services.cacheStore?.clear()
        refresh()
        message = context.getString(R.string.cache_cleared)
    }

    /** The Test button: exactly what the Shuffle Favorites shortcut runs. */
    suspend fun testShuffle() {
        val r = IntentRun.shuffleFavorites(services.intentPlayer)
        message = if (r == IntentResult.played) null else r.text(context)     // started: the music is the answer
    }

    /** Change Server: signs out of this server and goes back to the server screen. */
    suspend fun changeServer() = services.changeServer()

    /** Car autoplay off: the association removed, the car forgotten. */
    fun turnOffCar() {
        car?.turnOff()
        refresh()
    }

    companion object {
        val capChoices: List<Int> = CacheStore.CAP_CHOICES_GB

        /** "0.1.0 (100)": the version and the version code. */
        val versionText: String get() = "${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})"

        /** Below Android 12 the system does not wake Melarka for the car (`car_autoplay_old_android`). */
        val oldAndroid: Boolean get() = Build.VERSION.SDK_INT < 31

        /** The phone maker's extra power setting, in words (`CarAutoplay.vendorHint`); null on stock-like phones. */
        fun vendorHint(context: Context, manufacturer: String = Build.MANUFACTURER): String? =
            CarAutoplay.vendorHint(manufacturer)?.let { context.getString(it) }

        /** A size in binary units, as iOS `ByteCountFormatter(.binary)`: "1000 B", "12.3 MB", "2 GB". */
        fun bytes(n: Long, locale: Locale = Locale.getDefault()): String {
            if (n < 1024) return "$n B"
            val units = listOf("KB", "MB", "GB", "TB")
            var v = n / 1024.0
            var u = 0
            while (v >= 1024 && u < units.size - 1) { v /= 1024; u++ }
            val text = if (v >= 100 || v == Math.floor(v)) String.format(locale, "%.0f", v) else String.format(locale, "%.1f", v)
            return "$text ${units[u]}"
        }
    }
}
