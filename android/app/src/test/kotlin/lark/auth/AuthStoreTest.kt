package lark.auth

import kotlinx.coroutines.test.runTest
import lark.testing.FakeCookies
import lark.testing.MemorySecretStore
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test

/** Port of AuthStoreTests; the cookie-attribute tests (host, domain, HttpOnly, path) have no Android equivalent: `CookieManager` already scopes by URL. */
class AuthStoreTest {
    private lateinit var server: MockWebServer
    private val secrets = MemorySecretStore()
    private val cookies = FakeCookies()
    private lateinit var url: String

    @Before fun setUp() { server = MockWebServer().also { it.start() }; url = server.url("/").toString() }
    @After fun tearDown() { server.shutdown() }

    private fun store(s: MemorySecretStore = secrets) = AuthStore(s, cookies)
    private fun meOk(id: Int = 3) = server.enqueue(MockResponse().setBody("""{"id":$id,"username":"u","role":"user"}"""))

    @Test fun harvestTakesTheCookieAndTheUser() = runTest {
        val a = store(); meOk(3)
        cookies.value = "T1"
        assertEquals(true, a.harvest(url))
        assertEquals("T1", a.token); assertEquals("T1", secrets.get("token")); assertEquals(3L, a.userId)
        assertEquals(listOf(url), cookies.asked)
    }

    @Test fun userIdFromThePageIsKeptWhenThereIsNoCookie() = runTest {
        val a = store()
        a.harvest(url, userId = 7)
        assertNull(a.token); assertEquals(7L, a.userId); assertEquals("7", secrets.get("userId"))
    }

    @Test fun clearRemovesTokenAndNotifiesOnce() = runTest {
        val a = store(); val changes = mutableListOf<String?>()
        a.onChange = { changes += it }
        meOk(); cookies.value = "T1"; a.harvest(url)
        a.clear(); a.clear()
        assertNull(a.token); assertNull(a.userId); assertNull(secrets.get("token")); assertNull(secrets.get("userId"))
        assertEquals(listOf<String?>("T1", null), changes)
    }

    @Test fun missingCookieKeepsTokenAndSameTokenIsNoChange() = runTest {
        val a = store(); var changes = 0
        a.onChange = { changes++ }
        meOk(); cookies.value = "T1"; a.harvest(url, userId = 3)
        a.harvest(url)                       // the same token: no request, no change
        cookies.value = null
        a.harvest(url)                       // a flaky empty cookie jar must not sign native out
        cookies.value = ""
        a.harvest(url)
        assertEquals("T1", a.token); assertEquals(3L, a.userId); assertEquals(1, changes)
        assertEquals(1, server.requestCount)
    }

    @Test fun tokenSurvivesRelaunch() = runTest {
        meOk(3); cookies.value = "T1"; store().harvest(url)
        val again = store()
        assertEquals("T1", again.token); assertEquals(3L, again.userId)
    }

    @Test fun clearedTokenIsNotHarvestedAgain() = runTest {
        // A 401 or a logout racing the server's Set-Cookie leaves the old cookie in place for a moment.
        val a = store(); val changes = mutableListOf<String?>()
        a.onChange = { changes += it }
        meOk(); cookies.value = "T1"; a.harvest(url)
        a.clear()
        a.harvest(url)                       // T1 again: refused without asking the server
        assertNull(a.token); assertEquals(1, server.requestCount)
        meOk(); cookies.value = "T2"; a.harvest(url)   // a fresh login
        assertEquals("T2", a.token)
        assertEquals(listOf<String?>("T1", null, "T2"), changes)
    }
}
