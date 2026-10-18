package lark.testing

import lark.auth.WebData

/** The web view's cookie jar and site data for one server: [token] is its `lark_token` cookie (null: none). */
class FakeWebData(var token: String? = null) : WebData {
    val asked = mutableListOf<String>()
    val deleted = mutableListOf<String>()
    val removed = mutableListOf<String>()
    /** Runs when the session cookie is deleted (an order log). */
    var onDelete: () -> Unit = {}

    override fun larkToken(url: String): String? { asked += url; return token }
    override fun deleteSessionCookie(serverUrl: String) { deleted += serverUrl; token = null; onDelete() }
    override fun removeWebsiteData(serverUrl: String) { removed += serverUrl; token = null }
}
