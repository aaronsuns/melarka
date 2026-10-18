package lark.ui

import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import lark.app.R

/**
 * The page could not load (offline, the server down; port of iOS `PageErrorView`): a native screen instead of a blank
 * web view or the browser's error page. The app retries by itself when the network comes back
 * (`AppServices.networkChanged`); Play Offline Favorites opens the offline player.
 */
@Composable
fun CantConnectScreen(
    server: String,
    stayOffline: Boolean,
    hasOfflineFavorites: Boolean,
    onRetry: () -> Unit,
    onSettings: () -> Unit,
    onPlayOfflineFavorites: () -> Unit,
    onStayOffline: (Boolean) -> Unit,
) {
    Column(
        Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background).verticalScroll(rememberScrollState()).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
    ) {
        Icon(LarkIcons.cloudOff, contentDescription = null, tint = LarkColors.muted, modifier = Modifier.size(48.dp))
        Text(stringResource(R.string.cant_connect_title), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold,
            textAlign = TextAlign.Center)
        Text(Uri.parse(server).host ?: server, style = MaterialTheme.typography.bodySmall, color = LarkColors.muted)
        val wide = Modifier.widthIn(min = 220.dp)
        Button(onClick = onRetry, enabled = !stayOffline, modifier = wide) { Text(stringResource(R.string.retry)) }
        OutlinedButton(onClick = onSettings, modifier = wide) { Text(stringResource(R.string.settings)) }
        if (hasOfflineFavorites) {
            OutlinedButton(onClick = onPlayOfflineFavorites, modifier = wide) { Text(stringResource(R.string.play_offline_favorites)) }
        }
        StayOfflineToggle(stayOffline, onStayOffline, Modifier.widthIn(max = 360.dp))
    }
}

/** Stay Offline, with what it does under it (the offline player, the can't-connect screen, Settings). */
@Composable
fun StayOfflineToggle(on: Boolean, onChange: (Boolean) -> Unit, modifier: Modifier = Modifier) {
    Column(modifier) {
        // The whole row is one switch (its label read with it, a tap anywhere toggles).
        Row(Modifier.fillMaxWidth().toggleable(value = on, role = Role.Switch, onValueChange = onChange),
            verticalAlignment = Alignment.CenterVertically) {
            Icon(LarkIcons.airplane, contentDescription = null, tint = if (on) LarkColors.offline else LarkColors.muted)
            Spacer(Modifier.width(12.dp))
            Text(stringResource(R.string.stay_offline), Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
            Switch(checked = on, onCheckedChange = null,
                colors = SwitchDefaults.colors(checkedTrackColor = LarkColors.offline, checkedThumbColor = LarkColors.text))
        }
        Text(stringResource(R.string.stay_offline_explain), style = MaterialTheme.typography.bodySmall, color = LarkColors.muted,
            modifier = Modifier.padding(top = 4.dp))
    }
}
