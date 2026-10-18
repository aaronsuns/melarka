package lark.car

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import lark.app.LarkApplication
import lark.app.MainActivity

/**
 * The launcher shortcuts "Shuffle Favorites" and "Resume" (`res/xml/shortcuts.xml`; iOS `LarkShortcuts`): no UI
 * (`Theme.NoDisplay`), the intent runs and the activity finishes. Playback is prepared while this activity is still
 * visible (the intent runs synchronously up to its first suspension), so Android lets the media service start. A short
 * toast says what happened (iOS's Shortcuts dialog); with no server or signed out, Melarka opens, where that is fixed.
 */
class ShortcutActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val services = (application as LarkApplication).services
        val action = intent?.action
        val app = applicationContext
        var creating = true
        services.runIntent {
            val result = run(action, services.intentPlayer) ?: return@runIntent
            report(app, action, result)
            if (creating && (result == IntentResult.notSetUp || result == IntentResult.notSignedIn))
                startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }
        creating = false
        finish()
    }

    companion object {
        const val ACTION_SHUFFLE_FAVORITES = "lark.action.SHUFFLE_FAVORITES"
        const val ACTION_RESUME = "lark.action.RESUME"

        /** The shortcut's intent ([action]) on [player]; null for an unknown action. */
        suspend fun run(action: String?, player: IntentPlayer?): IntentResult? = when (action) {
            ACTION_SHUFFLE_FAVORITES -> IntentRun.shuffleFavorites(player)
            ACTION_RESUME -> IntentRun.resume(player)
            else -> null
        }

        /** The result's text as a toast (Resume that played says nothing, as on iOS). */
        fun report(context: Context, action: String?, result: IntentResult) {
            val text = result.text(context, shuffle = action == ACTION_SHUFFLE_FAVORITES) ?: return
            Toast.makeText(context, text, Toast.LENGTH_SHORT).show()
        }
    }
}
