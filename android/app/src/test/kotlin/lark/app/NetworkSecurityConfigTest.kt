package lark.app

import kotlinx.coroutines.runBlocking
import lark.service.Playback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.w3c.dom.Element
import java.io.File
import java.net.InetAddress
import javax.net.ssl.SSLException
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Who the app trusts (Review focus 3): https with the system's and the user's CAs, for OkHttp and the WebView alike (both
 * follow the network security config); plain http only for the development hosts `ServerUrl` takes. Robolectric does not
 * enforce the config, so its files are checked as written, and the production client is checked to validate certificates.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class NetworkSecurityConfigTest {
    private class Domain(val name: String, val subdomains: Boolean)
    private class Config(val baseCleartext: String, val baseAnchors: List<String>, val cleartext: List<Domain>,
                         val domainAnchors: List<String>, val elements: List<String>)

    /** The unit tests run in the app module's folder. */
    private fun read(path: String): Config {
        val doc = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(File(path))
        val root = doc.documentElement
        assertEquals("network-security-config", root.tagName)
        fun children(e: Element, tag: String) = (0 until e.childNodes.length).map { e.childNodes.item(it) }
            .filterIsInstance<Element>().filter { it.tagName == tag }
        fun anchors(e: Element) = children(e, "trust-anchors").flatMap { children(it, "certificates") }.map { it.getAttribute("src") }
        val base = children(root, "base-config").single()
        val domainConfigs = children(root, "domain-config")
        val cleartext = domainConfigs.filter { it.getAttribute("cleartextTrafficPermitted") == "true" }
            .flatMap { children(it, "domain") }.map { Domain(it.textContent.trim(), it.getAttribute("includeSubdomains") == "true") }
        val all = (0 until doc.getElementsByTagName("*").length).map { (doc.getElementsByTagName("*").item(it) as Element).tagName }
        return Config(base.getAttribute("cleartextTrafficPermitted"), anchors(base), cleartext,
            domainConfigs.flatMap { anchors(it) }.distinct(), all.distinct())
    }

    private fun Config.allowsCleartext(host: String) =
        cleartext.any { host == it.name || (it.subdomains && host.endsWith("." + it.name)) }

    @Test fun releaseTrustsSystemAndUserCasAndAllowsHttpOnlyForDevelopmentHosts() {
        val c = read("src/main/res/xml/network_security_config.xml")
        assertEquals("false", c.baseCleartext)
        assertEquals(listOf("system", "user"), c.baseAnchors)
        assertEquals(listOf("system", "user"), c.domainAnchors)
        assertEquals(listOf("localhost" to false, "local" to true), c.cleartext.map { it.name to it.subdomains })
        // Nothing that would weaken or pin trust: no debug overrides, no pins, no other sources.
        assertEquals(setOf("network-security-config", "base-config", "trust-anchors", "certificates", "domain-config", "domain"), c.elements.toSet())
        assertTrue(c.allowsCleartext("localhost"))
        assertTrue(c.allowsCleartext("mac.local"))
        assertFalse(c.allowsCleartext("10.0.2.2"))
        assertFalse(c.allowsCleartext("music.example.com"))
        assertFalse(c.allowsCleartext("local.example.com"))
        assertFalse(c.allowsCleartext("notlocal"))
    }

    @Test fun debugAddsOnlyTheEmulatorsHost() {
        val release = read("src/main/res/xml/network_security_config.xml")
        val debug = read("src/debug/res/xml/network_security_config.xml")
        assertEquals(release.baseCleartext, debug.baseCleartext)
        assertEquals(release.baseAnchors, debug.baseAnchors)
        assertEquals(release.elements.toSet(), debug.elements.toSet())
        assertEquals(release.cleartext.map { it.name to it.subdomains } + ("10.0.2.2" to false), debug.cleartext.map { it.name to it.subdomains })
    }

    /** The config's http hosts are exactly the ones the server screen takes (`ServerUrl`). */
    @Test fun theConfigMatchesWhatTheServerScreenTakes() {
        val release = read("src/main/res/xml/network_security_config.xml")
        val debug = read("src/debug/res/xml/network_security_config.xml")
        for (host in listOf("localhost", "mac.local", "10.0.2.2", "music.example.com", "192.168.1.5")) {
            assertEquals(host, ServerUrl.normalize("http://$host/", allowEmulatorHost = false) != null, release.allowsCleartext(host))
            assertEquals(host, ServerUrl.normalize("http://$host/", allowEmulatorHost = true) != null, debug.allowsCleartext(host))
        }
    }

    @Test fun theManifestUsesItAndNoCleartextSwitch() {
        val manifest = File("src/main/AndroidManifest.xml").readText()
        assertTrue(manifest.contains("android:networkSecurityConfig=\"@xml/network_security_config\""))
        assertFalse(manifest.contains("usesCleartextTraffic"))
    }

    /** The production client keeps certificate validation: a self-signed server is refused, the same server trusted is fine. */
    @Test fun theProductionClientValidatesCertificates() = runBlocking {
        val cert = HeldCertificate.Builder().addSubjectAlternativeName("127.0.0.1").build()
        val server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(cert).build().sslSocketFactory(), false)
        server.enqueue(MockResponse().setBody("ok")); server.enqueue(MockResponse().setBody("ok"))
        server.start(InetAddress.getByName("127.0.0.1"), 0)
        try {
            val url = "https://127.0.0.1:${server.port}/"
            try {
                LarkHttp.client.newCall(Request.Builder().url(url).build()).execute().close()
                fail("a self-signed certificate was accepted")
            } catch (_: SSLException) {}
            val trusting = HandshakeCertificates.Builder().addTrustedCertificate(cert.certificate).build()
            val control: OkHttpClient = LarkHttp.client.newBuilder()
                .sslSocketFactory(trusting.sslSocketFactory(), trusting.trustManager).build()
            control.newCall(Request.Builder().url(url).build()).execute().use { assertEquals("ok", it.body.string()) }
        } finally { server.shutdown() }
    }

    /** One client for the API, the downloads and the streams: the same trust everywhere. */
    @Test fun theAppGivesThePlayerTheSameClient() {
        assertSame(LarkHttp.client, Playback.httpClient())
        assertNull(LarkHttp.client.certificatePinner.pins.firstOrNull())
        assertNotNull(LarkHttp.client.hostnameVerifier)
    }
}
