package lark.core

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.shadow.api.Shadow

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class AndroidNetworkStatusRobolectricTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val cm = context.getSystemService(ConnectivityManager::class.java)

    private fun caps(vararg capabilities: Int): NetworkCapabilities {
        val c = Shadow.newInstanceOf(NetworkCapabilities::class.java)
        capabilities.forEach { shadowOf(c).addCapability(it) }
        return c
    }

    private val validated = intArrayOf(NetworkCapabilities.NET_CAPABILITY_INTERNET, NetworkCapabilities.NET_CAPABILITY_VALIDATED)

    @Test fun noNetworkIsAnAnswerAtOnce() {
        shadowOf(cm).setActiveNetworkInfo(null)
        val status = AndroidNetworkStatus(context)
        assertTrue(status.answered)
        assertFalse(status.isOnline)
    }

    @Test fun beforeTheFirstCallbackIsGuessedAndNotAnswered() {
        val status = AndroidNetworkStatus(context)
        assertFalse(status.answered)
        assertTrue(status.isExpensive)
    }

    @Test fun capabilitiesAreMappedAndObserversNotifiedOnChangeOnly() {
        val status = AndroidNetworkStatus(context)
        var changes = 0
        status.observe { changes += 1 }
        val callback = shadowOf(cm).networkCallbacks.single()
        val net = org.robolectric.shadows.ShadowNetwork.newInstance(1)

        callback.onCapabilitiesChanged(net, caps(*validated, NetworkCapabilities.NET_CAPABILITY_NOT_METERED))
        assertTrue(status.answered); assertTrue(status.isOnline); assertFalse(status.isExpensive)
        assertEquals(1, changes)

        callback.onCapabilitiesChanged(net, caps(*validated, NetworkCapabilities.NET_CAPABILITY_NOT_METERED))
        assertEquals(1, changes)                       // the same state again is no change

        callback.onCapabilitiesChanged(net, caps(*validated))   // metered
        assertTrue(status.isExpensive)
        assertEquals(2, changes)

        callback.onLost(net)
        assertFalse(status.isOnline)
        assertEquals(3, changes)
    }
}
