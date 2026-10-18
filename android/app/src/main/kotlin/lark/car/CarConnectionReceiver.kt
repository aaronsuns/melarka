package lark.car

import android.bluetooth.BluetoothDevice
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.content.IntentCompat

/**
 * Below Android 12 (no presence observation for companion devices): car autoplay hears the car's Bluetooth connect and
 * disconnect (`ACL_CONNECTED`/`ACL_DISCONNECTED`, which apps may still receive in the manifest). Enabled below API 31
 * only (`@bool/car_connection_receiver`). Android 8–11 allow a foreground service start from the background, so the
 * media service comes up; what a phone maker's power management blocks (a force-stopped app gets no broadcasts) is
 * what the Settings guidance is for.
 */
class CarConnectionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val device = IntentCompat.getParcelableExtra(intent, BluetoothDevice.EXTRA_DEVICE, BluetoothDevice::class.java) ?: return
        val address = device.address ?: return
        val car = CarAutoplay.of(context)
        when (intent.action) {
            BluetoothDevice.ACTION_ACL_CONNECTED -> car.deviceAppeared(address)
            BluetoothDevice.ACTION_ACL_DISCONNECTED -> car.deviceDisappeared(address)
        }
    }
}
