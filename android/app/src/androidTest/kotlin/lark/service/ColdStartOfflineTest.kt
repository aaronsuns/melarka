package lark.service

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.view.KeyEvent
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.DefaultMediaNotificationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import lark.Instrumented
import lark.Instrumented.onMain
import lark.Instrumented.waitFor
import lark.app.AppServices
import lark.auth.KeystoreSecretStore
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/**
 * A Bluetooth play with the app not running, offline, with nothing cached: the media service comes up in the
 * foreground (within Android's 5 s), finds nothing to play, and stops again, its notification gone.
 */
@UnstableApi
@RunWith(AndroidJUnit4::class)
class ColdStartOfflineTest {
    @After fun tearDown() = Instrumented.setNetwork(true)

    @Test fun aPlayOfflineWithNothingCachedStopsTheService() {
        val ctx = Instrumented.context
        assertFalse("nothing built yet", Instrumented.app.servicesBuilt)
        // Signed in to a server earlier (the services read these when the play builds them).
        ctx.getSharedPreferences("lark", Context.MODE_PRIVATE).edit().putString(AppServices.SERVER_URL_KEY, "http://10.0.2.2:4700/").commit()
        KeystoreSecretStore(File(ctx.filesDir, "secrets.bin")).apply { set("token", "not-a-real-token"); set("userId", "1") }
        Instrumented.setNetwork(false)
        waitFor("the phone is offline", 30_000) { ctx.getSystemService(ConnectivityManager::class.java).activeNetwork == null }

        val play = Intent(ctx, PlaybackService::class.java).setAction(Intent.ACTION_MEDIA_BUTTON)
            .putExtra(Intent.EXTRA_KEY_EVENT, KeyEvent(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MEDIA_PLAY))
        ctx.startForegroundService(play)

        waitFor("the services came up with the service") { onMain { Instrumented.app.servicesBuilt } }
        waitFor("the service stops", 45_000) { !PlaybackService.running }
        assertTrue("it was the engine's cold start", onMain { Instrumented.app.services.engine != null })
        assertFalse("nothing plays", onMain { Instrumented.app.services.engine?.playing == true })
        val nm = ctx.getSystemService(NotificationManager::class.java)
        assertTrue("no notification left", nm.activeNotifications.none { it.id == DefaultMediaNotificationProvider.DEFAULT_NOTIFICATION_ID })
    }
}
