package lark.bridge

import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

class WebMessageTest {
    private fun decode(json: String) = WebMessage.decode(json)

    @Test fun decodesSetQueueWithMeta() {
        val q = decode("""{"type":"setQueue","kind":"track","index":1,"positionMs":5000,"play":true,"source":"favorites",
          "items":[{"kind":"track","id":"7","title":"晴天","artist":"周杰伦","album":"叶惠美","durationMs":269000,
                    "meta":{"id":7,"favorite":true,"title":"晴天"}},
                   {"kind":"track","id":"8","title":"稻香","artist":"周杰伦","album":"魔杰座","durationMs":223000,"meta":{"id":8}}]}""")
            as WebMessage.SetQueue
        assertEquals(listOf("7", "8"), q.items.map { it.id }); assertEquals(5000L, q.positionMs); assertEquals(QueueSource.favorites, q.source)
        assertEquals(JsonPrimitive(true), (q.items[0].meta as kotlinx.serialization.json.JsonObject)["favorite"])
        assertEquals(1, q.index); assertEquals(true, q.play)
    }

    @Test fun setQueueWithoutPositionMeansKeep() {
        val q = decode("""{"type":"setQueue","kind":"episode","index":0,"play":false,"source":"list","items":[]}""") as WebMessage.SetQueue
        assertNull(q.positionMs)
    }

    @Test fun unknownTypeIsDropped() { assertNull(decode("""{"type":"rm -rf"}""")) }

    @Test fun authCarriesNoToken() {
        assertEquals(WebMessage.Auth(true, 3), decode("""{"type":"auth","signedIn":true,"userId":3,"token":"x"}"""))
    }

    @Test fun decodesEveryMessage() {
        val t = Item.Kind.track; val e = Item.Kind.episode
        assertEquals(WebMessage.Hello("resume"), decode("""{"type":"hello","onOpen":"resume"}"""))
        assertEquals(WebMessage.Play(e), decode("""{"type":"play","kind":"episode"}"""))
        assertEquals(WebMessage.Pause(null), decode("""{"type":"pause"}"""))
        assertEquals(WebMessage.Pause(t), decode("""{"type":"pause","kind":"track"}"""))
        assertEquals(WebMessage.Next(t), decode("""{"type":"next","kind":"track"}"""))
        assertEquals(WebMessage.Prev(t), decode("""{"type":"prev","kind":"track"}"""))
        assertEquals(WebMessage.Seek(t, 1235), decode("""{"type":"seek","kind":"track","ms":1234.6}"""))
        assertEquals(WebMessage.Seek(t, 1501), decode("""{"type":"seek","kind":"track","ms":1500.6}"""))
        assertEquals(WebMessage.Seek(t, -1501), decode("""{"type":"seek","kind":"track","ms":-1500.6}"""))
        assertEquals(WebMessage.Skip(e, -15000), decode("""{"type":"skip","kind":"episode","ms":-15000}"""))
        assertEquals(WebMessage.SetRate(1.5), decode("""{"type":"setRate","rate":1.5}"""))
        assertEquals(WebMessage.Stop(e), decode("""{"type":"stop","kind":"episode"}"""))
        assertEquals(WebMessage.SetPrefs(NativePrefs("high", false)), decode("""{"type":"setPrefs","quality":"high","carLyrics":false}"""))
        assertEquals(WebMessage.Auth(false, null), decode("""{"type":"auth","signedIn":false}"""))
        assertEquals(WebMessage.PauseForWeb, decode("""{"type":"pauseForWeb"}"""))
        assertEquals(WebMessage.FavoriteChanged(7, true), decode("""{"type":"favoriteChanged","trackId":7,"on":true}"""))
        assertEquals(WebMessage.FlushEvents("f1"), decode("""{"type":"flushEvents","id":"f1"}"""))
        assertEquals(WebMessage.OpenSettings, decode("""{"type":"openSettings"}"""))
        assertEquals(WebMessage.LyricsChanged(7), decode("""{"type":"lyricsChanged","trackId":7}"""))
    }

    @Test fun rejectsABadRate() {
        assertNull(decode("""{"type":"setRate","rate":0}"""))
        assertNull(decode("""{"type":"setRate","rate":-1}"""))
        assertNull(decode("""{"type":"setRate","rate":"2"}"""))
        assertNull(decode("""{"type":"setRate"}"""))
    }

    @Test fun decodesSetModes() {
        assertEquals(WebMessage.SetModes(true, RepeatMode.one), decode("""{"type":"setModes","shuffle":true,"repeat":"one"}"""))
        assertEquals(WebMessage.SetModes(false, RepeatMode.all), decode("""{"type":"setModes","shuffle":false,"repeat":"all"}"""))
        assertEquals(WebMessage.SetModes(false, RepeatMode.off), decode("""{"type":"setModes","shuffle":false,"repeat":"off","extra":1}"""))
        assertNull(decode("""{"type":"setModes","shuffle":true,"repeat":"sometimes"}"""))
        assertNull(decode("""{"type":"setModes","repeat":"all"}"""))
        assertNull(decode("""{"type":"setModes","shuffle":"yes","repeat":"all"}"""))
        // setPrefs from a web that knows nothing newer still decodes
        assertEquals(WebMessage.SetPrefs(NativePrefs("saver", true)), decode("""{"type":"setPrefs","quality":"saver","carLyrics":true}"""))
    }

    @Test fun rejectsMalformedBodies() {
        assertNull(decode("\"just a string\""))
        assertNull(decode("not json"))
        assertNull(decode("[1]"))
        assertNull(decode("""{"type":"play"}"""))                    // kind missing
        assertNull(decode("""{"type":"play","kind":"video"}"""))     // unknown kind
        assertNull(decode("""{"kind":"track"}"""))                   // type missing
        assertNull(decode("""{"type":"setQueue","kind":"track","index":0,"play":true,"source":"list","items":[{"id":"7"}]}"""))  // item without kind
    }

    @Test fun itemMetaRoundTripsUnchanged() {
        val raw = """{"kind":"episode","id":"dQw4w9WgXcQ","title":"t","artist":"c","album":"频道","durationMs":1000,""" +
            """"meta":{"id":12,"nested":{"a":[1,2.5,"x",false]},"none":null,"seven":7,"seven2":7.0}}"""
        val item = Item.from(lenientJson.parseToJsonElement(raw))
        val event = NativeEvent.Queue(Item.Kind.episode, listOf(item), 0, QueueSource.list).toJson()
        assertEquals(raw.replace("/", "\\/"), event.substringAfter("\"items\":[").substringBefore("],\"index\""))
        assertEquals(item, Item.from(lenientJson.parseToJsonElement(item.toJsonObject().toString())))
    }

    @Test fun itemDefaultsAndLenientDuration() {
        val q = decode("""{"type":"setQueue","kind":"track","index":0,"play":true,"source":"list","items":[{"kind":"track","id":"7","durationMs":1500.6}]}""")
            as WebMessage.SetQueue
        assertEquals(Item(Item.Kind.track, "7", "", "", "", 1501), q.items[0])
    }

    @Test fun trackAndYoutubeIds() {
        assertEquals(123L, Item(Item.Kind.track, "123").trackId)
        assertNull(Item(Item.Kind.track, "yt:dQw4w9WgXcQ").trackId)
        assertNull(Item(Item.Kind.episode, "123").trackId)
        assertEquals("dQw4w9WgXcQ", Item(Item.Kind.track, "yt:dQw4w9WgXcQ").youtubeVideoId)
        assertNull(Item(Item.Kind.track, "yt:short").youtubeVideoId)
        assertNull(Item(Item.Kind.track, "yt:dQw4w9WgXcQ!").youtubeVideoId)
        assertNull(Item(Item.Kind.episode, "yt:dQw4w9WgXcQ").youtubeVideoId)
        assertNull(Item(Item.Kind.track, "123").youtubeVideoId)
        assertNotNull(Item(Item.Kind.track, "7"))
    }

    @Test fun decodesSleepTimer() {
        assertEquals(WebMessage.SleepTimer(SleepRequest.Minutes(15)), decode("""{"type":"sleepTimer","minutes":15}"""))
        assertEquals(WebMessage.SleepTimer(SleepRequest.Minutes(60)), decode("""{"type":"sleepTimer","minutes":60}"""))
        assertEquals(WebMessage.SleepTimer(SleepRequest.EndOfTrack), decode("""{"type":"sleepTimer","endOfTrack":true}"""))
        assertEquals(WebMessage.SleepTimer(SleepRequest.Cancel), decode("""{"type":"sleepTimer","cancel":true}"""))
        assertEquals(WebMessage.SleepTimer(SleepRequest.Cancel), decode("""{"type":"sleepTimer","minutes":5,"cancel":true}"""))
        assertEquals(WebMessage.SleepTimer(SleepRequest.Minutes(30)), decode("""{"type":"sleepTimer","minutes":30,"extra":1}"""))
    }

    @Test fun rejectsAMalformedSleepTimer() {
        assertNull(decode("""{"type":"sleepTimer"}"""))
        assertNull(decode("""{"type":"sleepTimer","minutes":0}"""))
        assertNull(decode("""{"type":"sleepTimer","minutes":-15}"""))
        assertNull(decode("""{"type":"sleepTimer","minutes":"15"}"""))
        assertNull(decode("""{"type":"sleepTimer","minutes":100000}"""))
        assertNull(decode("""{"type":"sleepTimer","endOfTrack":false}"""))
        assertNull(decode("""{"type":"sleepTimer","cancel":"yes"}"""))
    }

    @Test fun setPrefsCarriesLoudness() {
        assertEquals(WebMessage.SetPrefs(NativePrefs("high", true, false)), decode("""{"type":"setPrefs","quality":"high","carLyrics":true,"loudness":false}"""))
        assertEquals(WebMessage.SetPrefs(NativePrefs("high", true, true)), decode("""{"type":"setPrefs","quality":"high","carLyrics":true,"loudness":true}"""))
        assertEquals(WebMessage.SetPrefs(NativePrefs("high", true, true)), decode("""{"type":"setPrefs","quality":"high","carLyrics":true}"""))
        assertNull(decode("""{"type":"setPrefs","quality":"high","carLyrics":true,"loudness":"off"}"""))
    }
}
