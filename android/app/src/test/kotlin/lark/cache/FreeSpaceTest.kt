package lark.cache

import android.content.Context
import android.os.storage.StorageManager
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.Implementation
import org.robolectric.annotation.Implements
import java.io.File
import java.io.IOException
import java.util.UUID

/** The framework's StorageManager answers what the test says (Robolectric has no allocatable-bytes shadow). */
@Implements(StorageManager::class)
class ShadowAllocatable {
    @Implementation fun getUuidForPath(path: File): UUID { askedPath = path; return uuid }
    @Implementation fun getAllocatableBytes(id: UUID): Long {
        asked = id
        failure?.let { throw it }
        return bytes
    }

    companion object {
        val uuid: UUID = UUID.fromString("00000000-0000-0000-0000-000000000042")
        var askedPath: File? = null
        var asked: UUID? = null
        var bytes = 0L
        var failure: Exception? = null
        fun reset() { askedPath = null; asked = null; bytes = 0; failure = null }
    }
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], shadows = [ShadowAllocatable::class])
class FreeSpaceTest {
    @get:Rule val folder = TemporaryFolder()
    private val context: Context = ApplicationProvider.getApplicationContext()

    @Before fun setUp() = ShadowAllocatable.reset()

    @Test fun asksForTheVolumeOfTheNearestExistingParent() {
        ShadowAllocatable.bytes = 123_456_789L
        val dir = File(folder.root, "lark/abcd/5")                          // none of it exists yet
        assertEquals(123_456_789L, StorageManagerFreeSpace(context, dir).bytes())
        assertEquals(folder.root, ShadowAllocatable.askedPath)
        assertEquals(ShadowAllocatable.uuid, ShadowAllocatable.asked)
    }

    @Test fun anExistingFolderIsAskedAboutItself() {
        File(folder.root, "lark").mkdirs()
        ShadowAllocatable.bytes = 7
        assertEquals(7L, StorageManagerFreeSpace(context, File(folder.root, "lark")).bytes())
        assertEquals(File(folder.root, "lark"), ShadowAllocatable.askedPath)
    }

    @Test fun anErrorMeansUnknownNotZero() {
        ShadowAllocatable.failure = IOException("no such volume")
        assertNull(StorageManagerFreeSpace(context, File(folder.root, "x")).bytes())
        ShadowAllocatable.failure = IllegalStateException("boom")
        assertNull(StorageManagerFreeSpace(context, File(folder.root, "x")).bytes())
    }
}
