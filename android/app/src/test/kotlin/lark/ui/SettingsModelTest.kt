package lark.ui

import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import lark.app.AppServices
import lark.app.BuildConfig
import lark.app.R
import lark.app.ServicesTestCase
import lark.bridge.NativePrefs
import lark.bridge.WebMessage
import lark.car.CarAutoplay
import lark.car.CarAutoplaySettings
import lark.car.CompanionOps
import lark.car.IntentResult
import lark.testing.FakeIntentPlayer
import lark.testing.q
import lark.testing.trackModel
import okhttp3.mockwebserver.MockResponse
import okio.Buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.robolectric.Shadows.shadowOf
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * iOS `SettingsModelTests`: the settings screen's model, and what it drives in `AppServices`: the cache cap, the cache
 * buttons, the server change, the test button; and car autoplay and the battery guide (Android's own).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SettingsModelTest : ServicesTestCase() {
    /** A launch of the app: the real per-user cache under `dir/caches`, everything else fake. */
    private fun services(user: Long? = 5, prefs: android.content.SharedPreferences = newPrefs()) =
        makeServices(user = user, prefs = prefs, cache = null)

    private fun model(s: AppServices, car: CarAutoplay? = null) = SettingsModel(s, context, car)

    private fun storeOne(s: AppServices) {
        val file = File(dir, "in.m4a")
        file.writeBytes(ByteArray(1000) { 1 })
        val t = trackModel(9)
        s.cacheStore!!.store(9, file, t.track, t.json, favorite = true)
    }

    @Test fun capChoicePersistsAndAppliesToCache() = servicesTest {
        assertEquals(listOf(1, 2, 5, 10), SettingsModel.capChoices)
        val prefs = newPrefs()
        val s = services(prefs = prefs)
        val m = model(s)
        assertEquals(2, m.capGB)                                        // the default
        assertEquals(2L shl 30, s.cacheStore?.capBytes)
        m.setCap(5)
        assertEquals(5, m.capGB)
        assertEquals(5, prefs.getInt("lark.cacheCapGB", 0))
        assertEquals(5L shl 30, s.cacheStore?.capBytes)
        m.setCap(3)                                                     // not a choice: ignored
        assertEquals(5, m.capGB)
        assertEquals(5, prefs.getInt("lark.cacheCapGB", 0))
        // A relaunch reads it back.
        val again = services(prefs = prefs)
        assertEquals(5L shl 30, again.cacheStore?.capBytes)
        assertEquals(5, model(again).capGB)
    }

    @Test fun capWithNobodySignedInIsStillSaved() = servicesTest {
        val prefs = newPrefs()
        val s = services(user = null, prefs = prefs)
        assertNull(s.cacheStore)
        s.setCacheCap(10)
        assertEquals(10, s.cacheCapGB)
        assertEquals(10, prefs.getInt(AppServices.CACHE_CAP_KEY, 0))
    }

    @Test fun usageShowsUsedAndCap() = servicesTest {
        val s = services()
        val m = model(s)
        storeOne(s)
        m.refresh()
        assertEquals(1000L, m.usedBytes)
        assertEquals(2L shl 30, m.capBytes)
        assertTrue(m.usageText, m.usageText.contains("2 GB"))
        assertEquals("Used 1000 B / limit 2 GB", m.usageText)
    }

    @Test fun signedOutShowsOnlyTheCap() = servicesTest {
        val m = model(services(user = null))
        assertFalse(m.signedIn)
        assertEquals("Not signed in · limit 2 GB", m.usageText)
    }

    /** Binary units, as iOS `ByteCountFormatter(.binary)` and the cap's "2 GB" (2 << 30). */
    @Test fun byteCounts() {
        assertEquals("0 B", SettingsModel.bytes(0))
        assertEquals("1023 B", SettingsModel.bytes(1023))
        assertEquals("1 KB", SettingsModel.bytes(1024))
        assertEquals("12.3 MB", SettingsModel.bytes((12.3 * (1 shl 20)).toLong()))
        assertEquals("2 GB", SettingsModel.bytes(2L shl 30))
        assertEquals("1,5 GB", SettingsModel.bytes(3L shl 29, java.util.Locale.forLanguageTag("sv")))
    }

    @Test fun clearCacheGoesThroughTheStore() = servicesTest {
        val s = services()
        val m = model(s)
        storeOne(s)
        assertNotNull(s.cache.localFile(9))
        m.clearCache()
        assertNull(s.cache.localFile(9))
        assertEquals(0L, m.usedBytes)
        assertEquals("Cache cleared", m.message)
    }

    @Test fun syncNowRunsTheFavoritesSync() = servicesTest {
        val page = """{"items":[{"id":2,"title":"T2","artist":"A","album":"B","duration_ms":1000,"favorite":true}],"next_cursor":""}"""
        handler = { r ->
            val path = r.requestUrl?.encodedPath ?: ""
            when {
                path == "/api/v1/tracks" -> ok(page)
                path.endsWith("/stream") -> MockResponse().setHeader("Content-Type", "audio/mp4").setBody(Buffer().write(ByteArray(50) { 1 }))
                else -> status(404)
            }
        }
        val s = services()
        val m = model(s)
        m.syncNow()
        assertEquals("Syncing favorites (downloads only on Wi‑Fi)", m.message)
        waitUntil { s.cache.localFile(2) != null }
    }

    @Test fun changeServerSignsOutStopsPlaybackAndForgetsTheServer() = servicesTest {
        handler = { ok("""{"accepted":1}""") }
        val prefs = newPrefs()
        val s = services(prefs = prefs)
        val engine = s.engine!!
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        assertTrue(engine.playing)
        s.openSettings()

        model(s).changeServer()

        assertFalse(engine.playing)
        assertTrue(backend.calls.contains("stop"))
        assertNull(s.serverUrl)                                         // back to the server screen
        assertNull(prefs.getString(AppServices.SERVER_URL_KEY, null))
        assertNull(s.auth.token)
        assertFalse(s.showSettings)
        assertNull(s.engine)                                            // no server, no engine
        assertNull(s.intentPlayer)
        assertEquals(listOf(serverUrl), web.deleted)                    // the session cookie and token gone
        assertEquals(listOf(serverUrl), web.removed)                    // the old origin's web state goes with it
        assertTrue(engine.music.items.isEmpty())                        // the old server's queue is wiped
    }

    @Test fun aNewServerGetsAFreshEngineWithItsOwnApi() = servicesTest {
        val engines = mutableListOf<Any?>()
        val s = makeServices(cache = null, onEngine = { engines += it })
        val old = s.engine!!
        old.handle(q(listOf(1), 0, 0, true))
        val other = "https://other.lark.test/"
        s.setServerUrl(other)
        val fresh = s.engine!!
        assertTrue(fresh !== old)
        assertTrue(fresh.client !== old.client)                         // its own API, for the new server
        assertEquals(other, s.serverUrl)
        assertFalse(old.playing)                                        // the old server's playback stopped
        assertNull(s.auth.token)                                        // the old server's token never goes to the new one
        assertTrue(fresh.music.items.isEmpty())
        assertEquals(fresh, engines.last())                             // the playback host has the new one
    }

    /** The change marks the screen while it signs out, and a second one does nothing. */
    @Test fun changeServerIsMarkedWhileItRunsAndDoesNotReenter() = servicesTest {
        handler = { ok("""{"accepted":1}""").setBodyDelay(400, TimeUnit.MILLISECONDS) }
        val s = services()
        val engine = s.engine!!
        engine.handle(q(listOf(1), 0, 0, true))
        backend.start()
        backend.playTo(40_000)                                          // a listen worth posting on the way out
        assertFalse(s.changingServer)
        val first = launch { s.changeServer() }
        waitUntil { s.changingServer }
        val t0 = System.nanoTime()
        s.changeServer()                                                // re-entry: returns at once
        assertTrue((System.nanoTime() - t0) < 300_000_000)
        assertTrue(s.changingServer)
        waitUntil { first.isCompleted }
        assertFalse(s.changingServer)
        assertNull(s.serverUrl)
        assertEquals(1, requestsTo("/api/v1/events/play").size)         // flushed once
    }

    /** Switching straight to another server flushes the old server's pending events first. */
    @Test fun switchingServersFlushesThePendingEventsFirst() = servicesTest {
        handler = { ok("""{"accepted":1}""") }
        val s = services()
        val engine = s.engine!!
        engine.handle(q(listOf(1), 0, 0, true))
        backend.start()
        backend.playTo(40_000)
        s.setServerUrl("https://next.lark.test/")
        val posts = requestsTo("/api/v1/events/play")
        assertEquals(1, posts.size)                                     // to the old server: the only one this test serves
        assertEquals("Bearer T1", posts.first().getHeader("Authorization"))
    }

    @Test fun theTestButtonShowsWhyNothingPlays() = servicesTest {
        network.isOnline = false
        val m = model(services())
        m.testShuffle()
        assertEquals(IntentResult.offlineNothingCached.text(context), m.message)
        val out = model(services(user = null))
        out.testShuffle()
        assertEquals(IntentResult.notSignedIn.text(context), out.message)
    }

    /** Started, the test says nothing (the music is the answer); already playing, it says so (as iOS). */
    @Test fun theTestButtonRunsTheShuffleFavoritesIntent() = servicesTest {
        val s = services()
        val player = FakeIntentPlayer()
        s.intentPlayer = player
        val m = model(s)
        m.testShuffle()
        assertEquals(listOf("intentBlocker", "prepareForPlayback", "shuffleFavorites"), player.log)
        assertNull(m.message)
        player.playing = true
        m.testShuffle()
        assertEquals(IntentResult.alreadyPlaying.text(context), m.message)
    }

    @Test fun prefsSurviveARelaunch() = servicesTest {
        val prefs = newPrefs()
        val s = services(prefs = prefs)
        s.handle(WebMessage.SetPrefs(NativePrefs("saver", carLyrics = false)))
        assertEquals(NativePrefs("saver", carLyrics = false), s.engine?.prefs)
        assertEquals(NativePrefs("saver", carLyrics = false), services(prefs = prefs).engine?.prefs)
    }

    /** The loudness switch too, so a cold start from the car plays at the level the user chose. */
    @Test fun loudnessPrefSurvivesARelaunch() = servicesTest {
        val prefs = newPrefs()
        val s = services(prefs = prefs)
        s.handle(WebMessage.SetPrefs(NativePrefs("high", carLyrics = true, loudness = false)))
        assertEquals(NativePrefs("high", carLyrics = true, loudness = false), services(prefs = prefs).engine?.prefs)
        s.handle(WebMessage.SetPrefs(NativePrefs("high", carLyrics = true, loudness = true)))
        assertEquals(true, services(prefs = prefs).engine?.prefs?.loudness)
    }

    @Test fun versionTextHasVersionAndBuild() {
        assertEquals("${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})", SettingsModel.versionText)
    }

    @Test fun theServerIsShownOrNotSet() = servicesTest {
        assertEquals(serverUrl, model(services()).server)
        assertEquals("Not set", model(makeServices(server = null, cache = null)).server)
    }

    // MARK: Car autoplay (Android's: the companion device)

    private class RecordingCompanion : CompanionOps {
        val calls = mutableListOf<String>()
        override fun observe(address: String) { calls += "observe:$address" }
        override fun forget(address: String, associationId: Int) { calls += "forget:$address" }
        override fun setReceiverEnabled(on: Boolean) { calls += "receiver:$on" }
    }

    @Test fun carAutoplayShowsTheCarAndTurnsOff() = servicesTest {
        val s = services()
        val companion = RecordingCompanion()
        val car = CarAutoplay(CarAutoplaySettings(newPrefs()), player = { s.intentPlayer }, startService = { true },
            run = { body -> s.runIntent(body) }, companion = companion)
        val m = model(s, car)
        assertFalse(m.carEnabled)
        assertNull(m.carName)
        car.onAssociated("aa:bb:cc:dd:ee:ff", "My Car", 7)
        m.refresh()
        assertTrue(m.carEnabled)
        assertEquals("My Car", m.carName)
        m.turnOffCar()
        assertFalse(m.carEnabled)
        assertNull(m.carName)
        assertTrue(companion.calls.contains("forget:AA:BB:CC:DD:EE:FF"))
    }

    @Test fun carAutoplaySaysWhatTheLastConnectionDid() = servicesTest {
        network.isOnline = false
        val s = services()
        val car = CarAutoplay(CarAutoplaySettings(newPrefs()), player = { s.intentPlayer }, startService = { true },
            run = { body -> s.runIntent(body) })
        val m = model(s, car)
        assertNull(m.carResult)
        car.onAssociated("AA:BB:CC:DD:EE:FF", "My Car", 7)
        car.deviceAppeared("AA:BB:CC:DD:EE:FF")
        waitUntil { car.lastResult != null }
        m.refresh()
        assertEquals(IntentResult.offlineNothingCached.text(context), m.carResult)
    }

    @Test fun thePhoneMakersHint() {
        assertEquals(context.getString(R.string.car_autoplay_hint_xiaomi), SettingsModel.vendorHint(context, "Xiaomi"))
        assertEquals(context.getString(R.string.car_autoplay_hint_samsung), SettingsModel.vendorHint(context, "samsung"))
        assertNull(SettingsModel.vendorHint(context, "Google"))
    }
}

/** The battery guide: Android's exemption request for Melarka, and the docs for the phone makers' own settings. */
@org.junit.runner.RunWith(org.robolectric.RobolectricTestRunner::class)
@org.robolectric.annotation.Config(sdk = [34])
class BatteryGuideTest {
    private val context: android.content.Context get() = androidx.test.core.app.ApplicationProvider.getApplicationContext()

    @Test fun theButtonAsksAndroidForTheExemption() {
        val i: Intent = BatteryGuide.requestIntent(context)
        assertEquals(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, i.action)
        assertEquals(Uri.parse("package:${context.packageName}"), i.data)
    }

    @Test fun theManifestDeclaresThePermission() {
        val info = context.packageManager.getPackageInfo(context.packageName, android.content.pm.PackageManager.GET_PERMISSIONS)
        assertTrue(info.requestedPermissions.orEmpty().contains("android.permission.REQUEST_IGNORE_BATTERY_OPTIMIZATIONS"))
    }

    @Test fun itKnowsWhetherMelarkaIsExempt() {
        val power = context.getSystemService(PowerManager::class.java)
        assertFalse(BatteryGuide.isExempt(context))
        shadowOf(power).setIgnoringBatteryOptimizations(context.packageName, true)
        assertTrue(BatteryGuide.isExempt(context))
    }

    @Test fun theDocsAreTheAndroidGuideOnGitHub() {
        assertEquals("https://github.com/aaronsuns/melarka/blob/main/docs/android.md", BatteryGuide.DOCS_URL)
    }
}
