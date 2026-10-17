package lark.testing

import lark.core.Clock
import java.time.Instant

/** Time moves only when the test says so (iOS: `clock` / `advanceClock` in `EngineTestCase`). */
class FakeClock(var current: Instant = Instant.ofEpochSecond(1_800_000_000)) : Clock {
    override fun now(): Instant = current
    fun advance(seconds: Double) { current = current.plusMillis((seconds * 1000).toLong()) }
}
