package lark.core

import java.time.Instant

/** The wall clock, injected so tests move time by hand (iOS passes `now: () -> Date`). */
fun interface Clock {
    fun now(): Instant

    companion object {
        val system = Clock { Instant.now() }
    }
}
