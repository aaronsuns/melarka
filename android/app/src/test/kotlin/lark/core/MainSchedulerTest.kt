package lark.core

import android.os.Looper
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import java.time.Duration

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MainSchedulerTest {
    private val scheduler = MainScheduler()
    private fun idle(seconds: Long) = shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(seconds))

    @Test fun afterRunsOnceWhenDue() {
        var runs = 0
        scheduler.after(5.0) { runs += 1 }
        idle(4); assertEquals(0, runs)
        idle(1); assertEquals(1, runs)
        idle(20); assertEquals(1, runs)
    }

    @Test fun aCancelledAfterNeverRuns() {
        var runs = 0
        scheduler.after(5.0) { runs += 1 }.cancel()
        idle(10)
        assertEquals(0, runs)
    }

    @Test fun everyRepeatsUntilCancelled() {
        var runs = 0
        val timer = scheduler.every(2.0) { runs += 1 }
        idle(1); assertEquals(0, runs)
        idle(1); assertEquals(1, runs)
        idle(4); assertEquals(3, runs)
        timer.cancel()
        idle(10); assertEquals(3, runs)
    }

    @Test fun everyCanBeCancelledFromInsideItsOwnFunction() {
        var runs = 0
        lateinit var timer: Cancellable
        timer = scheduler.every(1.0) { runs += 1; if (runs == 2) timer.cancel() }
        idle(10)
        assertEquals(2, runs)
    }
}
