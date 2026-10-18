package lark.app

import androidx.test.core.app.ApplicationProvider
import lark.cache.FavoritesRefresh
import lark.service.Playback
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** The application wires the process-wide seams at once and builds the services only when something needs them. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class LarkApplicationTest {
    private val app: LarkApplication get() = ApplicationProvider.getApplicationContext()

    @Test fun theSeamsAreSetAndTheServicesWaitForTheirFirstUse() {
        assertSame(LarkHttp.client, Playback.httpClient())
        assertNotNull(FavoritesRefresh.provider)
        assertNotNull(Playback.onCreated)
        assertFalse(app.servicesBuilt)               // a process started for the background work alone builds them in the worker
    }

    /** With no server, the services hold no engine: the playback host's engine is left alone. */
    @Test fun theServicesBuildWithNoServer() {
        val s = app.services
        assertTrue(app.servicesBuilt)
        assertSame(s, app.services)
        assertTrue(s.serverUrl == null && s.engine == null)
    }
}
