package lark.app

import okhttp3.OkHttpClient

/**
 * The app's one HTTP client: the API, downloads, covers, the `/me` check and the player's streams (`Playback.httpClient`).
 * Its trust is the platform's, which follows the network security config (`res/xml/network_security_config.xml`): the
 * system's and the user's CAs, the same as the WebView's. Certificate and host-name checks are OkHttp's own and are never
 * replaced here. Callers derive their own timeouts with `newBuilder()` (sharing its connections).
 */
object LarkHttp {
    val client: OkHttpClient by lazy { OkHttpClient.Builder().build() }
}
