package lark.player

import android.net.Uri
import java.io.File

/**
 * Where an item's audio comes from: a file in the offline cache, or the server. A remote source carries the bearer
 * token only for the backend's data source, which sends it as an `Authorization` header: it is never part of the
 * [Uri], never logged, and never reaches a `MediaItem`'s metadata or tag. (`toString` leaves it out too.)
 */
sealed class MediaSource {
    data class File(val file: java.io.File) : MediaSource()

    class Remote(val uri: Uri, val token: String) : MediaSource() {
        override fun equals(other: Any?) = other is Remote && other.uri == uri && other.token == token
        override fun hashCode() = 31 * uri.hashCode() + token.hashCode()
        override fun toString() = "Remote($uri)"
    }
}
