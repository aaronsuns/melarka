package lark.app

import lark.bridge.NativeEvent

/**
 * The native offline player over a page that could not load, as the app services see it (iOS `OfflinePlayerModel`'s
 * hooks): it hears every engine event, and the page's retries. The screen and its model are Task 12's
 * (`AppServices.makeOfflinePlayer`); until then [Placeholder] stands in.
 */
interface OfflinePlayer {
    fun receive(event: NativeEvent) {}
    fun retryFailed() {}
    fun retryStarted() {}

    object Placeholder : OfflinePlayer
}
