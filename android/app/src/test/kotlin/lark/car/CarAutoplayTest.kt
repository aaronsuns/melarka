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
    private val car = CarAutoplay(settings, player = { fake }, startService = { starts++; fake?.log?.add("startService"); allowStart },
        run = { body -> scope.launch { body() } }, log = { logged.add(it) })

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
        assertFalse(context.resources.getBoolean(R.bool.car_connection_receiver))
        val requested = pm.getPackageInfo(context.packageName, PackageManager.GET_PERMISSIONS).requestedPermissions!!.toList()
        assertTrue(requested.contains("android.permission.REQUEST_OBSERVE_COMPANION_DEVICE_PRESENCE"))
        assertTrue(requested.contains("android.permission.REQUEST_COMPANION_START_FOREGROUND_SERVICES_FROM_BACKGROUND"))
    }

    @Test @Config(sdk = [30]) fun belowAndroid12TheBluetoothBroadcastIsUsed() {
        assertFalse(context.resources.getBoolean(R.bool.car_companion_service))
        assertTrue(context.resources.getBoolean(R.bool.car_connection_receiver))
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
