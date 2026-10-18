package lark.car

import android.content.ComponentName
import android.content.Context
import android.content.pm.PackageManager
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.test.runTest
import lark.api.RandomFavorites
import lark.app.R
import lark.app.ServicesTestCase
import lark.testing.EngineTestCase
import lark.testing.FakeIntentPlayer
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.xmlpull.v1.XmlPullParser
import java.io.IOException

/**
 * Port of ios/Tests/IntentTests.swift (`IntentTests`): the shortcuts' intents against a fake player, their texts, and the
 * launcher shortcuts that offer them. The engine cases are [IntentEngineTest]; the services' player [IntentServicesTest].
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class IntentRunTest {
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test fun shuffleIntentCallsEngineWithoutUI() = runTest {
        val fake = FakeIntentPlayer()
        assertEquals(IntentResult.played, ShortcutActivity.run(ShortcutActivity.ACTION_SHUFFLE_FAVORITES, fake, { true }, "refused"))
        assertEquals(listOf("shuffleFavorites"), fake.calls)
        // Playback is prepared synchronously, before the intent's first suspension.
        assertEquals(listOf("intentBlocker", "prepareForPlayback", "shuffleFavorites"), fake.log)
        assertNoUI()
    }

    @Test fun resumeIntentResumesOrShufflesWithoutUI() = runTest {
        val fake = FakeIntentPlayer()
        assertEquals(IntentResult.played, ShortcutActivity.run(ShortcutActivity.ACTION_RESUME, fake, { true }, "refused"))
        assertEquals(listOf("resumeOrShuffleFavorites"), fake.calls)
        assertEquals(listOf("intentBlocker", "prepareForPlayback", "resumeOrShuffleFavorites"), fake.log)
        assertNoUI()
    }

    /** iOS `openAppWhenRun = false`: the shortcuts' activity shows nothing (`Theme.NoDisplay`) and is not exported. */
    private fun assertNoUI() {
        val info = context.packageManager.getActivityInfo(ComponentName(context, ShortcutActivity::class.java), PackageManager.GET_META_DATA)
        assertEquals(android.R.style.Theme_NoDisplay, info.theme)
        assertTrue(!info.exported)
    }

    /** The shortcut starts the media service in the foreground first, while its activity is visible (the cold-start hold). */
    @Test fun shortcutsStartTheServiceInTheForegroundBeforePreparing() = runTest {
        for (action in listOf(ShortcutActivity.ACTION_SHUFFLE_FAVORITES, ShortcutActivity.ACTION_RESUME)) {
            val fake = FakeIntentPlayer()
            ShortcutActivity.run(action, fake, { fake.log.add("startInForeground"); true }, "refused")
            assertEquals(listOf("intentBlocker", "startInForeground", "prepareForPlayback"), fake.log.take(3))
        }
        // Refused (not expected while the activity is visible): nothing plays, and the text says why.
        val fake = FakeIntentPlayer()
        assertEquals(IntentResult.nothingToPlay("refused"),
            ShortcutActivity.run(ShortcutActivity.ACTION_SHUFFLE_FAVORITES, fake, { false }, "refused"))
        assertEquals(emptyList<String>(), fake.calls)
        // Blocked or already playing: the service is not started at all.
        var starts = 0
        val playing = FakeIntentPlayer().also { it.playing = true }
        ShortcutActivity.run(ShortcutActivity.ACTION_SHUFFLE_FAVORITES, playing, { starts++; true }, "refused")
        val blocked = FakeIntentPlayer().also { it.blocker = IntentResult.notSignedIn }
        ShortcutActivity.run(ShortcutActivity.ACTION_RESUME, blocked, { starts++; true }, "refused")
        assertEquals(0, starts)
        assertEquals("Android didn't allow Melarka to start playing in the background",
            context.getString(R.string.intent_background_start_refused))
    }

    @Test fun anUnknownShortcutDoesNothing() = runTest {
        val fake = FakeIntentPlayer()
        assertNull(ShortcutActivity.run("lark.action.OTHER", fake, { true }, "refused"))
        assertEquals(emptyList<String>(), fake.log)
    }

    private fun texts(locale: String): Context {
        val c = android.content.res.Configuration(context.resources.configuration)
        c.setLocale(java.util.Locale.forLanguageTag(locale))
        return context.createConfigurationContext(c)
    }

    @Test fun intentTextsAreEnglishChineseAndSwedish() {
        val en = texts("en"); val hans = texts("zh-CN"); val hant = texts("zh-TW"); val sv = texts("sv")
        assertEquals("随机播放收藏", hans.getString(R.string.shortcut_shuffle_favorites))
        assertEquals("继续播放", hans.getString(R.string.shortcut_resume))
        assertEquals("Shuffle Favorites", en.getString(R.string.shortcut_shuffle_favorites))
        assertEquals("Resume", en.getString(R.string.shortcut_resume))
        assertEquals("未登录", IntentResult.notSignedIn.text(hans))
        assertEquals("Not signed in", IntentResult.notSignedIn.text(en))
        assertEquals("离线且没有缓存的歌曲", IntentResult.offlineNothingCached.text(hans))
        assertEquals("Offline, and no songs are cached", IntentResult.offlineNothingCached.text(en))
        assertEquals("正在随机播放收藏", IntentResult.played.text(hans))
        assertEquals("Shuffling favorites", IntentResult.played.text(en))
        // Android adds Traditional Chinese (in Traditional characters) and Swedish.
        assertEquals("隨機播放收藏", hant.getString(R.string.shortcut_shuffle_favorites))
        assertEquals("未登入", IntentResult.notSignedIn.text(hant))
        assertEquals("Blanda favoriter", sv.getString(R.string.shortcut_shuffle_favorites))
        assertEquals("Inte inloggad", IntentResult.notSignedIn.text(sv))
        assertEquals("Open Melarka and set the server first", IntentResult.notSetUp.text(en))
        assertEquals("Already playing", IntentResult.alreadyPlaying.text(en))
        // A Resume that played says nothing (iOS returns no dialog).
        assertNull(IntentResult.played.text(en, shuffle = false))
    }

    /** Finding 4: what cannot play fails with its reason, and never prepares playback. */
    @Test fun aBlockedIntentFailsWithItsReasonAndDoesNotPrepare() = runTest {
        for (blocker in listOf(IntentResult.notSignedIn, IntentResult.offlineNothingCached)) {
            val fake = FakeIntentPlayer(); fake.blocker = blocker
            assertEquals(blocker, IntentRun.shuffleFavorites(fake))
            assertEquals(blocker, IntentRun.resume(fake))
            assertEquals(listOf("intentBlocker", "intentBlocker"), fake.log)
        }
    }

    @Test fun shuffleIntentWhilePlayingIsANoOp() = runTest {
        val fake = FakeIntentPlayer(); fake.playing = true
        val outcome = IntentRun.shuffleFavorites(fake)
        assertEquals(IntentResult.alreadyPlaying, outcome)
        assertEquals(emptyList<String>(), fake.log)                  // not even the service
        assertEquals("Already playing", outcome.text(context))
    }

    @Test fun aStartThatDidNotPlayFailsWithTheEnginesReason() = runTest {
        val fake = FakeIntentPlayer(); fake.startsPlaying = false; fake.failureText = "无法获取收藏，请稍后再试"
        assertEquals(IntentResult.nothingToPlay("无法获取收藏，请稍后再试"), IntentRun.shuffleFavorites(fake))
        assertEquals("x", IntentResult.nothingToPlay("x").text(context))
        assertEquals("Can't play", IntentResult.nothingToPlay(null).text(context))   // iOS `PlayerEngine.cannotPlay`
    }

    /** iOS `LarkShortcuts.appShortcuts.count == 2`: the launcher shortcuts, both on [ShortcutActivity]. */
    @Test fun shortcutsOfferBothIntents() {
        val found = mutableListOf<Pair<String?, String?>>()
        context.resources.getXml(R.xml.shortcuts).use { xml ->
            while (xml.next() != XmlPullParser.END_DOCUMENT) {
                if (xml.eventType == XmlPullParser.START_TAG && xml.name == "intent")
                    found += xml.getAttributeValue(ANDROID, "action") to xml.getAttributeValue(ANDROID, "targetClass")
            }
        }
        assertEquals(listOf(ShortcutActivity.ACTION_SHUFFLE_FAVORITES to ShortcutActivity::class.java.name,
            ShortcutActivity.ACTION_RESUME to ShortcutActivity::class.java.name), found)
        val main = context.packageManager.getActivityInfo(ComponentName(context, lark.app.MainActivity::class.java), PackageManager.GET_META_DATA)
        assertEquals(R.xml.shortcuts, main.metaData.getInt("android.app.shortcuts"))
    }

    @Test fun withNoServerTheIntentSaysSoInsteadOfDoingNothing() = runTest {
        assertEquals(IntentResult.notSetUp, IntentRun.shuffleFavorites(null))
        assertEquals(IntentResult.notSetUp, IntentRun.resume(null))
    }

    private inline fun <T> android.content.res.XmlResourceParser.use(block: (android.content.res.XmlResourceParser) -> T): T =
        try { block(this) } catch (e: IOException) { throw AssertionError(e) } finally { close() }

    private companion object {
        const val ANDROID = "http://schemas.android.com/apk/res/android"
    }
}

/** The services' player (iOS `testThePlayerTheIntentsDriveIsTheEngineUnlessReplaced`). */
class IntentServicesTest : ServicesTestCase() {
    @Test fun thePlayerTheIntentsDriveIsTheEngineUnlessReplaced() = servicesTest {
        val s = makeServices(server = null)
        assertNull(s.intentPlayer)                                   // no server yet: nothing to drive
        s.setServerUrl(serverUrl)
        assertSame(s.engine, (s.intentPlayer as EngineIntentPlayer).engine)
        assertSame(s.intentPlayer, s.intentPlayer)                   // one adapter per engine
        val fake = FakeIntentPlayer()
        s.intentPlayer = fake
        assertSame(fake, s.intentPlayer)
        s.intentPlayer = null
        assertSame(s.engine, (s.intentPlayer as EngineIntentPlayer).engine)
    }
}

/** Port of `IntentEngineTests`: the intents with the real engine; playback is prepared before anything is awaited. */
class IntentEngineTest : EngineTestCase() {
    private val player get() = EngineIntentPlayer(engine)

    @Test fun shufflePreparesBeforeTheServerIsAsked() = engineTest {
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(4), trackModel(5)))
        val activations = mutableListOf<Int>()
        engine.willStartPlayback = { activations.add(api.randomFavoritesCalls.size) }
        assertEquals(IntentResult.played, IntentRun.shuffleFavorites(player))
        assertEquals(0, activations.first())                          // before the network call
        assertEquals(1, api.randomFavoritesCalls.size)
        assertTrue(engine.playing)
        assertEquals(lark.bridge.QueueSource.favorites, engine.music.source)
    }

    @Test fun signedOutFailsBeforePreparing() = engineTest {
        api.token = null
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        assertEquals(IntentResult.notSignedIn, IntentRun.shuffleFavorites(player))
        assertEquals(IntentResult.notSignedIn, IntentRun.resume(player))
        assertEquals(0, activations)
        assertTrue(api.randomFavoritesCalls.isEmpty())
    }

    @Test fun offlineWithNothingCachedFailsBeforePreparing() = engineTest {
        network.isOnline = false
        engine.handle(q(listOf(5, 6), 0, 0, false))                    // a queue, but nothing of it cached
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        assertEquals(IntentResult.offlineNothingCached, IntentRun.shuffleFavorites(player))
        assertEquals(IntentResult.offlineNothingCached, IntentRun.resume(player))
        assertEquals(0, activations)
        // Something cached in the queue: resume may play, shuffle still has no cached favorite.
        cache.local[6] = tmp("6.m4a")
        assertEquals(IntentResult.played, IntentRun.resume(player))
        assertEquals(lark.player.MediaSource.File(tmp("6.m4a")), backend.loads.last().src)
        engine.pause()                                                  // a shuffle while playing is a no-op
        assertEquals(IntentResult.offlineNothingCached, IntentRun.shuffleFavorites(player))
    }

    @Test fun serverUnreachableFailsWithTheReason() = engineTest {
        api.refillError = java.net.SocketTimeoutException()
        assertEquals(IntentResult.nothingToPlay(texts.shuffleFailedNotice), IntentRun.shuffleFavorites(player))
    }

    @Test fun resumeIntentPlaysTheRestoredQueue() = engineTest {
        engine.handle(q(listOf(5, 6), 1, 7000, false))
        makeEngine()
        assertEquals(IntentResult.played, IntentRun.resume(player))
        assertEquals(7000L, backend.loads.last().startMs)
        assertEquals("6", engine.current?.id)
        assertTrue(engine.playing)
    }

    /** Permanent focus loss (another app took the audio for good) is the user's pause: the intent starts afresh, it does not count as resumable. */
    @Test fun aPermanentFocusLossIsNotTreatedAsResumable() = engineTest {
        cache.favorites = listOf(trackModel(1)); cache.local[1] = tmp("1.m4a"); cache.local[5] = tmp("5.m4a")
        engine.handle(q(listOf(5), 0, 0, true))
        backend.start()
        backend.pausedForGood()
        assertTrue(!engine.playing)
        assertTrue(!engine.pausedBySystem)
        assertEquals(IntentResult.played, IntentRun.shuffleFavorites(player))   // not "already playing"
        assertEquals("1", engine.current?.id)
    }
}
