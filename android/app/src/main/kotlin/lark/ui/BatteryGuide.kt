package lark.ui

import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import lark.app.ExternalLink
import lark.app.R

/**
 * Background playback against the phone's power management: Android's own battery-optimisation exemption for Melarka
 * (a media player is one of the uses Android's policy names for it; F-Droid takes the permission for media apps too),
 * and for the phone makers whose power management goes further, their own setting in words (`CarAutoplay.vendorHint`)
 * and the steps in `docs/android.md`. There is no vendor code path.
 */
object BatteryGuide {
    const val DOCS_URL = "https://github.com/aaronsuns/melarka/blob/main/docs/android.md"
    private const val TAG = "BatteryGuide"

    /** Android already lets Melarka run in the background (battery optimisation off for it). */
    fun isExempt(context: Context): Boolean =
        context.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(context.packageName) == true

    /** The system's "Let Melarka always run in the background?" dialog. */
    @SuppressLint("BatteryLife")    // a media player that must keep playing with the screen off: an accepted use
    fun requestIntent(context: Context): Intent =
        Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${context.packageName}"))

    /** Asks for the exemption; where a phone has no such dialog, opens the system's battery-optimisation list. */
    fun request(context: Context) {
        try {
            context.startActivity(requestIntent(context))
        } catch (_: ActivityNotFoundException) {
            try {
                context.startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
            } catch (_: ActivityNotFoundException) {
                Log.w(TAG, "no battery optimisation settings on this phone")
            }
        }
    }
}

/** The settings screen's battery section: the plain guide, the button (or that it is done), the maker's hint, the docs. */
@Composable
fun BatteryGuideSection(exempt: Boolean, vendorHint: String?, onRequest: () -> Unit) {
    val context = LocalContext.current
    Column {
        Text(stringResource(R.string.battery_explain), style = MaterialTheme.typography.bodyMedium)
        Spacer(Modifier.height(8.dp))
        if (exempt) {
            Text(stringResource(R.string.battery_allowed), style = MaterialTheme.typography.bodyMedium, color = LarkColors.accent)
        } else {
            Button(onClick = onRequest) { Text(stringResource(R.string.battery_allow)) }
        }
        if (vendorHint != null) {
            Spacer(Modifier.height(8.dp))
            Text(vendorHint, style = MaterialTheme.typography.bodySmall, color = LarkColors.muted)
        }
        TextButton(onClick = { ExternalLink.open(BatteryGuide.DOCS_URL) { context.startActivity(it) } }) {
            Text(stringResource(R.string.battery_docs))
        }
    }
}
