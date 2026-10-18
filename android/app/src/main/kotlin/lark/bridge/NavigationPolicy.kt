package lark.bridge

import java.net.URI

/** What the web view does with a navigation (iOS `NavigationPolicy`, `Bridge/BridgeController.swift`). */
enum class NavigationPolicy {
    allow, cancel, openExternally,
    /** A same-origin new window: there are no tabs, so it loads in the one web view. */
    loadInMainFrame;

    enum class Target { mainFrame, subframe, newWindow }

    companion object {
        /**
         * - main frame: only the server's own http(s) origin (and same-origin `blob:`, `about:blank`) loads; other
         *   http(s) origins and app schemes open outside the app; `data:`, `javascript:` and foreign blobs are dropped,
         *   so no page content an attacker controls can replace the Melarka UI.
         * - new window (`target=_blank`, `window.open`): only same-origin http(s) loads, in the main frame.
         * - subframes (the 视频 YouTube embed): web and in-page schemes stay in the page; app schemes need a tap.
         * A string with no scheme is dropped.
         */
        fun decide(url: String, server: String, target: Target, userActivated: Boolean): NavigationPolicy {
            val scheme = schemeOf(url) ?: return cancel
            val isWeb = scheme == "http" || scheme == "https"
            val inPage = scheme in IN_PAGE
            val sameOrigin = isWeb && webOrigin(url)?.let { BridgeController.originMatches(it.scheme, it.host, it.port, server) } == true
            // Apps (mailto:, tel:, intent:…) launch only from the main frame or on a tap.
            val external = if (target == Target.mainFrame || userActivated) openExternally else cancel

            return when (target) {
                Target.subframe -> if (isWeb || inPage) allow else external
                Target.newWindow -> when {
                    sameOrigin -> loadInMainFrame
                    isWeb -> openExternally
                    inPage -> cancel
                    else -> external
                }
                Target.mainFrame -> when {
                    sameOrigin -> allow
                    isWeb -> openExternally
                    scheme == "about" -> if (url.lowercase() == "about:blank") allow else cancel
                    // blob:<origin>/<uuid>: allowed only when the inner origin is the server's.
                    scheme == "blob" -> {
                        val inner = webOrigin(url.substring("blob:".length))
                        if (inner != null && BridgeController.originMatches(inner.scheme, inner.host, inner.port, server)) allow else cancel
                    }
                    scheme == "data" || scheme == "javascript" -> cancel
                    else -> external
                }
            }
        }

        private val IN_PAGE = setOf("about", "blob", "data", "javascript")
        private val SCHEME = Regex("^[A-Za-z][A-Za-z0-9+.-]*$")

        /** The lowercased scheme, or null when the string has none. */
        internal fun schemeOf(url: String): String? {
            val i = url.indexOf(':')
            if (i <= 0) return null
            val s = url.substring(0, i)
            return if (SCHEME.matches(s)) s.lowercase() else null
        }

        internal class Origin(val scheme: String, val host: String, val port: Int)

        /**
         * The origin of a hierarchical URL (`scheme://authority…`): the authority ends at the first `/`, `?`, `#` or `\`
         * (a browser reads `\` as `/` in http(s) URLs), the host is what follows the last `@`, and the authority must
         * parse as one. Anything else has no origin, so it is never taken for the server.
         */
        internal fun webOrigin(url: String): Origin? {
            val scheme = schemeOf(url) ?: return null
            val rest = url.substring(scheme.length + 1)
            if (!rest.startsWith("//")) return null
            val after = rest.substring(2)
            val end = after.indexOfAny(charArrayOf('/', '?', '#', '\\')).let { if (it < 0) after.length else it }
            val authority = after.substring(0, end).substringAfterLast('@')
            if (authority.isEmpty()) return null
            return try {
                val u = URI("$scheme://$authority/")
                val host = u.host?.lowercase()?.takeIf { it.isNotEmpty() } ?: return null
                Origin(scheme, host, u.port)
            } catch (_: Exception) {
                null
            }
        }
    }
}
