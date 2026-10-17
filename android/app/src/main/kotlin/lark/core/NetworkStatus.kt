package lark.core

/**
 * Online / expensive (metered) state. Production uses [AndroidNetworkStatus]; tests use `FakeNetwork`.
 * Several parts react to changes (the engine, the cache's Wi-Fi sync), so it is a multicast: each adds its
 * own observer and none can replace another's. All of it runs on the main thread.
 */
interface NetworkStatus {
    val isOnline: Boolean

    /** Metered, or Data Saver is on for this app: no bulk downloads. */
    val isExpensive: Boolean

    /** False until the first network callback: until then [isOnline] and [isExpensive] are guesses. */
    val answered: Boolean

    /** `fn` runs on every change of [isOnline] or [isExpensive], for the life of the status object. */
    fun observe(fn: () -> Unit)
}

/**
 * The Stay Offline switch (a flight): offline whatever the phone's network says, so nothing that waits for the
 * network (page reloads, retries, refills, syncs, prefetches) tries a server it can't reach. Turning it off is
 * a change like the network coming back.
 */
class SwitchableNetworkStatus(private val inner: NetworkStatus, stayOffline: Boolean = false) : NetworkStatus {
    private val observers = mutableListOf<() -> Unit>()

    var stayOffline: Boolean = stayOffline
        set(value) {
            val changed = field != value
            field = value
            if (changed) observers.toList().forEach { it() }
        }

    init {
        inner.observe { observers.toList().forEach { it() } }
    }

    override val isOnline: Boolean get() = !stayOffline && inner.isOnline
    override val isExpensive: Boolean get() = inner.isExpensive

    /** Switched off-line is a sure answer: a cold launch waits for nothing. */
    override val answered: Boolean get() = stayOffline || inner.answered
    override fun observe(fn: () -> Unit) { observers.add(fn) }
}
