package lark.player

import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.episode
import lark.testing.q
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/LookaheadTests.swift: the engine hands the next 2 tracks to the cache once the current one
 * sounds, and touches what it plays from the cache.
 */
class LookaheadTest : EngineTestCase() {
    @Test fun nextTwoArePrefetchedWhenTheTrackStartsSounding() = engineTest {
        engine.handle(q(listOf(1, 2, 3, 4), 0, 0, true))
        assertEquals(emptyList<List<Long>>(), cache.prefetched)            // still buffering: the current stream goes first
        backend.start()
        assertEquals(listOf(listOf(2L, 3L)), cache.prefetched)
        backend.finish()                                 // 2 loads; once it sounds, 3 and 4
        assertEquals(listOf(listOf(2L, 3L)), cache.prefetched)
        backend.start()
        assertEquals(listOf(listOf(2L, 3L), listOf(3L, 4L)), cache.prefetched)
    }

    @Test fun restoredQueueThatIsNotPlayingPrefetchesNothing() = engineTest {
        engine.handle(q(listOf(1, 2, 3), 0, 0, false))
        assertTrue(cache.prefetched.isEmpty())
    }

    @Test fun lastTrackPrefetchesNothing() = engineTest {
        engine.handle(q(listOf(1), 0, 0, true))
        backend.start()
        assertTrue(cache.prefetched.isEmpty())
    }

    @Test fun editWhileSoundingPrefetchesTheNewNext() = engineTest {
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.start()
        engine.handle(q(listOf(1, 7, 8, 2, 3), 0, null, true))   // enqueue next
        assertEquals(listOf(7L, 8L), cache.prefetched.last())
    }

    @Test fun episodesArePrefetchedNever() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.episode, listOf(episode("aaaaaaaaaaa"), episode("bbbbbbbbbbb")), 0, 0, true, QueueSource.list))
        backend.start()
        assertTrue(cache.prefetched.isEmpty())
    }

    @Test fun playFromTheCacheTouchesIt() = engineTest {
        cache.local[1] = tmp("1.m4a")
        engine.handle(q(listOf(1, 2), 0, 0, true))
        assertEquals(MediaSource.File(tmp("1.m4a")), backend.loads.last().src)
        assertEquals(listOf(1L), cache.touched)
        backend.finish()                                 // 2 streams: nothing to touch
        assertEquals(listOf(1L), cache.touched)
    }

    /** The engine is one observer among several: the cache wiring adds its own without replacing it. */
    @Test fun engineObservesTheNetworkWithoutTakingItOver() = engineTest {
        var other = 0
        network.observe { other += 1 }
        network.isOnline = false
        engine.handle(q(listOf(1), 0, 0, true))
        backend.fail(network = true)                      // offline, nothing on the phone: stopped for the network
        val loads = backend.loads.size
        network.isOnline = true                          // both observers run
        assertEquals(2, other)
        assertEquals(loads + 1, backend.loads.size)   // the engine started again
        assertTrue(engine.playing)
    }
}
