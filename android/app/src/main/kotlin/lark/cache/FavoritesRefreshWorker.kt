package lark.cache

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.TimeUnit

/** One favorites sync, as the background work needs it (the app services' [FavoritesSync], on the main thread). */
fun interface SyncRun {
    suspend fun run()
}

/**
 * The favorites sync in the background (iOS `BackgroundRefresh`): WorkManager periodic work every 6 h on an unmetered
 * network. The system decides when it runs; the sync itself still requires Wi-Fi. The work is cancelled when the system
 * stops it, which cancels the sync.
 *
 * The worker gets what it needs from [provider], set by the app services (Task 10) before work can run: it answers the
 * signed-in user's sync, or null while signed out (then there is nothing to do). Until it is set (a process that
 * started only for the work, before the services are up) the work ends at once; the next period tries again.
 */
object FavoritesRefresh {
    const val WORK_NAME = "io.github.aaronsuns.melarka.refresh"
    const val INTERVAL_HOURS = 6L

    @Volatile var provider: (() -> SyncRun?)? = null

    /** Keeps an already scheduled period (its timing) instead of restarting it at every launch. */
    fun schedule(context: Context) {
        val request = PeriodicWorkRequestBuilder<FavoritesRefreshWorker>(INTERVAL_HOURS, TimeUnit.HOURS)
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.UNMETERED).build())
            .build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(WORK_NAME, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    /** Sign-out: no more background syncs until the next sign-in schedules them again. */
    fun cancel(context: Context) {
        WorkManager.getInstance(context).cancelUniqueWork(WORK_NAME)
    }
}

class FavoritesRefreshWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        // The cache and the sync are main-confined (like the engine): the provider and the sync both run on the main thread.
        // Stopping the work cancels this coroutine, and with it the sync.
        withContext(Dispatchers.Main.immediate) { FavoritesRefresh.provider?.invoke()?.run() }
        return Result.success()
    }
}
