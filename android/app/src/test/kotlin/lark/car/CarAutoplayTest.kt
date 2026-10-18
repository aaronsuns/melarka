package lark.car

import android.content.ComponentName
import android.content.Context
import android.content.pm.PackageManager
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import lark.app.R
import lark.testing.FakeIntentPlayer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.UUID

/**
 * Car autoplay: the associated car appearing runs Shuffle Favorites once per appearance (the iOS Bluetooth automation's
 * action, `IntentRun.shuffleFavorites`), with the media service started into the foreground first; nothing while
 * already playing, nothing for another device, nothing when it is off, and no crash when Android refuses the start.
 */
@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CarAutoplayTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val scope = TestScope(UnconfinedTestDispatcher())
    private val prefs = context.getSharedPreferences("lark.tests.${UUID.randomUUID()}", Context.MODE_PRIVATE)
    private val settings = CarAutoplaySettings(prefs)
    private var fake: FakeIntentPlayer? = FakeIntentPlayer()
    private var starts = 0
    private var allowStart = true
    private val logged = mutableListOf<String>()
    /** Records what car autoplay asks of the system. */
    private class FakeCompanion : CompanionOps {
        val calls = mutableListOf<String>()
        override fun observe(address: String) { calls.add("observe $address") }
        override fun forget(address: String, associationId: Int) { calls.add("forget $address $associationId") }
        override fun setReceiverEnabled(on: Boolean) { calls.add("receiver $on") }
    }
    private val companion = FakeCompanion()
    private val car = CarAutoplay(settings, player = { fake }, startService = { starts++; fake?.log?.add("startService"); allowStart },
        run = { body -> scope.launch { body() } }, companion = companion, log = { logged.add(it) })

    private val address = "00:11:22:AA:BB:CC"

    @Test fun appearingDeviceShufflesOnce() {
        settings.save(address, "My car", 7)
        car.deviceAppeared(address)
        assertEquals(listOf("shuffleFavorites"), fake!!.calls)
        // The service comes up in the foreground before playback is prepared (and the blocker is asked before both).
        assertEquals(listOf("intentBlocker", "startService", "prepareForPlayback", "shuffleFavorites"), fake!!.log)
        assertEquals(IntentResult.played, car.lastResult)
        // Still connected (Android repeats itself, or another profile connects): nothing again, even once paused.
        fake!!.playing = false
        car.deviceAppeared(address.lowercase())
        assertEquals(1, fake!!.calls.size)
        // Gone and back: the next appearance shuffles again.
        car.deviceDisappeared(address)
        car.deviceAppeared(address)
        assertEquals(2, fake!!.calls.size)
        assertEquals(2, starts)
    }

    @Test fun alreadyPlayingDoesNothing() {
        settings.save(address, null, -1)
        fake!!.playing = true
        car.deviceAppeared(address)
        assertEquals(emptyList<String>(), fake!!.log)          // not even the service
        assertEquals(0, starts)
        assertEquals(IntentResult.alreadyPlaying, car.lastResult)
    }

    @Test fun anotherDeviceOrAutoplayOffDoesNothing() {
        car.deviceAppeared(address)                               // nothing associated
        settings.save(address, null, -1)
        car.deviceAppeared("66:77:88:99:AA:BB")                   // headphones, not the car
        settings.clear()
        car.deviceAppeared(address)                               // turned off
        assertEquals(emptyList<String>(), fake!!.log)
        assertNull(car.lastResult)
    }

    @Test fun aBlockedIntentNeverStartsTheService() {
        settings.save(address, null, -1)
        fake!!.blocker = IntentResult.notSignedIn
        car.deviceAppeared(address)
        assertEquals(0, starts)
        assertEquals(IntentResult.notSignedIn, car.lastResult)
        assertTrue(logged.single().contains("nothing played"))
    }

    @Test fun noServerYetIsNotSetUp() {
        settings.save(address, null, -1)
        fake = null
        car.deviceAppeared(address)
        assertEquals(0, starts)
        assertEquals(IntentResult.notSetUp, car.lastResult)
    }

    /** Android refused the foreground start: logged, nothing plays, no crash. */
    @Test fun aRefusedStartIsLoggedAndPlaysNothing() {
        settings.save(address, null, -1)
        allowStart = false
        car.deviceAppeared(address)
        assertEquals(listOf("intentBlocker", "startService"), fake!!.log)
        assertEquals(IntentResult.nothingToPlay(CarAutoplay.BACKGROUND_START_REFUSED), car.lastResult)
        assertTrue(logged.single().contains("nothing played"))
    }

    @Test fun theSettingsKeepTheCarUntilTurnedOff() {
        assertFalse(settings.enabled)
        settings.save("aa:bb:cc:dd:ee:ff", "Volvo", 3)
        assertTrue(settings.enabled)
        assertEquals("AA:BB:CC:DD:EE:FF", settings.address)
        assertEquals("Volvo", settings.name)
        assertEquals(3, settings.associationId)
        assertTrue(settings.matches("aa:bb:cc:dd:ee:ff"))
        assertTrue(CarAutoplaySettings(prefs).enabled)          // across launches
        settings.clear()
        assertFalse(settings.enabled)
        assertNull(settings.address)
    }

    /** Nearby devices is asked only on Android 12+, and only when car autoplay is turned on (nothing at install). */
    @Test fun permissionsAreAskedOnlyOnAndroid12AndLater() {
        assertEquals(listOf(android.Manifest.permission.BLUETOOTH_CONNECT), CarAutoplay.requiredPermissions(31))
        assertEquals(emptyList<String>(), CarAutoplay.requiredPermissions(30))
    }

    /** Android 12+ gets the companion device service (presence); older phones the Bluetooth broadcast instead. */
    @Test fun theCompanionServiceIsDeclaredForTheSystemOnly() {
        val pm = context.packageManager
        val info = pm.getServiceInfo(ComponentName(context, CarCompanionService::class.java), PackageManager.MATCH_DISABLED_COMPONENTS)
        assertEquals("android.permission.BIND_COMPANION_DEVICE_SERVICE", info.permission)
        assertTrue(context.resources.getBoolean(R.bool.car_companion_service))     // SDK 34 here
        val requested = pm.getPackageInfo(context.packageName, PackageManager.GET_PERMISSIONS).requestedPermissions!!.toList()
        assertTrue(requested.contains("android.permission.REQUEST_OBSERVE_COMPANION_DEVICE_PRESENCE"))
        assertTrue(requested.contains("android.permission.REQUEST_COMPANION_START_FOREGROUND_SERVICES_FROM_BACKGROUND"))
    }

    /** Below Android 12 the Bluetooth broadcast is used, and only while car autoplay is on (off in the manifest). */
    @Test @Config(sdk = [30]) fun belowAndroid12TheBluetoothReceiverIsOnOnlyWhileAutoplayIs() {
        assertFalse(context.resources.getBoolean(R.bool.car_companion_service))
        val pm = context.packageManager
        val receiver = ComponentName(context, CarConnectionReceiver::class.java)
        assertFalse(pm.getReceiverInfo(receiver, PackageManager.MATCH_DISABLED_COMPONENTS).enabled)
        val system = SystemCompanion(context)
        system.setReceiverEnabled(true)
        assertEquals(PackageManager.COMPONENT_ENABLED_STATE_ENABLED, pm.getComponentEnabledSetting(receiver))
        system.setReceiverEnabled(false)
        assertEquals(PackageManager.COMPONENT_ENABLED_STATE_DISABLED, pm.getComponentEnabledSetting(receiver))
        // The car autoplay switch drives it.
        val live = CarAutoplay(settings, { fake }, { true }, { }, companion = system)
        live.onAssociated(address, null, -1)
        assertEquals(PackageManager.COMPONENT_ENABLED_STATE_ENABLED, pm.getComponentEnabledSetting(receiver))
        live.turnOff()
        assertEquals(PackageManager.COMPONENT_ENABLED_STATE_DISABLED, pm.getComponentEnabledSetting(receiver))
    }

    /** Android 12+: the receiver is never enabled (the companion service is used). */
    @Test fun fromAndroid12TheReceiverStaysOff() {
        val pm = context.packageManager
        val receiver = ComponentName(context, CarConnectionReceiver::class.java)
        SystemCompanion(context).setReceiverEnabled(true)
        assertEquals(PackageManager.COMPONENT_ENABLED_STATE_DEFAULT, pm.getComponentEnabledSetting(receiver))
    }

    @Test fun associatingObservesTheCar() {
        car.onAssociated(address, "Volvo", 4)
        assertEquals(listOf("observe $address", "receiver true"), companion.calls)
        assertTrue(settings.enabled)
    }

    /** Picking another car lets the old one go first: only one car starts autoplay. */
    @Test fun pickingANewCarForgetsThePreviousOne() {
        car.onAssociated(address, "Old car", 4)
        car.deviceAppeared(address)
        companion.calls.clear()
        car.onAssociated("11:22:33:44:55:66", "New car", 5)
        assertEquals(listOf("forget $address 4", "observe 11:22:33:44:55:66", "receiver true"), companion.calls)
        assertEquals("New car", settings.name)
        car.deviceAppeared(address)                               // the old car does nothing now
        assertEquals(1, fake!!.calls.size)
        // The same car again (Android re-confirmed it with the same association): nothing to forget.
        companion.calls.clear()
        car.onAssociated("11:22:33:44:55:66", "New car", 5)
        assertEquals(listOf("observe 11:22:33:44:55:66", "receiver true"), companion.calls)
    }

    /** Off: the car forgotten and its presence too, so turning it on again with the car still connected starts afresh. */
    @Test fun turningOffForgetsTheCarAndItsPresence() {
        car.onAssociated(address, null, 4)
        car.deviceAppeared(address)
        companion.calls.clear()
        car.turnOff()
        assertEquals(listOf("forget $address 4", "receiver false"), companion.calls)
        assertFalse(settings.enabled)
        car.onAssociated(address, null, 6)
        fake!!.playing = false
        car.deviceAppeared(address)
        assertEquals(2, fake!!.calls.size)
    }

    /** The guidance for phone makers whose power management goes beyond Android's (dontkillmyapp.com). */
    @Test fun vendorGuidanceForTheBrandsThatNeedIt() {
        assertEquals(R.string.car_autoplay_hint_xiaomi, CarAutoplay.vendorHint("Xiaomi"))
        assertEquals(R.string.car_autoplay_hint_xiaomi, CarAutoplay.vendorHint("POCO"))
        assertEquals(R.string.car_autoplay_hint_samsung, CarAutoplay.vendorHint("samsung"))
        assertEquals(R.string.car_autoplay_hint_oppo, CarAutoplay.vendorHint("OnePlus"))
        assertEquals(R.string.car_autoplay_hint_huawei, CarAutoplay.vendorHint("HONOR"))
        assertEquals(R.string.car_autoplay_hint_vivo, CarAutoplay.vendorHint("vivo"))
        assertNull(CarAutoplay.vendorHint("Google"))
        assertNull(CarAutoplay.vendorHint("motorola"))
        assertTrue(context.getString(R.string.car_autoplay_hint_xiaomi).contains("Melarka"))
    }
}
