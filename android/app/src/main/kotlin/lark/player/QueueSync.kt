package lark.player

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import lark.api.LarkApiProtocol
import lark.api.ServerQueue
import lark.core.Cancellable
import lark.core.Clock
import lark.core.Scheduler
import java.time.Duration
import java.time.Instant

/**
 * `PUT /queue` for the music queue (episodes never go to the server): 1 s after the last edit (debounced),
 * and every 15 s while music plays. Also the rule for adopting the server's queue ([shouldAdopt]).
 */
class QueueSync(
    private val api: LarkApiProtocol,
    private val scheduler: Scheduler,
    private val clock: Clock,
    private val scope: CoroutineScope,
) {
    data class Snapshot(val trackIds: List<Long>, val index: Int, val positionMs: Long)

    private var debounced: Cancellable? = null
    private var lastPush: Instant? = null
    private var epoch = 0
    private var owed = false   // a push skipped offline: sent when the network is back ([reconnected])
    private val flights = mutableSetOf<Job>()

    /** Offline a PUT can only fail: none is tried, so a flight does not try every 15 s. False too while a `yt:` track is queued. */
    var online: () -> Boolean = { true }

    /** The music queue as it should be sent now; null when there is nothing to send. */
    var snapshot: () -> Snapshot? = { null }

    /** The server's `updated_by` for our own PUT: this device's name there. */
    var onOwnDevice: ((String) -> Unit)? = null

    /** The music queue changed: send it once the edits stop for a second. */
    fun edited() {
        debounced?.cancel()
        debounced = scheduler.after(DEBOUNCE_S) { push() }
    }

    /**
     * Music is playing (called on the backend's ticks and the 10 s tick).
     * The first tick only starts the clock: the edit that started playback is already being sent.
     */
    fun playingTick() {
        val last = lastPush
        if (last == null) { lastPush = clock.now(); return }
        if (Duration.between(last, clock.now()) >= EVERY_WHILE_PLAYING) push()
    }

    fun push() {
        debounced?.cancel(); debounced = null
        lastPush = clock.now()
        if (!online()) { owed = true; return }
        owed = false
        val s = snapshot() ?: return
        val e = epoch
        val job = scope.launch {
            // Errors (offline, 5xx) are left for the next edit or the next 15 s tick: the queue is also on disk.
            val stored = try { api.saveQueue(s.trackIds, s.index, s.positionMs) } catch (ex: CancellationException) { throw ex } catch (_: Exception) { null }
            if (stored != null && epoch == e) onOwnDevice?.invoke(stored.updatedBy)
        }
        flights.add(job)
        job.invokeOnCompletion { flights.remove(job) }
    }

    /** The network is back: the queue as it is now, if a push was skipped meanwhile. */
    fun reconnected() { if (owed) push() }

    /** Sign-out: nothing more goes out for the old user. */
    fun cancel() {
        epoch += 1; owed = false
        debounced?.cancel(); debounced = null
        lastPush = null
        flights.toList().forEach { it.cancel() }
        flights.clear()
    }

    companion object {
        const val DEBOUNCE_S = 1.0
        val EVERY_WHILE_PLAYING: Duration = Duration.ofSeconds(15)

        /** The server's queue must be this much newer than the local one to replace it (seconds). */
        const val NEWER_BY = 5L

        /**
         * Adopt the server's queue? Always when the local one is empty. Otherwise only when it is newer
         * (`updated_at` > local `savedAt` + 5 s), came from another device, and nothing is playing.
         */
        fun shouldAdopt(server: ServerQueue, localEmpty: Boolean, savedAt: Long, ownDevice: String?, playing: Boolean): Boolean {
            if (playing || server.trackIds.isEmpty()) return false
            if (localEmpty) return true
            return server.updatedAt > savedAt + NEWER_BY && server.updatedBy != ownDevice
        }
    }
}
