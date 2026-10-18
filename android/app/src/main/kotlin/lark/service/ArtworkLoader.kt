package lark.service

import lark.api.LarkApiProtocol
import lark.api.LarkError
import lark.bridge.Item

/**
 * Covers for the now-playing display, as image data: the session gets bytes, never a URL (the server wants the token,
 * which goes only in an `Authorization` header, from [LarkApiProtocol.artwork]). A 404, or an empty answer, is "no
 * cover" (null, which the controller remembers); anything else throws, and the controller asks again later. Signed out
 * there is no API to ask: that throws too.
 */
class ArtworkLoader(private val api: () -> LarkApiProtocol?) {
    suspend fun load(item: Item): ByteArray? {
        val client = api() ?: throw LarkError.SignedOut
        return try {
            client.artwork(item).takeIf { it.isNotEmpty() }
        } catch (e: LarkError.Http) {
            if (e.status == 404) null else throw e
        }
    }
}
