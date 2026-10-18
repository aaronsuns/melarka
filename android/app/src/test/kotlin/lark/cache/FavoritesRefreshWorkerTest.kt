package lark.cache

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.work.Configuration
import androidx.work.ListenableWorker
import androidx.work.NetworkType
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.TestListenableWorkerBuilder
import androidx.work.testing.WorkManagerTestInitHelper
import android.os.Looper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import org.robolectric.Shadows.shadowOf
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.TimeUnit

/** Port of BackgroundRefreshTests: the background run does the sync, stops with it, and is asked again 6 h later. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@OptIn(DelicateCoroutinesApi::class)
class FavoritesRefreshWorkerTest {
    private val context: Context = ApplicationProvider.getApplicationContext()

    @Before fun setUp() {
        FavoritesRefresh.provider = null
        WorkManagerTestInitHelper.initializeTestWorkManager(
            context, Configuration.Builder().setExecutor(SynchronousExecutor()).setMinimumLoggingLevel(android.util.Log.DEBUG).build())
    }

    @After fun tearDown() { FavoritesRefresh.provider = null }

    private fun worker() = TestListenableWorkerBuilder<FavoritesRefreshWorker>(context).build()

    /** Lets the main looper run while the test thread waits (the work hops to the main thread). */
    private fun <T> awaitIdling(d: Deferred<T>): T = runBlocking {
        withTimeout(10_000) {
            while (!d.isCompleted) { shadowOf(Looper.getMainLooper()).idle(); delay(5) }
            d.await()
        }
    }

    /** CacheStore and FavoritesSync are main-confined: both the provider and the sync run on the main thread. */
    @Test fun providerAndSyncRunOnTheMainThread() {
        val main = Looper.getMainLooper().thread
        val seen = mutableListOf<Thread>()
        FavoritesRefresh.provider = { seen.add(Thread.currentThread()); SyncRun { seen.add(Thread.currentThread()) } }
        val result = GlobalScope.async(Dispatchers.Default) { worker().doWork() }   // WorkManager starts it off the main thread
        assertEquals(ListenableWorker.Result.success(), awaitIdling(result))
        assertEquals(listOf(main, main), seen)
    }

    @Test fun runsTheSyncOfTheSignedInUser() = runBlocking {
        var runs = 0
        FavoritesRefresh.provider = { SyncRun { runs += 1 } }
        assertEquals(ListenableWorker.Result.success(), worker().doWork())
        assertEquals(1, runs)
    }

    @Test fun doesNothingWhileSignedOut() = runBlocking {
        var asked = 0
        FavoritesRefresh.provider = { asked += 1; null }                    // the app services answer no sync: signed out
        assertEquals(ListenableWorker.Result.success(), worker().doWork())
        assertEquals(1, asked)
    }

    @Test fun doesNothingBeforeTheServicesAreUp() = runBlocking {
        assertNull(FavoritesRefresh.provider)
        assertEquals(ListenableWorker.Result.success(), worker().doWork())
    }

    @Test fun stoppingTheWorkCancelsTheSync() = runBlocking {
        val started = CompletableDeferred<Unit>()
        val cancelled = CompletableDeferred<Boolean>()
        FavoritesRefresh.provider = {
            SyncRun {
                started.complete(Unit)
                try { CompletableDeferred<Unit>().await() } catch (e: kotlinx.coroutines.CancellationException) { cancelled.complete(true); throw e }
            }
        }
        val w = worker()
        val result = GlobalScope.async(Dispatchers.Default) { w.startWork() }.let { awaitIdling(it) }
        awaitIdling(started)
        // What WorkManager does when the system stops the work (the network left, the time is up): it cancels the
        // future `startWork` returned, which cancels the coroutine, and with it the sync.
        result.cancel(true)
        assertTrue(awaitIdling(cancelled))
    }

    @Test fun isScheduledEverySixHoursOnAnUnmeteredNetworkAndOnlyOnce() {
        FavoritesRefresh.schedule(context)
        FavoritesRefresh.schedule(context)                                  // a launch again: keeps the one it has
        val wm = WorkManager.getInstance(context)
        val infos = wm.getWorkInfosForUniqueWork(FavoritesRefresh.WORK_NAME).get()
        assertEquals(1, infos.size)
        val info = infos[0]
        assertEquals(WorkInfo.State.ENQUEUED, info.state)
        assertEquals(TimeUnit.HOURS.toMillis(6), info.periodicityInfo!!.repeatIntervalMillis)
        assertEquals(NetworkType.UNMETERED, info.constraints.requiredNetworkType)
        assertEquals("io.github.aaronsuns.melarka.refresh", FavoritesRefresh.WORK_NAME)
        FavoritesRefresh.cancel(context)
        assertEquals(WorkInfo.State.CANCELLED, wm.getWorkInfosForUniqueWork(FavoritesRefresh.WORK_NAME).get()[0].state)
    }
}
