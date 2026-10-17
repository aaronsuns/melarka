package lark.testing

import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.player.PlaybackQueue

/** iOS `track(_:)`: a music item of 200 s whose meta is `{"id": id}`. */
fun track(id: Int) = Item(Item.Kind.track, id.toString(), "T$id", "A", "B", 200_000, buildJsonObject { put("id", id) })

/** iOS `episode(_:)`. */
fun episode(id: String, positionS: Int = 0, played: Boolean = false, durationS: Int = 3600) =
    Item(Item.Kind.episode, id, "E", "C", "频道", durationS * 1000L, buildJsonObject {
        put("video_id", id); put("position_s", positionS); put("played", played); put("duration_s", durationS)
    })

/** iOS `gainTrack`: a music item whose meta carries the server's `gain_db` (null: not measured). */
fun gainTrack(id: Int, gainDb: Double?) = Item(Item.Kind.track, id.toString(), "T$id", "A", "B", 200_000, buildJsonObject {
    put("id", id); put("gain_db", gainDb?.let { JsonPrimitive(it) } ?: JsonNull)
})

/** iOS `q(_:index:pos:play:source:)`: a music `setQueue`. */
fun q(ids: List<Int>, index: Int, pos: Long?, play: Boolean, source: QueueSource = QueueSource.list) =
    WebMessage.SetQueue(Item.Kind.track, ids.map(::track), index, pos, play, source)

fun pq(ids: List<Int>, index: Int = 0, positionMs: Long = 0, source: QueueSource = QueueSource.list) =
    PlaybackQueue(ids.map(::track), index, positionMs, source)

fun PlaybackQueue.ids(): List<String> = items.map { it.id }
