package lark.core

import org.junit.Assert.assertEquals
import org.junit.Test

class AndroidNetworkStatusTest {
    @Test fun validatedUnmeteredIsOnlineAndCheap() =
        assertEquals(NetworkState(online = true, expensive = false), classifyNetwork(true, notMetered = true, restrictBackground = false))

    @Test fun meteredIsExpensive() =
        assertEquals(NetworkState(online = true, expensive = true), classifyNetwork(true, notMetered = false, restrictBackground = false))

    @Test fun dataSaverMakesAnUnmeteredNetworkExpensive() =
        assertEquals(NetworkState(online = true, expensive = true), classifyNetwork(true, notMetered = true, restrictBackground = true))

    @Test fun notValidatedIsOffline() =
        assertEquals(false, classifyNetwork(false, notMetered = true, restrictBackground = false).online)
}
