package lark.core

import android.os.Handler
import android.os.Looper

/** A timer that can be cancelled. */
fun interface Cancellable {
    fun cancel()
}

/**
 * Delayed work on the main thread: network retries, the `/queue` debounce, refill retries.
 * Production uses [MainScheduler]; tests use `FakeScheduler` and move time by hand.
 */
interface Scheduler {
    fun after(seconds: Double, fn: () -> Unit): Cancellable

    /** Runs `fn` every `seconds`, the first time after `seconds`, until cancelled. */
    fun every(seconds: Double, fn: () -> Unit): Cancellable
}

class MainScheduler(private val handler: Handler = Handler(Looper.getMainLooper())) : Scheduler {
    private fun millis(seconds: Double) = (seconds * 1000).toLong().coerceAtLeast(0)

    override fun after(seconds: Double, fn: () -> Unit): Cancellable {
        val job = Runnable { fn() }
        handler.postDelayed(job, millis(seconds))
        return Cancellable { handler.removeCallbacks(job) }
    }

    override fun every(seconds: Double, fn: () -> Unit): Cancellable {
        val job = object : Runnable {
            override fun run() {
                handler.postDelayed(this, millis(seconds))
                fn()
            }
        }
        handler.postDelayed(job, millis(seconds))
        return Cancellable { handler.removeCallbacks(job) }
    }
}
