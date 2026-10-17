package lark.bridge

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The expected strings in `resources/bridge` are hand-written from iOS's NativeEvent.swift encoding rules and its tests. */
class NativeEventTest {
    private fun fixture(name: String): String =
        javaClass.getResource("/bridge/$name")!!.readText().removeSuffix("\n")

    private val item = Item(Item.Kind.track, "7", "晴天", "周杰伦", "叶惠美", 269000,
        lenientJson.parseToJsonElement("""{"id":7,"favorite":true,"gain":-3.5,"lyrics":null,"nested":{"a":[1,2.5,"x",false]}}"""))

    @Test fun stateWithNullsEncodesExplicitNulls() {
        val json = NativeEvent.State(Item.Kind.episode, null, 0, false, 10, 20, true, null, 1.25).toJson()
        assertEquals(fixture("state_nulls.json"), json)
        assertTrue(json.contains("\"itemId\":null,"))
        assertTrue(json.contains("\"error\":null,"))
        assertTrue(json.endsWith("\"sleepRemainingMs\":null}"))
    }

    @Test fun stateCarriesModesAndSleepRemaining() {
        val json = NativeEvent.State(Item.Kind.track, "7", 2, true, 1500, 269000, false, "offline", 1.0,
            shuffle = true, repeat = RepeatMode.all, sleepRemainingMs = 61_000).toJson()
        assertEquals(fixture("state_full.json"), json)
        assertFalse(json.contains("repeatMode"))
    }

    @Test fun anEpisodeStateSaysNoModes() {
        val json = NativeEvent.State(Item.Kind.episode, null, 0, false, 0, 0, false, null, 1.0).toJson()
        assertTrue(json.contains("\"shuffle\":false,\"repeat\":\"off\""))
    }

    @Test fun nonFiniteRateIsSentAsOne() {
        val json = NativeEvent.State(Item.Kind.track, "7", 0, true, 0, 1, false, null, Double.NaN).toJson()
        assertTrue(json.contains("\"rate\":1,"))
    }

    @Test fun queueKeepsMetaUnchanged() {
        assertEquals(fixture("queue.json"), NativeEvent.Queue(Item.Kind.track, listOf(item), 0, QueueSource.radio).toJson())
    }

    @Test fun smallEvents() {
        assertEquals(fixture("flushed.json"), NativeEvent.Flushed("f1").toJson())
        assertEquals(fixture("auth_required.json"), NativeEvent.AuthRequired.toJson())
        assertEquals(fixture("notice.json"), NativeEvent.Notice("离线").toJson())
    }

    @Test fun dispatchScriptWrapsTheEvent() {
        assertEquals("window.dispatchEvent(new CustomEvent(\"lark-native\",{detail:${fixture("flushed.json")}}))",
            dispatchScript(NativeEvent.Flushed("f1")))
    }

    @Test fun dispatchScriptEscapesLineSeparatorsAndScriptEnd() {
        val js = dispatchScript(NativeEvent.Notice("a b c</script>\"\\\n"))
        assertEquals(fixture("notice_escaped_script.json"), js)
        assertFalse(js.contains(" ")); assertFalse(js.contains(" ")); assertFalse(js.contains("</"))
        assertTrue(js.startsWith("window.dispatchEvent(new CustomEvent(\"lark-native\""))
    }

    @Test fun nonFiniteNumbersInMetaAreStrings() {
        val meta = JsonObject(mapOf("a" to JsonPrimitive(Double.NaN), "b" to JsonPrimitive(Double.POSITIVE_INFINITY),
            "c" to JsonPrimitive(Double.NEGATIVE_INFINITY), "d" to JsonArray(listOf(JsonPrimitive(1.5), JsonNull))))
        val js = dispatchScript(NativeEvent.Queue(Item.Kind.track, listOf(item.copy(meta = meta)), 0, QueueSource.list))
        assertTrue(js, js.contains("\"meta\":{\"a\":\"NaN\",\"b\":\"Infinity\",\"c\":\"-Infinity\",\"d\":[1.5,null]}"))
    }

    @Test fun controlCharactersAreEscaped() {
        assertEquals("{\"type\":\"notice\",\"text\":\"\\u0001\\t\\b\\f\"}", NativeEvent.Notice("\u0001\t\b\u000C").toJson())
    }
}
