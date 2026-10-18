package lark.car

import android.content.Intent

/**
 * The automation entry point (Tasker, Samsung Modes & Routines and the like; iOS: an App Shortcut in a Shortcuts
 * automation): exported with no permission, it takes exactly [ACTION_SHUFFLE_FAVORITES] and [ACTION_RESUME] and runs
 * them as the launcher shortcuts do (the same no-UI activity, toast and foreground start). Extras and data are ignored;
 * any other action, the launcher shortcuts' own included, does nothing. What another app can do with it is what a
 * headset's play button already can: start Melarka's own music.
 */
class AutomationActivity : ShortcutActivity() {
    override fun shortcutAction(intent: Intent?): String? = Companion.shortcutAction(intent)

    companion object {
        const val ACTION_SHUFFLE_FAVORITES = "io.github.aaronsuns.melarka.action.SHUFFLE_FAVORITES"
        const val ACTION_RESUME = "io.github.aaronsuns.melarka.action.RESUME"

        /** The shortcut action for a public [intent] action; null for anything else. Only the action is read. */
        fun shortcutAction(intent: Intent?): String? = when (intent?.action) {
            ACTION_SHUFFLE_FAVORITES -> ShortcutActivity.ACTION_SHUFFLE_FAVORITES
            ACTION_RESUME -> ShortcutActivity.ACTION_RESUME
            else -> null
        }
    }
}
