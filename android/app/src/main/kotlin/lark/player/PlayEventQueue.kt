package lark.player

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import lark.api.LarkApiProtocol
import lark.api.PlayEvent
import lark.core.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

/**
 * Play events waiting for `POST /events/play`, like the web's `EventBuffer`: each gets a `client_event_id`
 * (UUID) once, so a retry of the same event is deduped by the server. Pending events live in
 * `events-<userId>.json`, so a failed POST or a relaunch loses nothing. Background sends run in [scope].
 */
class PlayEventQueue(
    private val api: LarkApiProtocol,
    private val store: QueueStore,
    private val scope: CoroutineScope,
    private val clock: Clock = Clock.system,
) {
    private var memory: List<PlayEvent> = emptyList()   // no user: kept in memory only
    private val flushing = Mutex()
    private var lastFlush: Instant? = null

    /** Offline a POST can only fail: events wait on disk, and the first tick back online sends them. */
    var online: () -> Boolean = { true }

    val pending: List<PlayEvent> get() = if (store.user == null) memory else store.loadEvents()

    private fun save(e: List<PlayEvent>) { if (store.user == null) memory = e else store.saveEvents(e) }

    /** Records one listen and starts sending it. */
    fun add(trackId: Long, startedAt: Long, playedSeconds: Long, skipped: Boolean, quality: String) {
        var all = pending + PlayEvent(UUID.randomUUID().toString().lowercase(), trackId, startedAt, playedSeconds, skipped, quality)
        if (all.size > MAX_PENDING) all = all.drop(all.size - MAX_PENDING)   // the oldest go
        save(all)
        if (online()) launchFlush()
    }

    /**
     * Sends everything pending. A flush already in flight is waited for, then whatever is left goes too.
     * The server may accept fewer than it was sent (unknown tracks are skipped): those are dropped, since
     * sending them again would be skipped again. A failed POST keeps everything for the next flush.
     */
    suspend fun flush() = flushing.withLock {
        val batch = pending
        lastFlush = clock.now()
        if (batch.isEmpty()) return@withLock
        try { api.postEvents(batch) } catch (e: CancellationException) { throw e } catch (_: Exception) {
            return@withLock   // kept for the next flush; the server dedupes by client_event_id
        }
        val sent = batch.mapTo(HashSet()) { it.clientEventId }
        save(pending.filter { it.clientEventId !in sent })
    }

    /** Called from the engine's 10 s tick: a flush every 30 s while anything is pending. */
    fun flushIfDue() {
        val last = lastFlush
        if (!online() || pending.isEmpty() || (last != null && Duration.between(last, clock.now()) < FLUSH_EVERY)) return
        launchFlush()
    }

    /** Sign-out: forget every pending event of this user, on disk too. */
    fun clear() {
        memory = emptyList()
        store.saveEvents(emptyList())
    }

    private fun launchFlush() { scope.launch { flush() } }

    companion object {
        val FLUSH_EVERY: Duration = Duration.ofSeconds(30)

        /** Weeks offline must not grow the file without bound: past this, the oldest events are dropped. */
        const val MAX_PENDING = 2000
    }
}
