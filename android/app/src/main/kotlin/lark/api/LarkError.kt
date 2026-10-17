package lark.api

/** What an API call can fail with besides the network's own `IOException`. Messages never carry the token. */
sealed class LarkError(message: String) : Exception(message) {
    /** No token: nothing was sent. */
    data object SignedOut : LarkError("signed out")
    /** The server answered 401. */
    data object Unauthorized : LarkError("unauthorized")
    /** Refused locally, nothing was sent. */
    data object BadRequest : LarkError("bad request")
    /** Any other non-2xx; [code] is the server's stable error code. */
    data class Http(val status: Int, val code: String?) : LarkError("http $status${code?.let { " $it" } ?: ""}")
    /** 2xx with a body or type we cannot use. */
    data object BadResponse : LarkError("bad response")
}
