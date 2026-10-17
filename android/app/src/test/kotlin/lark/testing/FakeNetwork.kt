package lark.testing

import lark.core.NetworkStatus

class FakeNetwork : NetworkStatus {
    /** False: like a fresh network monitor before its first callback (`answer` gives it one). */
    override var answered = true

    fun answer(online: Boolean = true, expensive: Boolean = false) {
        answered = true
        // Set the backing fields directly: one notification, not one per property.
        isOnlineValue = online
        isExpensiveValue = expensive
        observers.toList().forEach { it() }
    }

    private var isOnlineValue = true
    private var isExpensiveValue = false

    override var isOnline: Boolean
        get() = isOnlineValue
        set(v) { isOnlineValue = v; observers.toList().forEach { it() } }

    override var isExpensive: Boolean
        get() = isExpensiveValue
        set(v) { isExpensiveValue = v; observers.toList().forEach { it() } }

    val observers = mutableListOf<() -> Unit>()
    override fun observe(fn: () -> Unit) { observers.add(fn) }
}
