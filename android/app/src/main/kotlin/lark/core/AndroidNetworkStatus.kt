package lark.core

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.Handler
import android.os.Looper

/** What the default network's capabilities and the Data Saver setting say. */
internal data class NetworkState(val online: Boolean, val expensive: Boolean)

/**
 * Online means the default network has the INTERNET capability, like iOS `NWPath.status == .satisfied`. VALIDATED is
 * not required: a self-hosted server on a Wi-Fi without WAN must stay reachable. Expensive means metered, or Data Saver
 * (`restrictBackgroundStatus == ENABLED`) is on: the user asked for no bulk downloads.
 */
internal fun classifyNetwork(internet: Boolean, notMetered: Boolean, restrictBackground: Boolean) =
    NetworkState(online = internet, expensive = !notMetered || restrictBackground)

internal fun classifyNetwork(caps: NetworkCapabilities?, restrictBackground: Boolean) =
    if (caps == null) NetworkState(online = false, expensive = restrictBackground)
    else classifyNetwork(
        internet = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET),
        notMetered = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED),
        restrictBackground = restrictBackground,
    )

class AndroidNetworkStatus(context: Context) : NetworkStatus {
    private val cm = context.applicationContext.getSystemService(ConnectivityManager::class.java)
    private val observers = mutableListOf<() -> Unit>()

    override var isOnline = true
        private set

    /**
     * Expensive until the first callback: a sync at launch must not run on a metered network before the system
     * has answered. The first callback on Wi-Fi is a change, so a sync skipped meanwhile runs then.
     */
    override var isExpensive = true
        private set
    override var answered = false
        private set

    /** The current default network: a loss of any other one (a stale loss after a switch) is ignored. */
    private var current: Network? = null

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
            current = network
            update(classifyNetwork(caps, restricted()))
        }

        override fun onLost(network: Network) {
            if (network != current) return
            current = null
            update(classifyNetwork(null, restricted()))
        }
    }

    init {
        // Never unregistered: this object lives as long as the app.
        cm.registerDefaultNetworkCallback(callback, Handler(Looper.getMainLooper()))
        // No default network at all: the callback may never fire, and "offline" is a sure answer.
        if (cm.activeNetwork == null) update(classifyNetwork(null, restricted()))
    }

    private fun restricted() = cm.restrictBackgroundStatus == ConnectivityManager.RESTRICT_BACKGROUND_STATUS_ENABLED

    private fun update(state: NetworkState) {
        if (answered && isOnline == state.online && isExpensive == state.expensive) return
        answered = true
        isOnline = state.online
        isExpensive = state.expensive
        observers.toList().forEach { it() }
    }

    override fun observe(fn: () -> Unit) { observers.add(fn) }
}
