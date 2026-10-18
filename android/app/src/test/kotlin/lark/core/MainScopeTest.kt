package lark.core

import android.os.Looper
import androidx.media3.common.util.UnstableApi
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import lark.service.PlaybackHost
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.shadows.ShadowLog

/** The process-long scopes log a failed task (its class only) instead of crashing the process. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MainScopeTest {
    private class Boom : RuntimeException("secret detail")

    private fun failedLogs(tag: String) = ShadowLog.getLogsForTag(tag).map { it.msg }

    @Test fun aFailedTaskIsLoggedByClassOnly() {
        val scope = loggingMainScope("T")
        scope.launch { throw Boom() }
        shadowOf(Looper.getMainLooper()).idle()
        assertEquals(listOf("an app task failed: ${Boom::class.java.name}"), failedLogs("T"))
        var ran = false
        scope.launch { ran = true }                        // the scope goes on
        shadowOf(Looper.getMainLooper()).idle()
        assertTrue(ran)
        scope.cancel()
    }

    /** The playback host's scope (the remote commands' cold-start shuffle runs there) has the same handler. */
    @androidx.annotation.OptIn(UnstableApi::class)
    @Test fun thePlaybackHostsScopeLogsAFailedTask() {
        val host = PlaybackHost.create(ApplicationProvider.getApplicationContext(), OkHttpClient())
        try {
            host.scope.launch { throw Boom() }
            shadowOf(Looper.getMainLooper()).idle()
            assertTrue(failedLogs("PlaybackHost").none { it.contains("secret detail") })
            assertEquals(listOf("an app task failed: ${Boom::class.java.name}"), failedLogs("PlaybackHost"))
        } finally {
            host.scope.cancel(); host.player.release()
        }
    }
}
