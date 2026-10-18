package lark.car

import android.app.Activity
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothManager
import android.companion.AssociationInfo
import android.companion.AssociationRequest
import android.companion.BluetoothDeviceFilter
import android.companion.CompanionDeviceManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.IntentSender
import android.content.pm.PackageManager
import android.os.Build
import android.util.Log
import androidx.core.content.IntentCompat
import lark.app.LarkApplication
import lark.app.R

/**
 * Car autoplay: when the car Melarka is associated with appears (its Bluetooth connects), the Shuffle Favorites intent
 * runs ([IntentRun.shuffleFavorites]): once per appearance, nothing at all if music is already playing (the car's own
 * play resumed the queue first), and nothing again until the car has gone and come back.
 *
 * Android tells Melarka through CompanionDeviceManager: on Android 12+ the system binds [CarCompanionService] when the
 * associated car connects (presence observation); below Android 12, [CarConnectionReceiver] hears the car's Bluetooth
 * connection (`ACL_CONNECTED`). Either way the media service is started into the foreground first ([startService],
 * `PlaybackService.startInForeground`), which Android 12+ allows from the background only for an app with a companion
 * association holding `REQUEST_COMPANION_START_FOREGROUND_SERVICES_FROM_BACKGROUND`; a refusal is logged and nothing
 * plays (never a crash). Main thread only.
 */
class CarAutoplay(
    val settings: CarAutoplaySettings,
    /** What the intent drives (`AppServices.intentPlayer`): null before a server is set. */
    private val player: () -> IntentPlayer?,
    /** Starts the media service into the foreground; false when Android refused. */
    private val startService: () -> Boolean,
    /** Runs the intent on the main thread, synchronously up to its first suspension (`AppServices.runIntent`). */
    private val run: (suspend () -> Unit) -> Unit,
    /** What CompanionDeviceManager and the package manager do for car autoplay ([SystemCompanion] in the app). */
    private val companion: CompanionOps = CompanionOps.none,
    /** The result text when Android refused the foreground start (`R.string.intent_background_start_refused`). */
    private val refusedText: String = BACKGROUND_START_REFUSED,
    private val log: (String) -> Unit = { Log.i(TAG, it) },
) {
    /** The cars present now (normalised addresses): an appearance runs the intent once. */
    private val present = mutableSetOf<String>()

    /** What the last appearance did (Settings shows it; tests). */
    var lastResult: IntentResult? = null
        private set

    /** The associated car appeared (connected): Shuffle Favorites, once until it disappears. */
    fun deviceAppeared(address: String) {
        if (!settings.enabled || !settings.matches(address)) return
        if (!present.add(CarAutoplaySettings.key(address))) return
        val p = player()
        run {
            val r = IntentRun.shuffleFavorites(p?.let { BackgroundStart(it, startService, refusedText) })
            lastResult = r
            if (r.failed) log("car autoplay: nothing played ($r)") else log("car autoplay: $r")
        }
    }

    /** The car disappeared (disconnected): its next appearance runs the intent again. */
    fun deviceDisappeared(address: String) {
        present.remove(CarAutoplaySettings.key(address))
    }

    /**
     * The car is associated (the system's confirmation came back): remembered, and Android told to report it. A car
     * picked before is let go first (its presence no longer observed, its association removed), so only one car
     * starts autoplay.
     */
    fun onAssociated(address: String, name: String?, associationId: Int) {
        val old = settings.address
        if (old != null && (!settings.matches(address) || settings.associationId != associationId)) {
            companion.forget(old, settings.associationId)
            present.remove(CarAutoplaySettings.key(old))
        }
        settings.save(address, name, associationId)
        companion.observe(address)
        companion.setReceiverEnabled(true)
    }

    /** Car autoplay off: presence no longer observed, the association removed, the car forgotten. */
    fun turnOff() {
        settings.address?.let { companion.forget(it, settings.associationId) }
        companion.setReceiverEnabled(false)
        settings.clear()
        present.clear()
    }

    /** A paired Bluetooth device the user can pick as the car. */
    data class Car(val address: String, val name: String)

    companion object {
        private const val TAG = "CarAutoplay"
        const val REQUEST_ASSOCIATE = 0x4C41     // "LA"
        const val REQUEST_PERMISSIONS = 0x4C42
        /** `Manifest.permission.BLUETOOTH_CONNECT` (API 31; asked only there). */
        const val BLUETOOTH_CONNECT = "android.permission.BLUETOOTH_CONNECT"
        /** Android refused the foreground start: the English text (the app passes the localised one). */
        const val BACKGROUND_START_REFUSED = "Android didn't allow Melarka to start playing in the background"

        /** The process's car autoplay (the application's). */
        fun of(context: Context): CarAutoplay = (context.applicationContext as LarkApplication).carAutoplay

        /**
         * The runtime permissions car autoplay asks for when the user turns it on: Nearby devices (`BLUETOOTH_CONNECT`)
         * on Android 12+, to list the paired cars and read the car's name. Below Android 12 nothing is asked
         * (`BLUETOOTH` is granted at install).
         */
        fun requiredPermissions(sdk: Int = Build.VERSION.SDK_INT): List<String> =
            if (sdk >= 31) listOf(BLUETOOTH_CONNECT) else emptyList()

        fun missingPermissions(context: Context): List<String> = requiredPermissions().filter {
            context.checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED
        }

        /** Asks for what is missing (the answer comes to the activity's `onRequestPermissionsResult`); false: nothing to ask. */
        fun requestPermissions(activity: Activity): Boolean {
            val missing = missingPermissions(activity)
            if (missing.isEmpty()) return false
            activity.requestPermissions(missing.toTypedArray(), REQUEST_PERMISSIONS)
            return true
        }

        /** The Bluetooth devices paired with this phone (empty without the permission or Bluetooth). */
        fun pairedCars(context: Context): List<Car> = try {
            val adapter = context.getSystemService(BluetoothManager::class.java)?.adapter
            adapter?.bondedDevices.orEmpty().map { Car(it.address, it.name ?: it.address) }.sortedBy { it.name }
        } catch (_: SecurityException) {
            emptyList()
        }

        /**
         * Asks Android to associate the car with Melarka: the system shows its own confirmation once (with [device], a
         * paired car's address, just that car; null: the nearby devices to choose from). The answer comes to the
         * activity's `onActivityResult` ([onActivityResult]).
         */
        fun associate(activity: Activity, device: String?) {
            val cdm = activity.getSystemService(CompanionDeviceManager::class.java)
            if (cdm == null) { Log.w(TAG, "no companion device manager on this phone"); return }
            val filter = BluetoothDeviceFilter.Builder().apply { if (device != null) setAddress(device) }.build()
            val request = AssociationRequest.Builder().addDeviceFilter(filter).setSingleDevice(device != null).build()
            val callback = object : CompanionDeviceManager.Callback() {
                @Deprecated("Android 13 calls it through onAssociationPending")
                override fun onDeviceFound(chooserLauncher: IntentSender) {
                    try {
                        activity.startIntentSenderForResult(chooserLauncher, REQUEST_ASSOCIATE, null, 0, 0, 0)
                    } catch (e: IntentSender.SendIntentException) {
                        Log.w(TAG, "the car picker could not open (${e.javaClass.simpleName})")
                    }
                }

                override fun onFailure(error: CharSequence?) {
                    Log.w(TAG, "car association failed")
                }
            }
            if (Build.VERSION.SDK_INT >= 33) cdm.associate(request, activity.mainExecutor, callback)
            else @Suppress("DEPRECATION") cdm.associate(request, callback, null)
        }

        /** The system's confirmation came back: true when it was the car picker's ([REQUEST_ASSOCIATE]). */
        fun onActivityResult(context: Context, requestCode: Int, resultCode: Int, data: Intent?): Boolean {
            if (requestCode != REQUEST_ASSOCIATE) return false
            if (resultCode != Activity.RESULT_OK || data == null) return true
            if (Build.VERSION.SDK_INT >= 33) {
                val info = IntentCompat.getParcelableExtra(data, CompanionDeviceManager.EXTRA_ASSOCIATION, AssociationInfo::class.java)
                val address = info?.deviceMacAddress?.toString() ?: return true
                associated(context, address, info.displayName?.toString(), info.id)
            } else {
                @Suppress("DEPRECATION")
                val device = IntentCompat.getParcelableExtra(data, CompanionDeviceManager.EXTRA_DEVICE, BluetoothDevice::class.java)
                    ?: return true
                val name = try { device.name } catch (_: SecurityException) { null }
                associated(context, device.address, name, -1)
            }
            return true
        }

        /** The car is associated: see [onAssociated]. */
        fun associated(context: Context, address: String, name: String?, associationId: Int) =
            of(context).onAssociated(address, name, associationId)

        /** Car autoplay off: see [turnOff]. */
        fun turnOff(context: Context) = of(context).turnOff()

        /**
         * What a phone maker's own power management may need besides Android's battery setting, for car autoplay and
         * playback with the screen off (dontkillmyapp.com): a guidance text for Settings, or null on phones that follow
         * stock Android (Pixel, Motorola, Nokia, Sony, …), where the generic battery text is enough.
         */
        fun vendorHint(manufacturer: String = Build.MANUFACTURER): Int? = when (manufacturer.trim().lowercase()) {
            "xiaomi", "redmi", "poco" -> R.string.car_autoplay_hint_xiaomi
            "huawei", "honor" -> R.string.car_autoplay_hint_huawei
            "oppo", "realme", "oneplus" -> R.string.car_autoplay_hint_oppo
            "vivo", "iqoo" -> R.string.car_autoplay_hint_vivo
            "samsung" -> R.string.car_autoplay_hint_samsung
            else -> null
        }
    }
}

/**
 * The intent's player with the media service started into the foreground before playback is prepared: from the
 * background (car autoplay) a plain start is refused and a foreground start is allowed (see [CarAutoplay]); from a
 * shortcut, the foreground start's notification covers a shuffle from the server that ends after the shortcut's
 * activity is gone. Refused: nothing plays, and [refusedText] says why.
 */
internal class BackgroundStart(
    private val inner: IntentPlayer,
    private val start: () -> Boolean,
    private val refusedText: String,
) : IntentPlayer by inner {
    private var refused = false

    override fun prepareForPlayback() {
        if (start()) inner.prepareForPlayback() else refused = true
    }

    override suspend fun shuffleFavorites() { if (!refused) inner.shuffleFavorites() }
    override suspend fun resumeOrShuffleFavorites() { if (!refused) inner.resumeOrShuffleFavorites() }
    override val failureText: String? get() = if (refused) refusedText else inner.failureText
}

/** What car autoplay asks of the system; a fake in tests. */
interface CompanionOps {
    /** Android 12+: report the car's presence (bind [CarCompanionService]). */
    fun observe(address: String)
    /** Stop reporting the car and remove its association (Android 13+ by [associationId] when known). */
    fun forget(address: String, associationId: Int)
    /** Below Android 12: [CarConnectionReceiver] gets the Bluetooth broadcasts only while car autoplay is on. */
    fun setReceiverEnabled(on: Boolean)

    companion object {
        val none = object : CompanionOps {
            override fun observe(address: String) {}
            override fun forget(address: String, associationId: Int) {}
            override fun setReceiverEnabled(on: Boolean) {}
        }
    }
}

/** [CompanionOps] on CompanionDeviceManager and the package manager. Failures are logged, never thrown. */
class SystemCompanion(private val context: Context) : CompanionOps {
    private val cdm: CompanionDeviceManager? get() = context.getSystemService(CompanionDeviceManager::class.java)

    override fun observe(address: String) {
        if (Build.VERSION.SDK_INT < 31) return
        try {
            @Suppress("DEPRECATION")
            cdm?.startObservingDevicePresence(address)
        } catch (e: RuntimeException) {
            Log.w(TAG, "car presence not observed (${e.javaClass.simpleName})")
        }
    }

    override fun forget(address: String, associationId: Int) {
        val cdm = cdm ?: return
        try {
            @Suppress("DEPRECATION")
            if (Build.VERSION.SDK_INT >= 31) cdm.stopObservingDevicePresence(address)
            if (Build.VERSION.SDK_INT >= 33 && associationId >= 0) cdm.disassociate(associationId)
            else @Suppress("DEPRECATION") cdm.disassociate(address)
        } catch (e: RuntimeException) {
            Log.w(TAG, "car association not removed (${e.javaClass.simpleName})")
        }
    }

    override fun setReceiverEnabled(on: Boolean) {
        if (Build.VERSION.SDK_INT >= 31) return
        context.packageManager.setComponentEnabledSetting(ComponentName(context, CarConnectionReceiver::class.java),
            if (on) PackageManager.COMPONENT_ENABLED_STATE_ENABLED else PackageManager.COMPONENT_ENABLED_STATE_DISABLED,
            PackageManager.DONT_KILL_APP)
    }

    private companion object {
        const val TAG = "CarAutoplay"
    }
}
