package lark.app

import android.content.ActivityNotFoundException
import android.content.Intent
import android.os.FileUriExposedException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Opening a link outside the app: always a plain browsable view of the URL, and nothing the system throws escapes. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ExternalLinkTest {
    @Test fun aPlainBrowsableViewEvenForIntentUrls() {
        val url = "intent://scan/#Intent;scheme=zxing;component=evil/.Steal;S.browser_fallback_url=x;end"
        val i = ExternalLink.intent(url)
        assertEquals(Intent.ACTION_VIEW, i.action)
        assertEquals(url, i.dataString)                     // never parsed into the intent it describes
        assertTrue(i.hasCategory(Intent.CATEGORY_BROWSABLE))
        assertNull(i.component)
        assertNull(i.selector)
        assertNull(i.`package`)
    }

    @Test fun whatTheSystemThrowsIsSwallowed() {
        for (e in listOf(ActivityNotFoundException(), FileUriExposedException("file"), SecurityException("no"), IllegalArgumentException("x"))) {
            assertFalse(ExternalLink.open("https://www.youtube.com/watch?v=x") { throw e })
        }
        var started: Intent? = null
        assertTrue(ExternalLink.open("mailto:a@b.c") { started = it })
        assertEquals("mailto:a@b.c", started?.dataString)
    }
}
