package lark.car

import android.content.SharedPreferences
import java.util.Locale

/**
 * The car autoplay setting (the Settings screen turns it on by picking the car): on or off, and the car Android
 * associated with Melarka (its Bluetooth address, its name as shown, the association's id on Android 13+, else -1).
 */
class CarAutoplaySettings(private val prefs: SharedPreferences) {
    val enabled: Boolean get() = prefs.getBoolean(ENABLED_KEY, false) && address != null
    val address: String? get() = prefs.getString(ADDRESS_KEY, null)
    val name: String? get() = prefs.getString(NAME_KEY, null)
    val associationId: Int get() = prefs.getInt(ASSOCIATION_KEY, -1)

    /** The associated car is this Bluetooth address (any letter case, as Android writes them both ways). */
    fun matches(address: String): Boolean = this.address?.let { key(it) == key(address) } ?: false

    /** The car is associated: car autoplay is on for it. */
    fun save(address: String, name: String?, associationId: Int) {
        prefs.edit().putBoolean(ENABLED_KEY, true).putString(ADDRESS_KEY, key(address)).putString(NAME_KEY, name)
            .putInt(ASSOCIATION_KEY, associationId).apply()
    }

    /** Off, and the car forgotten. */
    fun clear() {
        prefs.edit().remove(ENABLED_KEY).remove(ADDRESS_KEY).remove(NAME_KEY).remove(ASSOCIATION_KEY).apply()
    }

    companion object {
        const val ENABLED_KEY = "lark.car.enabled"
        const val ADDRESS_KEY = "lark.car.address"
        const val NAME_KEY = "lark.car.name"
        const val ASSOCIATION_KEY = "lark.car.associationId"

        fun key(address: String): String = address.trim().uppercase(Locale.ROOT)
    }
}
