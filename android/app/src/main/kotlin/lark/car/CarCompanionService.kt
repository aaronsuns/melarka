package lark.car

import android.companion.CompanionDeviceService
import androidx.annotation.RequiresApi

/**
 * Android 12+: the system binds this service when the car Melarka is associated with connects (its presence is
 * observed, [CarAutoplay.associated]) and when it goes; car autoplay runs from here, even with Melarka in the
 * background or not running. Android 13+ calls these through `onDeviceAppeared(AssociationInfo)` with the car's address.
 */
@RequiresApi(31)
class CarCompanionService : CompanionDeviceService() {
    @Deprecated("Android 13+ calls it from onDeviceAppeared(AssociationInfo)")
    override fun onDeviceAppeared(address: String) {
        CarAutoplay.of(this).deviceAppeared(address)
    }

    @Deprecated("Android 13+ calls it from onDeviceDisappeared(AssociationInfo)")
    override fun onDeviceDisappeared(address: String) {
        CarAutoplay.of(this).deviceDisappeared(address)
    }
}
