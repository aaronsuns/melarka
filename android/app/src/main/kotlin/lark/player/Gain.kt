package lark.player

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import lark.bridge.Item
import kotlin.math.pow

/**
 * Loudness normalization: the server measures each track and sends `gain_db` with it (never above 0, null until
 * measured). Music plays at `10^(gain_db/20)` of full volume, so loud masters come down to the level of quiet ones;
 * nothing is ever boosted. Episodes are never touched.
 */
object Gain {
    /** Volume factor for a music item: attenuation only; 1 for episodes, unmeasured tracks (or a non-number), or when off. */
    fun factor(item: Item, enabled: Boolean): Float {
        if (!enabled || item.kind != Item.Kind.track) return 1f
        val raw = (item.meta as? JsonObject)?.get("gain_db") as? JsonPrimitive ?: return 1f
        if (raw.isString) return 1f
        val db = raw.content.toDoubleOrNull() ?: return 1f          // JSON null is a primitive whose content is "null"
        if (!db.isFinite() || db >= 0) return 1f
        return 10.0.pow(db / 20).toFloat().coerceIn(0f, 1f)
    }
}
