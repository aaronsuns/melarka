package lark.player

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.QueueSource
import lark.bridge.RepeatMode
import lark.bridge.lenientJson
import lark.testing.ids
import lark.testing.pq
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test
import kotlin.random.Random

/**
 * Port of PlayModesTests: the same fixtures as the web's `web/src/player/modes.test.ts`, with the same results.
 * (The web's fixtures are inline in that test file; there is no modes*.json to read.)
 */
class PlayModesTest {
    private fun s(vararg xs: Int) = xs.map { it.toString() }
    /** A repeatable "random" source: the given values, round and round. */
    private fun seq(vararg xs: Double): () -> Double { var i = 0; return { xs[i++ % xs.size] } }

    @Test fun shuffleKeepsTheCurrentItemAndPermutesOnlyWhatFollows() {
        val (out, original) = PlayModes.shuffleUpcoming(pq(listOf(1, 2, 3, 4, 5), 1), seq(0.1, 0.9, 0.5))
        assertEquals(s(1, 2), out.ids().take(2))
        assertEquals(1, out.index)
        assertEquals(s(3, 4, 5), out.ids().drop(2).sorted())
        assertEquals(s(3, 4, 5), original)
        assertEquals(QueueSource.list, out.source)
    }

    @Test fun unshuffleRestoresTheOrderPlayNextStaysInFrontAppendedAtTheEnd() {
        val shuffled = pq(listOf(1, 9, 5, 3, 4, 7), 0)   // 9 = play next, 7 = add to queue, original [3,4,5]
        assertEquals(s(1, 9, 3, 4, 5, 7), PlayModes.unshuffle(shuffled, s(3, 4, 5)).ids())
    }

    @Test fun unshuffleDropsRemovedAndAlreadyPlayedItems() {
        // Shuffled from [1, 2, 3, 4, 5] at 1; then 4 played (now current) and 5 removed.
        val out = PlayModes.unshuffle(pq(listOf(1, 4, 3, 2), 1), s(2, 3, 4, 5))
        assertEquals(s(1, 4, 2, 3), out.ids())
        assertEquals(1, out.index)
    }

    @Test fun unshuffleMatchesRepeatedIdsAsAMultiset() {
        assertEquals(s(1, 2, 3), PlayModes.unshuffle(pq(listOf(1, 2, 3), 1), s(2, 3, 2)).ids())
        assertEquals(s(1, 3, 2, 2), PlayModes.unshuffle(pq(listOf(1, 2, 3, 2), 0), s(3, 2, 2)).ids())
    }

    @Test fun aShuffledNewPassNeverStartsWithTheItemThatJustEnded() {
        // Without the guard this random source puts 4 (just ended) first: [4, 2, 3, 1].
        val pass = PlayModes.newPass(pq(listOf(1, 2, 3, 4), 3), true, seq(0.0, 0.99, 0.99))
        assertNotEquals("4", pass.items[0].id)
        assertEquals(s(1, 2, 3, 4), pass.ids().sorted())
        repeat(200) {
            assertNotEquals("3", PlayModes.newPass(pq(listOf(1, 2, 3), 2), true) { Random.nextDouble() }.items[0].id)
        }
        assertEquals(s(5), PlayModes.newPass(pq(listOf(5), 0), true, seq(0.0)).ids())   // nothing else to start with
    }

    @Test fun unshuffleWithNothingRememberedKeepsTheCurrentOrder() {
        assertEquals(s(1, 3, 2), PlayModes.unshuffle(pq(listOf(1, 3, 2), 0), emptyList()).ids())
    }

    @Test fun newPassSameOrderFromZeroOrAFreshShuffle() {
        val end = pq(listOf(1, 2, 3, 4), 3, positionMs = 5000)
        val plain = PlayModes.newPass(end, false, seq(0.0))
        assertEquals(s(1, 2, 3, 4), plain.ids())
        assertEquals(0, plain.index)
        assertEquals(0L, plain.positionMs)
        assertEquals(QueueSource.list, plain.source)
        val mixed = PlayModes.newPass(end, true, seq(0.0, 0.0, 0.0))
        assertEquals(0, mixed.index)
        assertEquals(s(1, 2, 3, 4), mixed.ids().sorted())
        assertNotEquals(s(1, 2, 3, 4), mixed.ids())
    }

    // Persistence and the wire name

    @Test fun jsonUsesRepeatAndDecodesLeniently() {
        val modes = PlayModes(shuffle = true, repeatMode = RepeatMode.one, original = listOf("3", "4"))
        val obj = modes.toJson()
        assertEquals(JsonPrimitive("one"), obj["repeat"])
        assertNull(obj["repeatMode"])
        assertEquals(modes, PlayModes.from(lenientJson.parseToJsonElement(obj.toString())))
        assertEquals(PlayModes(), PlayModes.from(lenientJson.parseToJsonElement("""{"shuffle":"yes","repeat":"sometimes","original":"x"}""")))
        assertEquals(PlayModes(repeatMode = RepeatMode.all), PlayModes.from(lenientJson.parseToJsonElement("""{"repeat":"all"}""")))
        assertEquals(PlayModes(), PlayModes.from(lenientJson.parseToJsonElement("""{"original":["a",1]}""")))
    }

    @Test fun noOriginalIsLeftOutOfTheJson() {
        assertEquals(JsonObject(mapOf("shuffle" to JsonPrimitive(false), "repeat" to JsonPrimitive("off"))), PlayModes().toJson())
        assertEquals(JsonArray(listOf(JsonPrimitive("1"))), PlayModes(original = listOf("1")).toJson()["original"])
    }

    // Edits while shuffled (the web provider's forgetOriginal and move)

    @Test fun anInsertedOrRemovedEntryIsForgottenOnce() {
        // add to queue 7 after [1 | 3 4 5] (shuffled from [3 4 5]): 7 is not in the order, nothing changes
        assertEquals(s(3, 4, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(1, 5, 3, 4)), pq(listOf(1, 7, 5, 3, 4))))
        // play next of 4, which had already played (a second copy): that id is forgotten once
        assertEquals(s(3, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(4, 1, 5, 3), 1), pq(listOf(4, 1, 4, 5, 3), 1)))
        // the queue sheet's x on 3
        assertEquals(s(4, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(1, 5, 3, 4)), pq(listOf(1, 5, 4))))
    }

    @Test fun aMovedEntryKeepsItsNewNeighbourOnUnshuffle() {
        // [1 | 5 3 4] shuffled from [3 4 5]; 4 dragged up before 5: in the order it goes before 5
        assertEquals(s(3, 4, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(1, 5, 3, 4)), pq(listOf(1, 4, 5, 3))))
        // 5 dragged to the end: last
        assertEquals(s(3, 4, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(1, 5, 3, 4)), pq(listOf(1, 3, 4, 5))))
        // 3 dragged to just before 5 in [1 | 5 4 3]: before 5
        assertEquals(s(4, 3, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(1, 5, 4, 3)), pq(listOf(1, 3, 5, 4))))
        // moved into the past (before the current item): it leaves the order
        assertEquals(s(3, 5), PlayModes.edited(s(3, 4, 5), pq(listOf(1, 5, 3, 4)), pq(listOf(4, 1, 5, 3), 1)))
        // and unshuffle keeps it next to its new neighbour
        assertEquals(s(1, 4, 3, 5), PlayModes.unshuffle(pq(listOf(1, 3, 5, 4)), s(4, 3, 5)).ids())
    }

    @Test fun anotherQueueLeavesTheOrderAlone() {
        assertEquals(s(3, 4), PlayModes.edited(s(3, 4), pq(listOf(1, 3, 4)), pq(listOf(8, 9))))
        assertEquals(s(3, 4), PlayModes.edited(s(3, 4), pq(listOf(1, 3, 4)), pq(listOf(1, 3, 4))))
    }
}
