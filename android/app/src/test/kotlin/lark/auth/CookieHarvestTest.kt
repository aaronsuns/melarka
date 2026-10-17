package lark.auth

import kotlinx.coroutines.test.runTest
import lark.testing.FakeCookies
import lark.testing.MemorySecretStore
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test

class CookieHarvestTest {
    private lateinit var server: MockWebServer
    private val secrets = MemorySecretStore()
    private val cookies = FakeCookies()
    private lateinit var url: String

    @Before fun setUp() { server = MockWebServer().also { it.start() }; url = server.url("/").toString() }
    @After fun tearDown() { server.shutdown() }

    @Test fun verifiesBeforeStoring() = runTest {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"not signed in","code":"not_signed_in"}"""))
        val a = AuthStore(secrets, cookies); var changes = 0
        a.onChange = { changes++ }
        cookies.value = "STALE"
        assertFalse(a.harvest(url))
        assertNull(a.token); assertNull(secrets.get("token")); assertEquals(0, changes)
        val r = server.takeRequest()
        assertEquals("/api/v1/me", r.path); assertEquals("Bearer STALE", r.getHeader("Authorization"))
    }

    @Test fun anUnreachableServerStoresNothingEither() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))
        val a = AuthStore(secrets, cookies); cookies.value = "T1"
        assertFalse(a.harvest(url)); assertNull(a.token)
        server.shutdown()
        assertFalse(a.harvest(url)); assertNull(a.token)
    }

    @Test fun anUnreadableAnswerStoresNothing() = runTest {
        server.enqueue(MockResponse().setBody("<html>captive portal</html>"))
        val a = AuthStore(secrets, cookies); cookies.value = "T1"
        assertFalse(a.harvest(url)); assertNull(a.token)
    }

    @Test fun aRejectedTokenIsNeverStored() = runTest {
        server.enqueue(MockResponse().setResponseCode(401)); server.enqueue(MockResponse().setResponseCode(401))
        val a = AuthStore(secrets, cookies); cookies.value = "STALE"
        a.harvest(url); a.harvest(url)
        assertNull(a.token)
    }

    @Test fun aTokenRevokedWhileBeingVerifiedIsNotStored() = runTest {
        val a = AuthStore(secrets, cookies); cookies.value = "T1"
        server.enqueue(MockResponse().setBody("""{"id":1}"""))
        a.harvest(url); a.clear()
        // The verification of the next harvest of T1 never starts: it was revoked.
        a.harvest(url)
        assertEquals(1, server.requestCount); assertNull(a.token)
    }

    @Test fun cookieHeaderParsing() {
        assertEquals("abc", parseCookie("a=b; lark_token=abc; c=d", "lark_token"))
        assertEquals("abc", parseCookie("lark_token=abc", "lark_token"))
        assertEquals("a=b==", parseCookie("lark_token=a=b==", "lark_token"))   // only the first = splits
        assertNull(parseCookie("xlark_token=abc; lark_token2=1", "lark_token"))
        assertNull(parseCookie("lark_token=", "lark_token"))
        assertNull(parseCookie("", "lark_token"))
        assertNull(parseCookie(null, "lark_token"))
        // CookieManager lists the longest Path first: the server's cookie (Path=/) comes last, so it wins over a script-set one.
        assertEquals("REAL", parseCookie("lark_token=SUB; lark_token=REAL", "lark_token"))
    }
}
