package lark.cache

import lark.testing.FakeLarkApi
import lark.testing.FakeNetwork
import lark.testing.FakeScheduler
import lark.testing.FakeClock
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/** Port of CacheWiringTests' per-server tests, and the `UserCache` the engine holds. */
class CacheLayoutTest {
    @get:Rule val folder = TemporaryFolder()

    @Test fun serverKeyNormalisesTheBaseUrl() {
        val k = CacheLayout::serverKey
        assertEquals(k("https://Cache.Lark.test/"), k("https://cache.lark.test"))
        assertNotEquals(k("https://cache.lark.test"), k("https://cache.lark.test:8443"))
        assertNotEquals(k("https://cache.lark.test"), k("https://cache.lark.test/lark"))
        assertNotEquals(k("https://cache.lark.test"), k("http://cache.lark.test"))
        assertEquals(16, k("https://cache.lark.test").length)
        // The first 8 bytes of SHA-256("https://cache.lark.test"), as iOS computes it.
        assertEquals("e2c0d4ee7dcc6a2a".length, k("https://cache.lark.test").length)
        assertTrue(k("https://cache.lark.test").matches(Regex("[0-9a-f]{16}")))
    }

    @Test fun userRootIsServerThenUser() {
        val root = File(folder.root, "lark")
        assertEquals(File(File(root, CacheLayout.serverKey("https://a.test/")), "5"), CacheLayout.userRoot(root, "https://a.test/", 5))
    }

    @Test fun otherServersAndLegacyFoldersAreRemoved() {
        val root = File(folder.root, "lark")
        val keep = File(root, CacheLayout.serverKey("https://a.test/")).also { File(it, "5/files").mkdirs() }
        File(root, "5/files").mkdirs()                                      // the pre-server per-user folder
        File(root, CacheLayout.serverKey("https://b.test/") + "/7/files").mkdirs()
        CacheLayout.pruneOtherServers(root, keep)
        assertEquals(listOf(keep.name), root.list()!!.toList())
        assertTrue(File(keep, "5/files").exists())
    }

    @Test fun userCacheDelegatesAndClosesTheOldStoreOnSwitch() {
        val scope = CoroutineScope(Job())
        fun store(name: String) = CacheStore(File(folder.root, name), FakeLarkApi(), FakeNetwork(), FakeClock(), scope, FakeScheduler())
        val a = store("a"); val b = store("b")
        val f = File(folder.newFolder("s"), "1.m4a").also { it.writeBytes(ByteArray(3)) }
        val t = lark.testing.trackModel(1)
        a.store(1, f, t.track, t.json, true)
        val cache = UserCache()
        assertNull(cache.localFile(1)); assertTrue(cache.cachedFavorites().isEmpty())   // nothing while signed out
        cache.set(a)
        assertNotNull(cache.localFile(1))
        assertEquals(listOf(1L), cache.cachedFavorites().map { it.track.id })
        cache.set(a)
        assertSame(a, cache.store)
        cache.set(b)                                                        // another user: the old store is closed and flushed
        assertNull(cache.localFile(1))
        assertTrue(File(folder.root, "a/index.json").exists())
        assertFalse(File(folder.root, "b/index.json").exists())
        cache.set(null)
        assertNull(cache.store)
        scope.coroutineContext[Job]!!.cancel()
    }
}
