package lark.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import lark.app.AppServices
import lark.app.R
import lark.car.CarAutoplay

/**
 * Car autoplay's setup as the Activity drives it: the permission (Nearby devices, asked only when it is turned on), the
 * list of paired cars, and the system's confirmation (its answer reaches `MainActivity.onActivityResult`). [refresh] is
 * bumped when something outside the screen changed (an answer came back, the Activity resumed).
 */
class SettingsHost(
    /** Asks for the permissions car autoplay needs; true: asked (the answer comes later). */
    val requestCarPermissions: () -> Boolean = { false },
    /** Asks Android to associate this car (null: let the system search nearby). */
    val associate: (String?) -> Unit = {},
    /** The phone's paired Bluetooth devices. */
    val pairedCars: () -> List<CarAutoplay.Car> = { emptyList() },
    /** Asks Android to exempt Melarka from battery optimisation. */
    val requestBatteryExemption: () -> Unit = {},
    val batteryExempt: () -> Boolean = { false },
) {
    var picking by mutableStateOf(false)
    var permissionDenied by mutableStateOf(false)
    var refresh by mutableIntStateOf(0)

    /** The permission's answer (`onRequestPermissionsResult`). */
    fun permissionsAnswered(granted: Boolean) {
        permissionDenied = !granted
        picking = granted
    }
}

/**
 * Melarka's own settings (port of iOS `SettingsView`; the web's settings open it through `openSettings`, as does the
 * can't-connect screen): server, offline cache, Stay Offline, car autoplay, background playback, about.
 */
@Composable
fun SettingsScreen(
    services: AppServices,
    car: CarAutoplay?,
    host: SettingsHost,
    /** The services' state, passed in so the screen recomposes when it changes (`AppServices.observe`). */
    stayOffline: Boolean,
    changingServer: Boolean,
    /** `AppServices.cacheChanges`: the usage is read again when the cache changes (a sync storing songs, a clear). */
    cacheChanges: Int,
    onDone: () -> Unit,
) {
    val context = LocalContext.current
    val model = remember(services, car) { SettingsModel(services, context, car) }
    var confirmClear by remember { mutableStateOf(false) }
    var confirmServer by remember { mutableStateOf(false) }
    var exempt by remember { mutableStateOf(host.batteryExempt()) }
    LaunchedEffect(host.refresh, cacheChanges) {
        model.refresh()
        exempt = host.batteryExempt()
    }
    val busy = changingServer          // signing out of the old server: nothing else meanwhile

    Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background)) {
        Column(Modifier.fillMaxSize()) {
            Row(Modifier.fillMaxWidth().background(LarkColors.material).padding(start = 16.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically) {
                Text(stringResource(R.string.settings_title), Modifier.weight(1f).semantics { heading() },
                    style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                TextButton(onClick = onDone, enabled = !busy) { Text(stringResource(R.string.done)) }
            }
            Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp).testTag("settings")) {
                Section(stringResource(R.string.settings_server)) {
                    Labeled(stringResource(R.string.settings_address), model.server)
                    Divider()
                    TextButton(onClick = { confirmServer = true }, enabled = !busy) {
                        Text(stringResource(R.string.change_server), color = LarkColors.danger)
                    }
                }
                Section(stringResource(R.string.offline_cache), footer = stringResource(R.string.cache_footer)) {
                    Labeled(stringResource(R.string.cache), model.usageText)
                    Divider()
                    Text(stringResource(R.string.cache_limit), Modifier.padding(top = 10.dp), style = MaterialTheme.typography.bodyLarge)
                    // Wrapping, not squeezed: on a 320 dp phone four chips do not fit in a row ("10 GB" broke per letter).
                    FlowRow(Modifier.padding(vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        for (gb in SettingsModel.capChoices) {
                            FilterChip(selected = model.capGB == gb, onClick = { model.setCap(gb) }, enabled = !busy,
                                label = { Text("$gb GB") },
                                colors = FilterChipDefaults.filterChipColors(selectedContainerColor = LarkColors.accent,
                                    selectedLabelColor = Color.Black))
                        }
                    }
                    Divider()
                    TextButton(onClick = { model.syncNow() }, enabled = model.signedIn && !busy) { Text(stringResource(R.string.sync_now)) }
                    TextButton(onClick = { confirmClear = true }, enabled = model.signedIn && !busy) {
                        Text(stringResource(R.string.clear_cache), color = if (model.signedIn) LarkColors.danger else LarkColors.muted)
                    }
                }
                Section(null) {
                    StayOfflineToggle(stayOffline, { services.setStayOffline(it) }, Modifier.padding(vertical = 8.dp))
                }
                Section(stringResource(R.string.car_autoplay_title)) {
                    CarAutoplaySection(model, car, host, exempt, busy) { services.runIntent { model.testShuffle() } }
                }
                Section(stringResource(R.string.battery_title)) {
                    Box(Modifier.padding(vertical = 8.dp)) {
                        BatteryGuideSection(exempt, SettingsModel.vendorHint(context)) { host.requestBatteryExemption() }
                    }
                }
                Section(stringResource(R.string.about)) {
                    Labeled(stringResource(R.string.version), SettingsModel.versionText)
                }
                model.message?.let {
                    Text(it, Modifier.padding(vertical = 12.dp), style = MaterialTheme.typography.bodySmall, color = LarkColors.muted)
                }
                Spacer(Modifier.height(32.dp))
            }
        }
        if (busy) {
            Column(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.5f)).clickable(enabled = true, onClick = {}),
                horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                CircularProgressIndicator()
                Spacer(Modifier.height(12.dp))
                Text(stringResource(R.string.signing_out))
            }
        }
    }

    if (confirmClear) {
        Confirm(stringResource(R.string.clear_cache_confirm), stringResource(R.string.clear_cache_explain), stringResource(R.string.clear_cache),
            onConfirm = { confirmClear = false; model.clearCache() }, onDismiss = { confirmClear = false })
    }
    if (confirmServer) {
        Confirm(stringResource(R.string.change_server_confirm), stringResource(R.string.change_server_explain),
            stringResource(R.string.change_server),
            onConfirm = { confirmServer = false; services.runAppTask { model.changeServer() } }, onDismiss = { confirmServer = false })
    }
    if (host.picking) CarPicker(host)
}

/**
 * On: pick the car (the permission first, only now); the system confirms it once. Then its name, another car, the test
 * button (exactly the Shuffle Favorites shortcut), what the last connection did, and the notes for this phone.
 */
@Composable
private fun CarAutoplaySection(model: SettingsModel, car: CarAutoplay?, host: SettingsHost, exempt: Boolean, busy: Boolean,
                               onTest: () -> Unit) {
    val enabled = car != null && !busy
    // The whole row is one switch (its label read with it, a tap anywhere toggles).
    Row(Modifier.fillMaxWidth().toggleable(value = model.carEnabled, enabled = enabled, role = Role.Switch, onValueChange = { on ->
        if (on) {
            host.permissionDenied = false
            if (!host.requestCarPermissions()) host.picking = true
        } else {
            model.turnOffCar()
        }
    }).padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(stringResource(R.string.car_autoplay_title), Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
        Switch(checked = model.carEnabled, enabled = enabled, onCheckedChange = null)
    }
    Text(stringResource(R.string.car_autoplay_explain), style = MaterialTheme.typography.bodySmall, color = LarkColors.muted)
    if (host.permissionDenied) {
        Text(stringResource(R.string.car_autoplay_permission), Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodySmall,
            color = LarkColors.danger)
    }
    model.carName?.let { name ->
        Text(stringResource(R.string.car_autoplay_car, name), Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyLarge)
        TextButton(onClick = { host.picking = true }, enabled = !busy) { Text(stringResource(R.string.car_autoplay_change)) }
    }
    model.carResult?.let {
        Text(stringResource(R.string.car_autoplay_last, it), style = MaterialTheme.typography.bodySmall, color = LarkColors.muted)
    }
    if (SettingsModel.oldAndroid) {
        Text(stringResource(R.string.car_autoplay_old_android), Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodySmall,
            color = LarkColors.muted)
    }
    if (!exempt) {
        Text(stringResource(R.string.car_autoplay_hint_generic), Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodySmall,
            color = LarkColors.muted)
    }
    Divider()
    TextButton(onClick = onTest, enabled = !busy) { Text(stringResource(R.string.car_autoplay_test)) }
}

/** The paired cars to choose from, or the system's own search nearby. */
@Composable
private fun CarPicker(host: SettingsHost) {
    val cars = remember { host.pairedCars() }
    AlertDialog(
        onDismissRequest = { host.picking = false },
        title = { Text(stringResource(R.string.car_autoplay_choose)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                if (cars.isEmpty()) Text(stringResource(R.string.car_autoplay_no_paired), color = LarkColors.muted)
                for (c in cars) {
                    Text(c.name, Modifier.fillMaxWidth().clickable { host.picking = false; host.associate(c.address) }.padding(vertical = 12.dp),
                        style = MaterialTheme.typography.bodyLarge)
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { host.picking = false; host.associate(null) }) { Text(stringResource(R.string.car_autoplay_nearby)) }
        },
        dismissButton = { TextButton(onClick = { host.picking = false }) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun Confirm(title: String, message: String, action: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(message) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(action, color = LarkColors.danger) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun Section(title: String?, footer: String? = null, content: @Composable ColumnScope.() -> Unit) {
    Column(Modifier.padding(top = 20.dp)) {
        if (title != null) {
            Text(title, Modifier.padding(start = 4.dp, bottom = 6.dp).semantics { heading() },
                style = MaterialTheme.typography.titleSmall, color = LarkColors.muted)
        }
        Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(LarkColors.surface).padding(horizontal = 12.dp, vertical = 4.dp),
            content = content)
        if (footer != null) {
            Text(footer, Modifier.padding(start = 4.dp, top = 6.dp), style = MaterialTheme.typography.bodySmall, color = LarkColors.muted)
        }
    }
}

@Composable
private fun Labeled(label: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.bodyLarge)
        Spacer(Modifier.width(16.dp))
        Text(value, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, color = LarkColors.muted,
            textAlign = TextAlign.End)
    }
}

@Composable
private fun Divider() = HorizontalDivider(color = LarkColors.separator)
