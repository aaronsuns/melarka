package lark.car

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import androidx.media3.common.util.UnstableApi
import androidx.media3.session.DefaultMediaNotificationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import lark.DemoServer
import lark.Instrumented
import lark.Instrumented.onMain
import lark.Instrumented.waitFor
import lark.app.AppServices
import lark.auth.KeystoreSecretStore
import lark.service.PlaybackService
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/**
 * The launcher's Shuffle Favorites with the app not running (Android 12+): the shortcut's activity finishes at once,
 * the shuffle asks the server after that, and the media service still goes into the foreground with Media3's own
 * notification and plays; no Melarka screen opens.
 */
@UnstableApi
@RunWith(AndroidJUnit4::class)
class ShortcutTest {
    @Test fun shuffleFavoritesFromTheShortcutPlaysInTheForegroundWithNoScreen() {
        val server = DemoServer()
        val session = server.login("smoke-shortcut")
        val favorites = server.seedFavorites(session)
        val ctx = Instrumented.context
        assertTrue("nothing built yet", !Instrumented.app.servicesBuilt)
        // Signed in earlier (as the cookie harvest leaves it): the server, the token and the user.
        ctx.getSharedPreferences("lark", Context.MODE_PRIVATE).edit().putString(AppServices.SERVER_URL_KEY, server.url + "/").commit()
        KeystoreSecretStore(File(ctx.filesDir, "secrets.bin")).apply { set("token", session.token); set("userId", session.userId.toString()) }

        // As the launcher starts a shortcut: its intent, in a new task.
        ctx.startActivity(Intent(ShortcutActivity.ACTION_SHUFFLE_FAVORITES).setClassName(ctx, ShortcutActivity::class.java.name)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))

        try {
            waitFor("the engine plays", 30_000) { onMain { Instrumented.app.servicesBuilt && Instrumented.app.services.engine?.playing == true } }
        } catch (e: AssertionError) {
            val state = onMain {
                val en = Instrumented.app.services.engine
                val p = lark.service.Playback.host(ctx).player
                "engine playing=${en?.playing} current=${en?.current?.title} pausedBySystem=${en?.pausedBySystem} " +
                    "player pwr=${p.playWhenReady} state=${p.playbackState} suppression=${p.playbackSuppressionReason} isPlaying=${p.isPlaying} service=${PlaybackService.running}"
            }
            throw AssertionError("$state", e)
        }
        val title = onMain { Instrumented.app.services.engine?.current?.title }
        assertTrue("a favorite: $title", title in favorites.values)
        waitFor("the media service runs") { PlaybackService.running }
        val nm = ctx.getSystemService(NotificationManager::class.java)
        waitFor("the media notification (the service in the foreground)") {
            nm.activeNotifications.any { it.id == DefaultMediaNotificationProvider.DEFAULT_NOTIFICATION_ID && it.notification.extras.getCharSequence("android.title")?.toString() == title }
        }
        assertTrue("no Melarka screen opened", onMain {
            ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED).isEmpty()
        })
        onMain { Instrumented.app.services.engine?.pause() }
    }

    /**
     * The shortcut's shuffle fails after its activity finished (the server does not answer): no toast can show then
     * (Android 13+, no notification permission), so Melarka comes forward and says why in a dialog.
     */
    @Test fun aShuffleThatFailsLaterBringsMelarkaForwardWithTheReason() {
        val ctx = Instrumented.context
        // Signed in to a server that is gone (nothing listens on that port of the host).
        ctx.getSharedPreferences("lark", Context.MODE_PRIVATE).edit().putString(AppServices.SERVER_URL_KEY, "http://10.0.2.2:4799/").commit()
        KeystoreSecretStore(File(ctx.filesDir, "secrets.bin")).apply { set("token", "not-a-real-token"); set("userId", "1") }

        ctx.startActivity(Intent(ShortcutActivity.ACTION_SHUFFLE_FAVORITES).setClassName(ctx, ShortcutActivity::class.java.name)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))

        waitFor("Melarka in front", 30_000) {
            onMain { ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED).any { it is lark.app.MainActivity } }
        }
        val notice = checkNotNull(onMain { Instrumented.app.services.notice }) { "the services' notice" }
        val device = UiDevice.getInstance(Instrumented.instrumentation)
        checkNotNull(device.wait(Until.findObject(By.text(notice)), 10_000)) { "the dialog saying: $notice" }
        assertFalse("nothing plays", onMain { Instrumented.app.services.engine?.playing == true })
        device.findObject(By.text(ctx.getString(android.R.string.ok))).click()
        waitFor("the notice dismissed") { onMain { Instrumented.app.services.notice == null } }
    }
}
