package lark.player

import lark.api.PlayEvent
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.testing.ids
import lark.testing.pq
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/** Port of the store-level QueueStoreTests (the engine-level ones belong to the engine task). */
class QueueStoreTest {
    @get:Rule val tmp = TemporaryFolder()
    private lateinit var dir: File
    private lateinit var store: QueueStore
    private val queueFile get() = File(dir, "queues-1.json")

    @Before fun setUp() { dir = File(tmp.root, "lark"); store = QueueStore(dir).also { it.user = 1 } }

    private fun snapshot(music: List<Int> = listOf(7, 8), modes: PlayModes? = null) = QueueSnapshot(
        music = pq(music, 1, 3000), episodes = PlaybackQueue.empty, active = Item.Kind.track, savedAt = 1700, ownDevice = "device:1", modes = modes)

    private val item = """{"kind":"track","id":"7","title":"T7","artist":"A","album":"B","durationMs":1000,"meta":{"id":7}}"""
    private val q = """{"items":[$item],"index":0,"positionMs":0,"source":"list"}"""
    private val empty = """{"items":[],"index":0,"positionMs":0,"source":"list"}"""
    private fun file(extra: String = "", version: String = """"version":1,""") =
        """{$version"music":$q,"episodes":$empty,"active":"track","savedAt":1$extra}"""

    @Test fun savingWritesThePerUserFileAndLoadsBack() {
        assertFalse(queueFile.exists())
        val s = snapshot(modes = PlayModes(true, RepeatMode.all, listOf("8")))
        store.save(s)
        assertTrue(queueFile.exists())
        assertEquals(s, store.load())
    }

    @Test fun aSnapshotKeepsItemsAndMetaAndAnEpisodeQueue() {
        val ep = Item(Item.Kind.episode, "abcdefghijk", "E", "Chan", "频道", 60_000,
            kotlinx.serialization.json.buildJsonObject { put("video_id", kotlinx.serialization.json.JsonPrimitive("abcdefghijk")) })
        val s = snapshot().copy(episodes = PlaybackQueue(listOf(ep), 0, 70_000, QueueSource.list), active = Item.Kind.episode)
        store.save(s)
        assertEquals(s, store.load())
    }

    @Test fun eachUserHasTheirOwnFile() {
        store.save(snapshot())
        store.user = 2
        assertNull(store.load())
        store.user = 1
        assertEquals(listOf("7", "8"), store.load()?.music?.ids())
    }

    @Test fun aCorruptFileIsIgnored() {
        dir.mkdirs(); queueFile.writeText("{not json")
        assertNull(store.load())
    }

    @Test fun anotherSchemaVersionOrNoneReadsAsNoQueue() {
        dir.mkdirs()
        queueFile.writeText(file(version = """"version":2,"""))
        assertNull(store.load())
        queueFile.writeText(file(version = ""))
        assertNull(store.load())
        queueFile.writeText(file())
        assertNotNull(store.load())
    }

    @Test fun anIncompleteQueueReadsAsNoQueue() {
        dir.mkdirs()
        queueFile.writeText("""{"version":1,"music":$q,"active":"track","savedAt":1}""")
        assertNull(store.load())
        queueFile.writeText("[]")
        assertNull(store.load())
    }

    @Test fun noUserNoFile() {
        store.user = null
        store.save(snapshot())
        store.saveEvents(listOf(PlayEvent("e", 1, 1, 1, false, "high")))
        assertNull(store.load())
        assertTrue(store.loadEvents().isEmpty())
        assertTrue(dir.list().isNullOrEmpty())
    }

    /** A queue file written before shuffle and repeat existed (v0.1.0, no `modes`) loads, with both off. */
    @Test fun aQueueFileWithoutModesLoadsWithModesOff() {
        dir.mkdirs(); queueFile.writeText(file())
        val s = store.load()!!
        assertNull(s.modes)
        assertEquals(listOf("7"), s.music.ids())
    }

    @Test fun modesAreSavedWithTheQueues() {
        store.save(snapshot(modes = PlayModes(repeatMode = RepeatMode.one)))
        assertEquals(PlayModes(repeatMode = RepeatMode.one), store.load()?.modes)
        assertEquals(1, store.load()?.version)
    }

    /** A malformed `modes` field: the modes fall back to off, the saved queue is kept. */
    @Test fun aMalformedModesFieldKeepsTheQueue() {
        dir.mkdirs()
        for (bad in listOf("5", "\"all\"", "[1]")) {
            queueFile.writeText(file(""","modes":$bad"""))
            val s = store.load()!!
            assertEquals(bad, listOf("7"), s.music.ids())
            assertNull(bad, s.modes)
        }
    }

    @Test fun aWriteLeavesNoTempFileAndReplacesTheOldOne() {
        store.save(snapshot(listOf(1)))
        store.save(snapshot(listOf(2, 3)))
        assertEquals(listOf("queues-1.json"), dir.list()!!.toList())
        assertEquals(listOf("2", "3"), store.load()?.music?.ids())
    }

    @Test fun eventsRoundTripAndAnEmptyListDeletesTheFile() {
        val e = PlayEvent("id-1", 7, 1700, 40, false, "high")
        store.saveEvents(listOf(e))
        assertEquals(listOf(e), store.loadEvents())
        store.saveEvents(emptyList())
        assertFalse(File(dir, "events-1.json").exists())
        assertTrue(store.loadEvents().isEmpty())
    }

    @Test fun aCorruptEventsFileReadsAsEmpty() {
        dir.mkdirs()
        File(dir, "events-1.json").writeText("[{\"client_event_id\":1}]")
        assertTrue(store.loadEvents().isEmpty())
        File(dir, "events-1.json").writeText("nope")
        assertTrue(store.loadEvents().isEmpty())
    }

    @Test fun wipeDeletesTheQueuesAndEventsOfTheCurrentUserOnly() {
        store.save(snapshot()); store.saveEvents(listOf(PlayEvent("e", 1, 1, 1, false, "high")))
        store.user = 2; store.save(snapshot())
        store.user = 1; store.wipe()
        assertEquals(listOf("queues-2.json"), dir.list()!!.toList())
    }
}
