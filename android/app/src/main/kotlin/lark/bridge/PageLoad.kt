package lark.bridge

/**
 * Which main-frame load outcomes mean "the page is not there" (the native error screen), and which are only a
 * navigation being replaced (iOS `Bridge/PageLoad.swift`).
 */
object PageLoad {
    /**
     * A `WebViewClient.onReceivedError` for the main frame. `net::ERR_ABORTED` is another load replacing this one (a
     * new navigation, a stop): not a failure, as iOS `NSURLErrorCancelled`. A navigation the shell cancels in
     * `shouldOverrideUrlLoading` raises no error at all (iOS's WebKit 102). [errorCode] is kept for the log's sake.
     */
    @Suppress("UNUSED_PARAMETER")
    fun isFailure(errorCode: Int, description: String?): Boolean = description?.trim() != "net::ERR_ABORTED"

    /** A 5xx for the page itself: the proxy is up but the server is not. */
    fun isFailure(status: Int): Boolean = status >= 500
}
