package lark.player

import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.WebMessage
import lark.testing.EngineTestCase
import lark.testing.gainTrack
import lark.testing.q
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Port of ios/Tests/GaplessTests.swift (the engine half; the backend half is Media3BackendTest): the preloaded
 * next item is what the engine loads next, matched on the item id, and a network retry preloads again.
 */
class GaplessTest : EngineTestCase() {
    /**
     * The next track was preloaded as a stream and meanwhile reached the cache: the end of this track still
     * loads the preloaded stream, so the backend takes that item over (gapless) instead of starting the file.
     */
    @Test fun theEndLoadsWhatWasPreloadedEvenOnceCached() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        assertEquals(remote(2), backend.preloads.last())
        cache.local[2] = tmp("2.m4a")                 // the lookahead finished the download
        backend.finish()
        assertEquals(remote(2), backend.loads.last().src)
        assertEquals("2", engine.current?.id)
    }

    /** The user's next goes to the preloaded item too. */
    @Test fun nextLoadsThePreloadedSource() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        cache.local[2] = tmp("2.m4a")
        engine.next()
        assertEquals(remote(2), backend.loads.last().src)
    }

    /** Anything else than the preloaded item loads from its own best source: the file once cached. */
    @Test fun anotherItemUsesTheCache() = engineTest {
        engine.handle(q(listOf(1, 2, 3), 0, 0, true))
        backend.start()
        cache.local[3] = tmp("3.m4a")
        engine.handle(q(listOf(1, 2, 3), 2, 0, true))
        assertEquals(MediaSource.File(tmp("3.m4a")), backend.loads.last().src)
        // A later load of 2 (not the one preloaded after 3) is the file too.
        cache.local[2] = tmp("2.m4a")
        engine.handle(q(listOf(1, 2, 3), 1, 0, true))
        assertEquals(MediaSource.File(tmp("2.m4a")), backend.loads.last().src)
    }

    @Test fun networkRetryPreloadsAgain() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start(); backend.playTo(5_000)
        val preloads = backend.preloads.size
        backend.fail(network = true)
        scheduler.advance(2.0)
        assertEquals(2, backend.loads.size)
        assertEquals(preloads + 1, backend.preloads.size)
        assertEquals(remote(2), backend.preloads.last())
    }

    /**
     * Repeat one: the current item's own source is preloaded, and its natural end loads exactly that source,
     * so the backend takes the preloaded copy over (a gapless loop, nothing fresh fetched).
     */
    @Test fun repeatOnePreloadsTheCurrentItemAndTheLoopAdoptsIt() = engineTest {
        engine.handle(q(listOf(1, 2), 0, 0, true))
        backend.start()
        assertEquals(remote(2), backend.preloads.last())
        engine.handle(WebMessage.SetModes(false, RepeatMode.one))
        assertEquals(remote(1), backend.preloads.last())
        cache.local[1] = tmp("1.m4a")                 // reached the cache meanwhile: the preloaded stream still wins
        val loads = backend.loads.size
        backend.finish()
        assertEquals(loads + 1, backend.loads.size)
        assertEquals(remote(1), backend.loads.last().src)
        assertEquals(0L, backend.loads.last().startMs)
        assertEquals("1", engine.current?.id)
        assertEquals(MediaSource.File(tmp("1.m4a")), backend.preloads.last())   // and the next loop is ready, from the phone now
    }

    /** Repeat all without shuffle: at the last item the first one is preloaded, so the wrap is gapless too. */
    @Test fun repeatAllPreloadsTheFirstItemAtTheEnd() = engineTest {
        engine.handle(WebMessage.SetModes(false, RepeatMode.all))
        engine.handle(q(listOf(1, 2), 1, 0, true))
        assertEquals(remote(1), backend.preloads.last())
        backend.start(); backend.finish()
        assertEquals(remote(1), backend.loads.last().src)
        assertEquals(0, engine.music.index)
    }

    /** Modes off: what is preloaded is exactly as before (nothing after the last item). */
    @Test fun modesOffPreloadNothingAfterTheLastItem() = engineTest {
        engine.handle(q(listOf(1, 2), 1, 0, true))
        assertTrue(backend.preloads.isNotEmpty())
        assertNull(backend.preloads.last())
    }

    /**
     * Two tracks at different levels: the preloaded item got its own gain when it was preloaded, so the
     * change at the end is the backend's alone. The engine sets no volume for it.
     */
    @Test fun thePreloadedItemHasItsOwnGain() = engineTest {
        engine.handle(WebMessage.SetQueue(Item.Kind.track, listOf(gainTrack(1, -2.0), gainTrack(2, -10.0)), 0, 0, true, QueueSource.list))
        backend.start()
        assertEquals(remote(2), backend.preloads.last())
        assertEquals(0.316f, backend.preloadGains.last(), 0.001f)
        backend.finish()
        assertEquals(remote(2), backend.loads.last().src)
        assertEquals(0.316f, backend.gains.last(), 0.001f)    // the same gain: the backend adopts the item as it is
        assertTrue("the master volume is the sleep timer's only", backend.volumes.isEmpty())
        assertTrue(backend.currentGains.isEmpty())
    }
}
