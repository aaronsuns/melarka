package lark.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Nothing of Melarka leaves the phone in a backup or a device-to-device transfer (Android 12+ ignores `allowBackup`
 * for a transfer): not the WebView's cookies (the session), the secrets file, the queues or the cache. The unit tests
 * run in the app module's folder, so the files are checked as written.
 */
class BackupRulesTest {
    private val android = "http://schemas.android.com/apk/res/android"

    private fun parse(path: String) = DocumentBuilderFactory.newInstance().apply { isNamespaceAware = true }
        .newDocumentBuilder().parse(File(path)).documentElement

    private fun Element.children(tag: String? = null) = (0 until childNodes.length).map { childNodes.item(it) }
        .filterIsInstance<Element>().filter { tag == null || it.tagName == tag }

    /** Every domain a rule may name, excluded whole (path "."), and nothing included. */
    private fun assertExcludesEverything(section: Element, domains: List<String>) {
        assertTrue("${section.tagName}: nothing included", section.children("include").isEmpty())
        val excluded = section.children("exclude").filter { it.getAttribute("path") == "." }.map { it.getAttribute("domain") }
        assertEquals("${section.tagName}: every domain excluded", domains.sorted(), excluded.sorted())
    }

    @Test fun theManifestNamesTheRulesAndAllowsNoBackup() {
        val app = parse("src/main/AndroidManifest.xml").children("application").single()
        assertEquals("false", app.getAttributeNS(android, "allowBackup"))
        assertEquals("@xml/data_extraction_rules", app.getAttributeNS(android, "dataExtractionRules"))
        assertEquals("@xml/backup_rules", app.getAttributeNS(android, "fullBackupContent"))
    }

    @Test fun cloudBackupAndDeviceTransferExcludeEverything() {
        val root = parse("src/main/res/xml/data_extraction_rules.xml")
        assertEquals("data-extraction-rules", root.tagName)
        val domains = listOf("root", "file", "database", "sharedpref", "external",
            "device_root", "device_file", "device_database", "device_sharedpref")
        assertEquals(listOf("cloud-backup", "device-transfer"), root.children().map { it.tagName })
        root.children().forEach { assertExcludesEverything(it, domains) }
    }

    @Test fun fullBackupBeforeAndroid12ExcludesEverything() {
        val root = parse("src/main/res/xml/backup_rules.xml")
        assertEquals("full-backup-content", root.tagName)
        assertExcludesEverything(root, listOf("root", "file", "database", "sharedpref", "external",
            "device_root", "device_file", "device_database", "device_sharedpref"))
    }
}
