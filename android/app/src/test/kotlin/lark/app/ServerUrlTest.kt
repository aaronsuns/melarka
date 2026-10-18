package lark.app

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ServerUrlTest {
    private fun n(s: String, debug: Boolean = false) = ServerUrl.normalize(s, allowEmulatorHost = debug)?.toString()

    @Test fun normalizesAndValidates() {
        assertEquals("https://music.example.com/", n(" https://music.example.com/ "))
        assertEquals("https://music.example.com:8443/", n("https://music.example.com:8443/x?y"))
        assertEquals("http://mac.local:8080/", n("http://mac.local:8080"))
        assertEquals("http://localhost:8080/", n("http://localhost:8080"))
        assertEquals("https://music.example.com/", n("HTTPS://Music.Example.COM"))
        assertNull(n("http://music.example.com"))   // plain http only for development hosts
        assertNull(n("ftp://music.example.com"))
        assertNull(n("music.example.com"))
        assertNull(n(""))
        assertNull(n("https://user:pw@example.com"))
        assertNull(n("https://"))
        assertNull(n("https://exa mple.com"))
        assertNull(n("https://example.com:99999"))
        assertNull(n("http://evil.example.com/.local"))
        assertNull(n("http://notlocal"))
    }

    @Test fun emulatorHostOnlyInDebugBuilds() {
        assertEquals("http://10.0.2.2:8080/", n("http://10.0.2.2:8080", debug = true))
        assertNull(n("http://10.0.2.2:8080", debug = false))
        assertEquals("https://10.0.2.2/", n("https://10.0.2.2"))
    }

    @Test fun firstLaunchFieldStartsEmpty() {
        assertEquals("", ServerUrl.INITIAL_TEXT)   // no pre-filled server
        assertEquals("https://music.example.com", ServerUrl.PLACEHOLDER)
    }

    @Test fun infoUrl() {
        assertEquals("https://music.example.com/api/v1/info", ServerUrl.infoUrl(ServerUrl.normalize("https://music.example.com/")!!).toString())
    }

    /** The server screen keeps an address only when its `/api/v1/info` answers 200. */
    @Test fun theCheckAsksTheInfoEndpoint() = runBlocking {
        val server = MockWebServer()
        server.start()
        try {
            val url = ServerUrl.normalize("http://localhost:${server.port}/")!!
            server.enqueue(MockResponse().setBody("""{"name":"x"}"""))
            assertTrue(ServerUrl.check(url))
            assertEquals("/api/v1/info", server.takeRequest().path)
            server.enqueue(MockResponse().setResponseCode(404))
            assertFalse(ServerUrl.check(url))
        } finally {
            server.shutdown()
        }
        assertFalse(ServerUrl.check(ServerUrl.normalize("http://localhost:${server.port}/")!!))   // nothing listening
    }
}
