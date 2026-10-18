package lark.app

import lark.bridge.NativeEvent

/**
 * The native offline player over a page that could not load, as the app services see it (iOS `OfflinePlayerModel`'s
 * hooks): it hears every engine event, and the page's retries. The model is `lark.ui.OfflinePlayerModel`
 * (`AppServices.makeOfflinePlayer`), shown by `OfflinePlayerScreen`.
 */
interface OfflinePlayer {
    fun receive(event: NativeEvent)
    fun retryFailed()
    fun retryStarted()
}
