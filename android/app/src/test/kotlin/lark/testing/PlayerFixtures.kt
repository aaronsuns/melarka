package lark.testing

import kotlinx.serialization.json.JsonNull
import lark.bridge.Item
import lark.bridge.QueueSource
import lark.player.PlaybackQueue

fun track(id: Int) = Item(Item.Kind.track, id.toString(), "T$id", "A", "B", 1000, JsonNull)

fun pq(ids: List<Int>, index: Int = 0, positionMs: Long = 0, source: QueueSource = QueueSource.list) =
    PlaybackQueue(ids.map(::track), index, positionMs, source)

fun PlaybackQueue.ids(): List<String> = items.map { it.id }
