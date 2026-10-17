package lark.testing

import lark.core.Cancellable
import lark.core.Scheduler

/** Time moves only when the test says so. */
class FakeScheduler : Scheduler {
    private class Job(var at: Double, val interval: Double?, val fn: () -> Unit) : Cancellable {
        var cancelled = false
        override fun cancel() { cancelled = true }
    }

    var now = 0.0
        private set
    private val jobs = mutableListOf<Job>()

    val pending: Int get() = jobs.count { !it.cancelled }

    override fun after(seconds: Double, fn: () -> Unit): Cancellable = add(Job(now + seconds, null, fn))

    override fun every(seconds: Double, fn: () -> Unit): Cancellable = add(Job(now + seconds, seconds, fn))

    private fun add(job: Job): Cancellable { jobs.add(job); return job }

    /** Runs every job due within `seconds`, in time order (including jobs scheduled meanwhile). */
    fun advance(seconds: Double) {
        val end = now + seconds
        while (true) {
            val j = jobs.filter { !it.cancelled && it.at <= end }.minByOrNull { it.at } ?: break
            now = j.at
            if (j.interval == null) jobs.remove(j) else j.at += j.interval
            j.fn()
        }
        now = end
        jobs.removeAll { it.cancelled }
    }
}
