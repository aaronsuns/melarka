package lark.app

import android.content.Intent
import android.net.Uri

/**
 * A link the page may not load (another origin, `mailto:`, `tel:`, `intent:`…) opened outside the app: always a plain,
 * browsable view of the URL. An `intent:` URL is never parsed into the intent it describes (which could name any
 * component), so the system resolves it like any other link. Nothing the system throws (no app for it, a file URI, a
 * security check) escapes: the tap then does nothing, as on iOS.
 */
object ExternalLink {
    fun intent(url: String): Intent = Intent(Intent.ACTION_VIEW, Uri.parse(url)).addCategory(Intent.CATEGORY_BROWSABLE)

    /** True when [start] took the intent. */
    fun open(url: String, start: (Intent) -> Unit): Boolean = try {
        start(intent(url))
        true
    } catch (_: RuntimeException) {   // ActivityNotFoundException, FileUriExposedException, SecurityException, …
        false
    }
}
