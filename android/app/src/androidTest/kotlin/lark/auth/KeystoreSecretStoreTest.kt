package lark.auth

import androidx.test.ext.junit.runners.AndroidJUnit4
import lark.Instrumented
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.security.KeyStore

/**
 * The real Android Keystore (Robolectric has none): a first write, a read after a restart (a new store on the same
 * file), a file that was deleted or damaged reading as empty and taking new writes, a delete that still erases when
 * the file cannot be rewritten, and a write that cannot be saved leaving the old value.
 */
@RunWith(AndroidJUnit4::class)
class KeystoreSecretStoreTest {
    private val dir = File(Instrumented.context.cacheDir, "keystore-test")
    private val file = File(dir, "secrets.bin")
    private val alias = "lark_secrets_test"

    @Before fun setUp() { dir.deleteRecursively(); dropKey() }
    @After fun tearDown() { dir.deleteRecursively(); dropKey() }

    private fun dropKey() = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(alias)
    private fun store() = KeystoreSecretStore(file, alias)

    @Test fun firstWriteThenReadAfterRestart() {
        val s = store()
        assertNull(s.get("token"))
        s.set("token", "tok-1")
        s.set("userId", "7")
        assertTrue(file.exists())
        assertFalse("the file is ciphertext", String(file.readBytes(), Charsets.ISO_8859_1).contains("tok-1"))

        val restarted = store()
        assertEquals("tok-1", restarted.get("token"))
        assertEquals("7", restarted.get("userId"))
    }

    @Test fun deletedFileReadsEmptyAndTakesNewWrites() {
        store().set("token", "tok-1")
        assertTrue(file.delete())
        val s = store()
        assertNull(s.get("token"))
        s.set("token", "tok-2")
        assertEquals("tok-2", store().get("token"))
    }

    @Test fun damagedFileReadsEmptyAndTakesNewWrites() {
        store().set("token", "tok-1")
        val raw = file.readBytes()
        raw[raw.size - 1] = (raw[raw.size - 1].toInt() xor 0x55).toByte()      // the GCM tag no longer matches
        file.writeBytes(raw)
        val s = store()
        assertNull(s.get("token"))
        s.set("token", "tok-2")
        assertEquals("tok-2", store().get("token"))

        file.writeBytes(byteArrayOf(1, 2, 3))                                    // shorter than an IV
        assertNull(store().get("token"))
    }

    @Test fun keyGoneReadsEmpty() {
        store().set("token", "tok-1")
        dropKey()
        val s = store()
        assertNull(s.get("token"))
        s.set("token", "tok-2")
        assertEquals("tok-2", store().get("token"))
    }

    @Test fun deleteStillErasesWhenTheFileCannotBeRewritten() {
        val s = store()
        s.set("token", "tok-1")
        s.set("userId", "7")
        // The rewrite goes through `secrets.bin.tmp`: a directory there makes it fail.
        assertTrue(File(file.path + ".tmp").mkdirs())
        s.set("token", null)
        assertFalse("the ciphertext file is deleted instead", file.exists())
        assertNull(store().get("token"))
        assertNull(s.get("token"))
    }

    /** A write that cannot be saved is not kept in memory either: neither read back nor persisted by the next write. */
    @Test fun aFailedWriteLeavesTheOldValue() {
        val s = store()
        s.set("token", "tok-1")
        val tmp = File(file.path + ".tmp")
        assertTrue(tmp.mkdirs())                                                 // the rewrite fails
        assertTrue(runCatching { s.set("token", "tok-2") }.isFailure)
        assertTrue(runCatching { s.set("userId", "7") }.isFailure)
        assertEquals("tok-1", s.get("token"))
        assertNull(s.get("userId"))
        assertTrue(tmp.delete())
        s.set("server", "x")                                                     // the next save that works
        val restarted = store()
        assertEquals("tok-1", restarted.get("token"))
        assertNull(restarted.get("userId"))
        assertEquals("x", restarted.get("server"))
    }

    @Test fun deletingAMissingKeyWritesNothing() {
        val s = store()
        s.set("token", null)
        assertFalse(file.exists())
    }
}
