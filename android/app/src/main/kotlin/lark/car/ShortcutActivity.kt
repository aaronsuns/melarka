package lark.car

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import lark.app.LarkApplication
import lark.app.MainActivity
import lark.app.R
import lark.service.PlaybackService

/**
 * The launcher shortcuts "Shuffle Favorites" and "Resume" (`res/xml/shortcuts.xml`; iOS `LarkShortcuts`): no UI
 * (`Theme.NoDisplay`), the intent runs and the activity finishes. Playback is prepared while this activity is still
 * visible (the intent runs synchronously up to its first suspension), so Android lets the media service start. A short
 * toast says what happened (iOS's Shortcuts dialog); with no server or signed out, Melarka opens, where that is fixed.
 */
@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
open class ShortcutActivity : Activity() {
    /** The shortcut action [intent] asks for, null for anything else (which does nothing at all). */
    protected open fun shortcutAction(intent: Intent?): String? = intent?.action?.takeIf { it in ACTIONS }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val action = shortcutAction(intent) ?: return finish()
        val services = (application as LarkApplication).services
        val app = applicationContext
        var creating = true
        val start = { PlaybackService.startInForeground(app) }
        val refused = getString(R.string.intent_background_start_refused)
        services.runIntent {
            val result = run(action, services.intentPlayer, start, refused) ?: return@runIntent
            val r = reply(result, showing = creating, text = result.text(app, shuffle = action == ACTION_SHUFFLE_FAVORITES))
            r.toast?.let { Toast.makeText(app, it, Toast.LENGTH_SHORT).show() }
            r.notice?.let { services.showNotice(it) }
            if (r.open) openMelarka(app)
        }
        creating = false
        finish()
    }

    /**
     * What the shortcut does with its result ([reply]): a [toast], opening Melarka ([open]), and a [notice] the app
     * shows in a dialog once it is on screen.
     */
    data class Reply(val toast: String? = null, val open: Boolean = false, val notice: String? = null)

    companion object {
        const val ACTION_SHUFFLE_FAVORITES = "lark.action.SHUFFLE_FAVORITES"
        const val ACTION_RESUME = "lark.action.RESUME"
        private val ACTIONS = setOf(ACTION_SHUFFLE_FAVORITES, ACTION_RESUME)

        /** The activities whose own task goes as they finish (the media service ignores that task's removal). */
        val TASK_CLASSES = setOf(ShortcutActivity::class.java.name, AutomationActivity::class.java.name)

        /**
         * The shortcut's intent ([action]) on [player]; null for an unknown action. The media service is started into
         * the foreground ([start], `PlaybackService.startInForeground`) before playback is prepared, while this activity
         * is visible: its cold-start notification holds the foreground until playback starts, so a shuffle from the
         * server that answers after the activity is gone still gets its media notification.
         */
        suspend fun run(action: String?, player: IntentPlayer?, start: () -> Boolean, refusedText: String): IntentResult? {
            val p = player?.let { BackgroundStart(it, start, refusedText) }
            return when (action) {
                ACTION_SHUFFLE_FAVORITES -> IntentRun.shuffleFavorites(p)
                ACTION_RESUME -> IntentRun.resume(p)
                else -> null
            }
        }

        /**
         * The result's [text] (iOS's Shortcuts dialog). While the activity is [showing], a toast (Resume that played
         * says nothing, as on iOS), and with no server or signed out Melarka opens too. After it finished (a shuffle the
         * server answered later), Android 13+ drops a toast from an app in the background whose notifications are off,
         * and Melarka asks for no notification permission: a failure brings Melarka forward with the message instead;
         * a success or "already playing" needs none.
         */
        fun reply(result: IntentResult, showing: Boolean, text: String?): Reply = when {
            showing -> Reply(toast = text, open = result == IntentResult.notSetUp || result == IntentResult.notSignedIn)
            result.failed -> Reply(open = true, notice = text)
            else -> Reply()
        }

        /**
         * Melarka in front. From the background Android may refuse it (it allows it for a few seconds after the
         * shortcut's activity): the notice then waits in the services until Melarka is opened.
         */
        private fun openMelarka(context: Context) {
            try {
                context.startActivity(Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (_: RuntimeException) {
                // Not allowed from the background: the notice shows when Melarka is next opened.
            }
        }
    }
}
