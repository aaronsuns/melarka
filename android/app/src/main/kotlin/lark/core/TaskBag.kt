package lark.core

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * The async work a component has started, so tests can wait for all of it ([idle]) and a sign-out can drop it
 * ([cancelAll]). Every task runs in [scope] (the app's main-thread scope; a test scope in tests), and the bag is
 * used from that thread only.
 */
class TaskBag(private val scope: CoroutineScope) {
    private val tasks = LinkedHashMap<Long, Job>()
    private var nextId = 0L

    fun run(op: suspend () -> Unit) {
        val id = nextId++
        // Started only once it is in the map: a task that ends at once still leaves it.
        val job = scope.launch(start = CoroutineStart.LAZY) { op() }
        tasks[id] = job
        job.invokeOnCompletion { tasks.remove(id) }
        job.start()
    }

    val isEmpty: Boolean get() = tasks.isEmpty()

    /** Waits until every task started so far, and every task those started, has finished. */
    suspend fun idle() {
        while (true) {
            val t = tasks.values.firstOrNull() ?: return
            t.join()
        }
    }

    fun cancelAll() {
        tasks.values.toList().forEach { it.cancel() }
        tasks.clear()
    }
}
