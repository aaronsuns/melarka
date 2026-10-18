package lark.player

import kotlinx.coroutines.test.TestScope
import lark.api.RandomFavorites
import lark.bridge.WebMessage
import lark.service.RemoteCommands
import lark.testing.EngineTestCase
import lark.testing.q
import lark.testing.trackModel
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/ReconnectEngineTests.swift (the engine cases; `PageLoadFailureTests` in the same iOS file
 * belong to the app services, Task 10): the engine across sign-outs, reconnects and cold starts, and the shuffle
 * shortcut while playing.
 */
class ReconnectEngineTest : EngineTestCase() {
    /** The shuffle's own server call (a refill asks for 20, the shuffle for SHUFFLE_SIZE). */
    private val shuffleCalls get() = api.randomFavoritesCalls.count { it.n == PlayerEngine.SHUFFLE_SIZE }

    /** What the media session's play command runs: iOS `RemoteCommands.handlePlay`. */
    private fun TestScope.handlePlay() { RemoteCommands.handlePlay(engine, this) }

    /** iOS `IntentRun.shuffleFavorites` (the shortcut, Task 11): a no-op while playing, else focus, then the shuffle. */
    private suspend fun shuffleIntent(): String {
        if (engine.playing) return "alreadyPlaying"
        engine.intentBlocker(resume = false)?.let { return it.name }
        engine.prepareForPlayback()
        engine.shuffleFavorites()
        return if (engine.playing) "started" else "nothingToPlay"
    }

    // MARK: 5. `greeted`

    /** After a sign-out (or a user switch) the remounted page's `hello` is honoured again. */
    @Test fun helloIsHonouredAgainAfterASignOut() = engineTest {
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(1, api.queueCalls)
        engine.handle(WebMessage.Hello("resume"))          // a web reload: state only
        idle()
        assertEquals(1, api.queueCalls)
        engine.reset()                                    // sign-out, or another user
        engine.handle(WebMessage.Hello("resume"))
        idle()
        assertEquals(2, api.queueCalls)
    }

    // MARK: 6. No replay after reconnect

    @Test fun aQueueThatEndsOfflineDoesNotReplayOnReconnect() = engineTest {
        cache.local[5] = tmp("5.m4a")
        engine.handle(q(listOf(5), 0, 0, true))
        backend.start()
        network.isOnline = false
        backend.finish()
        assertTrue(!engine.playing)
        val loads = backend.loads.size
        network.isOnline = true
        assertEquals(loads, backend.loads.size)        // the finished track is not played again
        assertTrue(!engine.playing)
    }

    @Test fun anEndOfflineWithOnlyStreamsLeftGoesOnToTheNextOnReconnect() = engineTest {
        cache.local[5] = tmp("5.m4a")
        engine.handle(q(listOf(5, 6), 0, 0, true))
        backend.start()
        network.isOnline = false
        backend.finish()                                  // 6 is not on the phone: stopped
        assertTrue(!engine.playing)
        assertEquals(1, backend.loads.size)
        network.isOnline = true
        assertEquals(remote(6), backend.loads.last().src)  // the next one, not 5 again
        assertEquals("6", engine.current?.id)
        assertTrue(engine.playing)
    }

    @Test fun anInterruptedStreamStillResumesOnReconnect() = engineTest {
        engine.handle(q(listOf(5), 0, 0, true))
        backend.start()
        backend.playTo(10_000)
        network.isOnline = false
        backend.fail(network = true)                       // the stream broke: nothing local
        assertTrue(!engine.playing)
        network.isOnline = true
        assertEquals(remote(5), backend.loads.last().src)
        assertTrue(engine.playing)
    }

    // MARK: 7. Cold offline start, nothing on the phone

    @Test fun coldStartBeforeTheNetworkAnswersWaitsAndTakesNoFocus() = engineTest {
        network.answered = false
        makeEngine()
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        handlePlay()
        idle()
        assertEquals(0, activations)                    // the car's own source keeps playing
        assertEquals(0, api.randomFavoritesCalls.size)
        assertTrue(backend.loads.isEmpty())
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(4), trackModel(5)))
        network.answer(online = true)                      // the network turns out to be there
        idle()
        assertEquals(1, shuffleCalls)
        assertTrue(engine.playing)
        assertEquals(1, activations)
    }

    @Test fun coldStartOfflineWithNothingCachedResumesWhenTheNetworkReturns() = engineTest {
        network.isOnline = false
        makeEngine()
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        handlePlay()
        idle()
        assertEquals(0, activations)
        assertTrue(notices.contains(texts.nothingCachedNotice))
        assertEquals(0, api.randomFavoritesCalls.size)
        api.randomFavoritesAnswer = RandomFavorites("favorites", listOf(trackModel(4)))
        network.isOnline = true
        idle()
        assertEquals(1, shuffleCalls)
        assertTrue(engine.playing)
    }

    @Test fun aWaitingColdStartIsDroppedByAnythingElse() = engineTest {
        network.isOnline = false
        makeEngine()
        handlePlay()
        engine.handle(q(listOf(7), 0, 0, false))   // the user chose something meanwhile
        engine.pause()
        network.isOnline = true
        idle()
        assertEquals(0, api.randomFavoritesCalls.size)
    }

    // MARK: 8. The shuffle shortcut does not cut the car's resume

    @Test fun shuffleIntentIsANoOpWhilePlaying() = engineTest {
        cache.favorites = listOf(trackModel(1)); cache.local[1] = tmp("1.m4a"); cache.local[5] = tmp("5.m4a")
        engine.handle(q(listOf(5), 0, 0, true))
        val loads = backend.loads.size
        var activations = 0
        engine.willStartPlayback = { activations += 1 }
        assertEquals("alreadyPlaying", shuffleIntent())
        assertEquals(loads, backend.loads.size)
        assertEquals(0, activations)
        assertEquals("5", engine.current?.id)
        engine.pause()
        assertEquals("started", shuffleIntent())
        assertEquals("1", engine.current?.id)
    }
}
