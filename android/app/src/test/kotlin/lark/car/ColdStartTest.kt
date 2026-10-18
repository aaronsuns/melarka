package lark.car

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.Looper
import android.view.KeyEvent
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.MediaSession
import androidx.test.core.app.ApplicationProvider
import lark.api.RandomFavorites
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.app.ServicesTestCase
import lark.player.MediaSource
import lark.player.PlaybackQueue
import lark.player.QueueSnapshot
import lark.player.QueueStore
import lark.service.NowPlayingController
import lark.service.PlaybackHost
import lark.service.PlaybackService
import lark.service.RemoteCommands
import lark.testing.EngineTestCase
import lark.testing.FakeCache
import lark.testing.FakeInfoSink
import lark.testing.FakeNetwork
import lark.testing.ServiceRig
import lark.testing.SessionPlayerRig
import lark.testing.episode
import lark.testing.q
import lark.testing.track
import lark.testing.trackModel
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import java.io.File
import java.net.InetAddress

/**
 * Port of ios/Tests/CarColdStartTests.swift: Melarka not running (or killed), no network, the car sends play. The engine
 * is built, the music queue comes back from disk, and the current item plays from the cache before any network call.
 * With no queue, cached favorites are shuffled. The app-graph cases are [ColdStartServicesTest]; the media service's
 * side (the foreground within 5 s, playback resumption) [ColdStartServiceTest].
 */
@UnstableApi
class ColdStartTest : EngineTestCase() {
    /** Every request the engine could make through the fake API. */
    private val apiCalls: Int
        get() = api.randomFavoritesCalls.size + api.randomTracksCalls.size + api.radioCalls.size + api.queueCalls +
            api.saveCalls.size + api.postedEvents.size + api.progressCalls.size + api.lyricsCalls.size +
            api.favoritesCalls + api.downloadCalls.size

    /** What the media session's play runs (iOS `RemoteCommands.handlePlay`, MPRemoteCommandCenter's playCommand). */
    private fun handlePlay() = RemoteCommands.handlePlay(engine, scope)

    private fun saved(music: PlaybackQueue, episodes: PlaybackQueue = PlaybackQueue.empty, active: Item.Kind = Item.Kind.track) =
        store.save(QueueSnapshot(music = music, episodes = episodes, active = active, savedAt = 0))

    @Test fun playCommandAfterColdStartPlaysPersistedQueueFromDisk() = engineTest {
        saved(PlaybackQueue(listOf(track(7), track(8)), 1, 42_000, QueueSource.favorites))
        cache.local[8] = tmp("8.m4a"); network.isOnline = false
        makeEngine()                                                   // a fresh process: restore happens in init
        var activated = 0
        engine.willStartPlayback = { activated += 1 }
        assertEquals(RemoteCommands.Status.success, handlePlay())
        // Synchronously, inside the handler: nothing waited on the network.
        assertEquals(MediaSource.File(tmp("8.m4a")), backend.loads.last().src)
        assertEquals(42_000L, backend.loads.last().startMs)
        assertTrue(backend.loads.last().autoplay)
        assertEquals(0, apiCalls)
        assertEquals(1, activated)
        assertTrue(engine.playing)
        assertEquals(QueueSource.favorites, engine.music.source)
    }

    @Test fun playCommandWithEmptyQueueShufflesCachedFavoritesOffline() = engineTest {
        cache.favorites = listOf(trackModel(1), trackModel(2), trackModel(3))
        cache.local[1] = tmp("1.m4a"); cache.local[2] = tmp("2.m4a"); cache.local[3] = tmp("3.m4a")
        network.isOnline = false
        makeEngine()
        assertEquals(RemoteCommands.Status.success, handlePlay())
        // Cached favorites start inside the handler too: no coroutine, no suspension.
        assertEquals(1, backend.loads.size)
        val played = backend.loads.first()
        assertTrue(listOf("1", "2", "3").map { MediaSource.File(tmp("$it.m4a")) }.contains(played.src))
        assertTrue(played.autoplay)
        assertEquals(QueueSource.favorites, engine.music.source)
        assertEquals(setOf("1", "2", "3"), engine.music.items.map { it.id }.toSet())
        assertTrue(notices.contains(texts.offlineNotice))
        assertEquals(0, apiCalls)                                      // nothing was awaited before the sound
        // The short favorites queue wants a refill, but offline it is not asked for at all.
        idle()
        scheduler.advance(120.0)
        idle()
        assertEquals(0, api.randomFavoritesCalls.size + api.randomTracksCalls.size + api.radioCalls.size)
        // The network is back: exactly one refill, appended to the queue.
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(20), trackModel(21)))
        network.isOnline = true
        idle()
        assertEquals(1, api.randomFavoritesCalls.size)
        assertTrue(engine.music.items.map { it.id }.contains("20"))
        assertEquals(0, api.randomTracksCalls.size + api.radioCalls.size)
    }

    /**
     * The saved current track was streamed (not cached), and there is no network. The car's play goes straight to a
     * cached item of the queue, never a stream first; the streamed one stays queued.
     */
    @Test fun playCommandOfflineWithUncachedCurrentPlaysACachedItemFirst() = engineTest {
        saved(PlaybackQueue(listOf(track(7), track(8), track(9)), 0, 30_000, QueueSource.radio))
        cache.local[9] = tmp("9.m4a"); network.isOnline = false
        makeEngine()
        assertEquals(RemoteCommands.Status.success, handlePlay())
        assertEquals(1, backend.loads.size)
        assertEquals(MediaSource.File(tmp("9.m4a")), backend.loads.first().src)
        assertTrue(backend.loads.all { it.src is MediaSource.File })
        assertEquals("9", engine.current?.id)
        val after = engine.music.items.drop(engine.music.index + 1).map { it.id }
        assertEquals("7", after.first())                               // the streamed track, right after
        assertEquals(0, apiCalls)
    }

    /** The same before the network monitor's first answer (a cold launch: `isOnline` is only a guess). */
    @Test fun playCommandBeforeTheNetworkAnswersPlaysACachedItemFirst() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, false))
        cache.favorites = listOf(trackModel(3)); cache.local[3] = tmp("3.m4a")
        network.answered = false
        makeEngine()
        assertEquals(RemoteCommands.Status.success, handlePlay())
        assertEquals(listOf<MediaSource>(MediaSource.File(tmp("3.m4a"))), backend.loads.map { it.src })  // not the stream of 7
        assertEquals("3", engine.current?.id)
        assertTrue(engine.music.items.map { it.id }.contains("7"))
    }

    /** Online and answered, an uncached current track streams as before. */
    @Test fun onlineTheCurrentTrackStreams() = engineTest {
        engine.handle(q(listOf(7, 8), 0, 0, false))
        cache.local[8] = tmp("8.m4a")
        makeEngine()
        assertEquals(RemoteCommands.Status.success, handlePlay())
        assertTrue("streams the current track", backend.loads.last().src is MediaSource.Remote)
        assertEquals("7", engine.current?.id)
    }

    /** The commands are installed once and resolve the engine when a command arrives. */
    @Test fun commandsWithNoEngineYetHaveNothingToDo() = engineTest {
        val rc = RemoteCommands(scope) { null }
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, rc.playNow())
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, rc.pauseNow())
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, rc.toggleNow())
        assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, rc.nextNow())
    }

    @Test fun shuffleFavoritesOnlineUsesAPIAndCachedFirstWhenOffline() = engineTest {
        // Online, nothing cached: the server's random favorites, streamed.
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(4), trackModel(5)))
        engine.shuffleFavorites()
        assertEquals(1, api.randomFavoritesCalls.size)
        assertTrue("streams what the server chose", backend.loads.last().src is MediaSource.Remote)
        assertEquals(QueueSource.favorites, engine.music.source)

        // Cached favorites come first: offline, and online too (they start at once).
        cache.favorites = listOf(trackModel(1)); cache.local[1] = tmp("1.m4a")
        for (online in listOf(false, true)) {
            network.isOnline = online
            engine.shuffleFavorites()
            assertEquals("no server call when favorites are cached (online: $online)", 1, api.randomFavoritesCalls.size)
            assertEquals(MediaSource.File(tmp("1.m4a")), backend.loads.last().src)
        }
    }

    @Test fun episodeActiveOfflinePlaysTheMusicQueueFromTheCache() = engineTest {
        saved(PlaybackQueue(listOf(track(7)), 0, 5_000, QueueSource.list),
            PlaybackQueue(listOf(episode("abcdefghijk")), 0, 0, QueueSource.list), Item.Kind.episode)
        cache.local[7] = tmp("7.m4a"); network.isOnline = false
        makeEngine()
        assertEquals(RemoteCommands.Status.success, handlePlay())
        assertEquals(MediaSource.File(tmp("7.m4a")), backend.loads.last().src)
        assertEquals(5_000L, backend.loads.last().startMs)
        assertEquals(Item.Kind.track, engine.active)
        assertEquals(0, apiCalls)
    }

    @Test fun nothingQueuedOrCachedOfflineSaysWhyAndAsksNoServer() = engineTest {
        network.isOnline = false
        makeEngine()
        assertEquals(RemoteCommands.Status.success, handlePlay())
        idle()
        assertTrue(notices.contains(texts.nothingCachedNotice))
        assertTrue(backend.loads.isEmpty())
        assertEquals(0, apiCalls)
    }

    // MARK: Playback resumption (Android): answered at once from disk

    private fun controller() = MediaSession.ControllerInfo.createTestOnlyControllerInfo(
        "lark.test", 0, 0, 0, 0, false, Bundle.EMPTY, false)

    /** `onPlaybackResumption` resumes from disk synchronously, with no network, and answers the restored queue and position. */
    @Test fun playbackResumptionAnswersFromDiskWithTheRestoredQueue() = engineTest {
        saved(PlaybackQueue(listOf(track(7), track(8)), 1, 42_000, QueueSource.favorites))
        cache.local[8] = tmp("8.m4a"); network.isOnline = false
        makeEngine()
        val context: Context = ApplicationProvider.getApplicationContext()
        val sink = FakeInfoSink()
        val commands = RemoteCommands(scope, engine)
        val rig = SessionPlayerRig(context, commands, sink.hub)
        val host = PlaybackHost(rig.exo, sink.hub, scope, null, sink = sink, clock = clock, artwork = { null }, remote = commands)
        host.engine = engine
        val session = MediaSession.Builder(context, rig.player).build()
        try {
            val future = PlaybackService.SessionCallback(host).onPlaybackResumption(session, controller(), true)
            assertTrue(future.isDone)                                  // answered inside the call, nothing awaited
            val r = future.get()
            assertEquals(listOf("7", "8"), r.mediaItems.map { it.mediaId })
            assertEquals(1, r.startIndex)
            assertEquals(42_000L, r.startPositionMs)
            assertEquals("T8", r.mediaItems[1].mediaMetadata.title.toString())
            assertEquals(MediaSource.File(tmp("8.m4a")), backend.loads.last().src)
            assertTrue(engine.playing)
            assertEquals(0, apiCalls)
        } finally {
            session.release(); commands.detach(); rig.release()
        }
    }

    @Test fun playbackResumptionWithNothingSavedAnswersNoItems() = engineTest {
        network.isOnline = false
        val host = PlaybackHost(androidx.media3.test.utils.TestExoPlayerBuilder(ApplicationProvider.getApplicationContext()).build(),
            lark.service.NowPlayingHub(), scope, null, artwork = { null })
        try {
            val r = PlaybackService.resumed(host)
            assertTrue(r.mediaItems.isEmpty())
            host.engine = engine
            assertTrue(PlaybackService.resumed(host).mediaItems.isEmpty())
        } finally {
            host.engine = null; host.player.release()
        }
    }
}

/** The app graph (iOS `testTheSameCommandsPlayOnTheNewServersEngine`, `testColdLaunchThroughAppServicesPlaysFromDiskWithNoRequest`). */
class ColdStartServicesTest : ServicesTestCase() {
    @Test fun theSameCommandsPlayOnTheNewServersEngine() = servicesTest {
        val second = MockWebServer().apply { start(InetAddress.getByName("localhost"), 0) }
        try {
            val fakeCache = FakeCache(); fakeCache.local[1] = File(dir, "1.m4a"); fakeCache.local[2] = File(dir, "2.m4a")
            val s = makeServices(user = 3, server = null, cache = fakeCache)
            val rc = RemoteCommands(this) { s.engine }                  // as the playback host's `engineProvider`
            assertEquals(RemoteCommands.Status.noActionableNowPlayingItem, rc.playNow())   // no server yet
            s.setServerUrl(serverUrl)
            assertEquals(RemoteCommands.Status.success, rc.playNow())  // the engine is built by the command itself
            val first = s.engine!!
            s.setServerUrl("http://localhost:${second.port}/")
            val next = s.engine!!
            assertNotSame(first, next)
            next.handle(q(listOf(2), 0, 0, false))
            assertEquals(RemoteCommands.Status.success, rc.playNow())
            assertEquals(MediaSource.File(File(dir, "2.m4a")), backend.loads.last().src)
            assertTrue(next.playing)
            assertEquals(RemoteCommands.Status.success, rc.pauseNow())
            assertFalse(next.playing)
        } finally {
            second.shutdown()
        }
    }

    /**
     * The resumption hook builds the engine when there is none yet (a cold start, or right after a server change), as
     * the commands do: the saved queue plays from disk, and after a change the new server's engine is the one resumed.
     */
    @Test fun playbackResumptionBuildsTheEngineFirst() = servicesTest {
        val data = File(dir, "data")
        val saved = QueueStore(data); saved.user = 3
        saved.save(QueueSnapshot(music = PlaybackQueue(listOf(track(7), track(8)), 1, 42_000, QueueSource.favorites),
            episodes = PlaybackQueue.empty, active = Item.Kind.track, savedAt = 0))
        val fakeCache = FakeCache(); fakeCache.local[8] = File(dir, "8.m4a")
        val exo = androidx.media3.test.utils.TestExoPlayerBuilder(context).build()
        val host = PlaybackHost(exo, lark.service.NowPlayingHub(), this, null, artwork = { null })
        val second = MockWebServer().apply { start(InetAddress.getByName("localhost"), 0) }
        try {
            val s = makeServices(user = 3, cache = fakeCache, dataDirectory = data, onEngine = { host.engine = it })
            host.engineProvider = { s.engine }
            assertTrue(host.engine == null)                            // nothing built it yet
            host.resumption()
            val first = host.engine!!
            assertTrue(first === s.engine)
            assertEquals(MediaSource.File(File(dir, "8.m4a")), backend.loads.last().src)
            assertEquals(42_000L, backend.loads.last().startMs)
            s.setServerUrl("http://localhost:${second.port}/")
            assertTrue(host.engine == null)                            // the old server's engine is gone
            host.resumption()
            assertTrue(host.engine != null && host.engine !== first && host.engine === s.engine)
        } finally {
            host.engine = null; exo.release(); second.shutdown()
        }
    }

    /**
     * The whole app graph, as after Android started Melarka in the background for the car: the services from the
     * preferences and the secret store, the engine built, the queue from disk, the file from the cache, and no request.
     */
    @Test fun coldLaunchThroughAppServicesPlaysFromDiskWithNoRequest() = servicesTest {
        handler = { status(500) }
        val data = File(dir, "data")
        val saved = QueueStore(data); saved.user = 3
        saved.save(QueueSnapshot(music = PlaybackQueue(listOf(track(7), track(8)), 1, 42_000, QueueSource.favorites),
            episodes = PlaybackQueue.empty, active = Item.Kind.track, savedAt = 0))
        val offline = FakeNetwork(); offline.isOnline = false
        val fakeCache = FakeCache(); fakeCache.local[8] = File(dir, "8.m4a")
        val s = makeServices(user = 3, cache = fakeCache, dataDirectory = data, network = offline)
        s.start()
        assertEquals(RemoteCommands.Status.success, RemoteCommands.handlePlay(s.engine!!, this))
        assertEquals(MediaSource.File(File(dir, "8.m4a")), backend.loads.last().src)
        assertEquals(42_000L, backend.loads.last().startMs)
        assertTrue(backend.loads.last().autoplay)
        settle()
        assertTrue(requests.isEmpty())
    }
}

/**
 * The media service on a cold start (Android): a media button with the app not running starts it with
 * `startForegroundService`, so it must be in the foreground within 5 s even when nothing can play.
 */
@UnstableApi
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ColdStartServiceTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var rig: ServiceRig
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    private val started = mutableListOf<org.robolectric.android.controller.ServiceController<PlaybackService>>()

    @Before fun setUp() { rig = ServiceRig(context, tmp.newFolder("rig")) }
    @After fun tearDown() {
        started.filter { it.get().session != null }.forEach { it.destroy() }
        rig.release()
    }

    private fun start(intent: Intent) =
        Robolectric.buildService(PlaybackService::class.java, intent).also { started.add(it) }.create().startCommand(0, 1)

    private fun mediaButtonPlay() = Intent(Intent.ACTION_MEDIA_BUTTON)
        .putExtra(Intent.EXTRA_KEY_EVENT, KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_PLAY))

    private fun idleMain() = shadowOf(Looper.getMainLooper()).idle()

    private companion object {
        /** The cold start's notification uses Media3's id, so Media3's media notification replaces it. */
        const val HOLD_ID = androidx.media3.session.DefaultMediaNotificationProvider.DEFAULT_NOTIFICATION_ID
    }

    /** No engine (no server yet): the notification is posted inside the start, then removed, and the service stops. */
    @Test fun mediaButtonWithNoEngineStillGoesIntoTheForeground() {
        rig.host.engine = null
        val controller = start(mediaButtonPlay())
        val shadow = shadowOf(controller.get())
        // Into the foreground inside the start (Robolectric runs what the start posted right after it, so the
        // notification is gone again by now: its id and the stop say it was there).
        assertEquals(HOLD_ID, shadow.lastForegroundNotificationId)
        idleMain()
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }

    /** Offline with nothing on the phone: the play says why, nothing plays, and the foreground is let go the same way. */
    @Test fun mediaButtonWithNothingPlayableLetsTheForegroundGo() {
        rig.network.isOnline = false
        val controller = start(mediaButtonPlay())
        val shadow = shadowOf(controller.get())
        assertEquals(HOLD_ID, shadow.lastForegroundNotificationId)
        idleMain()
        assertFalse(rig.engine.playing)
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }

    /** A saved queue whose track is on the phone: it plays, and the service stays (its media notification takes over). */
    @Test fun mediaButtonWithACachedQueuePlaysAndKeepsTheService() {
        rig.cache.local[1] = lark.testing.silentWav(tmp.root, 30.0, 8000)
        rig.engine.handle(q(listOf(1), 0, 0, false))
        rig.makeEngine()                                                // a relaunch: restored, nothing loaded
        val controller = start(mediaButtonPlay())
        val shadow = shadowOf(controller.get())
        assertNotNull(shadow.lastForegroundNotification)
        idleMain()
        assertTrue(rig.engine.playing)
        rig.waitUntil { rig.player.isPlaying }
        rig.settle()
        assertFalse(shadow.isStoppedBySelf)
        assertFalse(shadow.isForegroundStopped)
    }

    /** A shuffle from the server still running keeps the cold-start notification; its failure lets it go. */
    @Test fun aShuffleStillRunningKeepsTheForeground() {
        val gate = kotlinx.coroutines.CompletableDeferred<Unit>()
        rig.api.randomFavoritesGate = gate
        rig.api.refillError = java.net.SocketTimeoutException()
        val controller = start(mediaButtonPlay())
        val shadow = shadowOf(controller.get())
        idleMain()
        assertTrue(rig.remote.starting)
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(20))
        assertFalse(shadow.isForegroundStopped)
        assertFalse(shadow.isStoppedBySelf)
        gate.complete(Unit)                                             // the server could not be reached
        assertFalse(rig.remote.starting)
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(2))
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }

    /**
     * A shortcut's or car autoplay's run still preparing playback (its shuffle asking the server after the shortcut's
     * activity has gone) keeps the foreground too; when it ends with nothing playing, it is let go.
     */
    @Test fun anIntentRunStillPreparingKeepsTheForeground() {
        var preparing = true
        rig.host.intentStarting = { preparing }
        val intent = Intent(context, PlaybackService::class.java).putExtra(PlaybackService.EXTRA_FOREGROUND_START, true)
        val controller = start(intent)
        val shadow = shadowOf(controller.get())
        idleMain()
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(20))
        assertFalse(shadow.isForegroundStopped)
        assertFalse(shadow.isStoppedBySelf)
        preparing = false                                               // the run ended, nothing plays
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(2))
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }

    /**
     * The shortcut's activity runs in a task of its own, which goes as it finishes: Android tells the service its task
     * was removed. That is not the user swiping Melarka away: the playback the shortcut is starting goes on (Media3's
     * default would pause it and stop, as the session does not show it as playing yet). Melarka's own task swiped away
     * with nothing playing still stops the service.
     */
    @Test fun theShortcutsTaskGoingDoesNotStopTheService() {
        var preparing = true
        rig.host.intentStarting = { preparing }
        val intent = Intent(context, PlaybackService::class.java).putExtra(PlaybackService.EXTRA_FOREGROUND_START, true)
        val controller = start(intent)
        val shadow = shadowOf(controller.get())
        idleMain()
        controller.get().onTaskRemoved(Intent(ShortcutActivity.ACTION_SHUFFLE_FAVORITES).setClassName(context, ShortcutActivity::class.java.name))
        idleMain()
        assertFalse(shadow.isStoppedBySelf)
        assertFalse(shadow.isForegroundStopped)

        preparing = false
        controller.get().onTaskRemoved(Intent(Intent.ACTION_MAIN).setClassName(context, lark.app.MainActivity::class.java.name))
        idleMain()                                                      // no time passes: the hold's check is not due
        // Media3's task-removed stop, not the hold letting go (that would have stopped the foreground first).
        assertTrue(shadow.isStoppedBySelf)
        assertFalse(shadow.isForegroundStopped)
    }

    /** Waiting for the network's first answer keeps it too, but only up to the 30 s cap. */
    @Test fun waitingForTheNetworkKeepsTheForegroundUpToTheCap() {
        rig.network.answered = false
        val controller = start(mediaButtonPlay())
        val shadow = shadowOf(controller.get())
        idleMain()
        assertTrue(rig.engine.awaitingNetworkAnswer)
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(PlaybackService.HOLD_MAX_MS - 2_000))
        assertFalse(shadow.isForegroundStopped)
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(3))
        assertTrue(rig.engine.awaitingNetworkAnswer)                   // still no answer: the cap let it go
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }

    private fun mediaButton(keyCode: Int) = Intent(Intent.ACTION_MEDIA_BUTTON)
        .putExtra(Intent.EXTRA_KEY_EVENT, KeyEvent(KeyEvent.ACTION_DOWN, keyCode))

    private fun isHold(n: android.app.Notification?) =
        n?.extras?.getCharSequence(android.app.Notification.EXTRA_TEXT)?.toString() == context.getString(lark.app.R.string.cold_start_notification)

    /**
     * Media3's own notification buttons (Android 8-12) reach the running service as `MEDIA_BUTTON` starts: that is no
     * cold start. Paused (past Media3's foreground timeout), Previous (a paused restart) and Play neither show the cold-start notification nor stop the
     * service; the session stays.
     */
    @Test fun notificationButtonsOnARunningServiceAreNoColdStart() {
        rig.cache.local[1] = lark.testing.silentWav(tmp.root, 30.0, 8000)
        rig.engine.handle(q(listOf(1), 0, 0, false))
        rig.makeEngine()
        val controller = start(mediaButtonPlay())                       // the real cold start
        val shadow = shadowOf(controller.get())
        idleMain()
        rig.waitUntil { rig.player.isPlaying }
        rig.settle()
        rig.engine.pause()
        rig.settle()
        // Paused long enough for Media3 to let the foreground go (its 10 min timeout): its notification stays.
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMinutes(11))
        assertFalse(rig.engine.playing)
        assertFalse(controller.get().isPlaybackOngoing)
        assertFalse("still running, paused", shadow.isStoppedBySelf)
        val before = shadow.lastForegroundNotification

        controller.get().onStartCommand(mediaButton(KeyEvent.KEYCODE_MEDIA_PREVIOUS), 0, 2)
        assertTrue("no cold-start notification for Previous", shadow.lastForegroundNotification === before ||
            !isHold(shadow.lastForegroundNotification))
        rig.settle()
        shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(3))
        assertFalse(rig.engine.playing)
        assertFalse(shadow.isStoppedBySelf)
        assertNotNull(controller.get().session)

        val beforePlay = shadow.lastForegroundNotification
        controller.get().onStartCommand(mediaButton(KeyEvent.KEYCODE_MEDIA_PLAY), 0, 3)
        assertTrue("no cold-start notification for Play", shadow.lastForegroundNotification === beforePlay ||
            !isHold(shadow.lastForegroundNotification))
        idleMain()
        assertFalse(shadow.isStoppedBySelf)
        assertNotNull(controller.get().session)
    }

    /**
     * A plain start (the engine's `PlaybackService.start`) can reach a new service first while car autoplay's
     * `startForegroundService` is on its way: the foreground start still gets the hold, as Android needs
     * `startForeground` within 5 s of it.
     */
    @Test fun aForegroundStartAfterAPlainFirstStartStillHolds() {
        rig.host.engine = null
        val controller = start(Intent(context, PlaybackService::class.java))
        val shadow = shadowOf(controller.get())
        idleMain()
        assertEquals(null, shadow.lastForegroundNotification)
        controller.get().onStartCommand(Intent(context, PlaybackService::class.java).putExtra(PlaybackService.EXTRA_FOREGROUND_START, true), 0, 2)
        assertEquals(HOLD_ID, shadow.lastForegroundNotificationId)
        idleMain()
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }

    /** Car autoplay's start ([PlaybackService.startInForeground]) holds the foreground the same way. */
    @Test fun aForegroundStartWithNothingToPlayStops() {
        rig.host.engine = null
        val intent = Intent(context, PlaybackService::class.java).putExtra(PlaybackService.EXTRA_FOREGROUND_START, true)
        val controller = start(intent)
        val shadow = shadowOf(controller.get())
        assertEquals(HOLD_ID, shadow.lastForegroundNotificationId)
        idleMain()
        assertTrue(shadow.isForegroundStopped)
        assertTrue(shadow.isStoppedBySelf)
    }
}
