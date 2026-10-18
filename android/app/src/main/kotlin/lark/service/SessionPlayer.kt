package lark.service

import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.ForwardingSimpleBasePlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import lark.bridge.Item
import lark.bridge.RepeatMode

/**
 * The player the media session shows: the engine's view of playback, over the app-wide ExoPlayer the engine drives.
 *
 * - One item, the engine's current one, with what [NowPlayingController] shows: its title is the car-lyrics line while
 *   there is one (the item's own metadata keeps the song, so only the shown metadata changes, once per line). The cover
 *   is image data, never a URI. With nothing current the playlist is empty.
 * - The position is the engine's (`activePositionMs`), the state READY or BUFFERING (meant to play but not playing yet,
 *   a network retry included); `playWhenReady` is the engine's `playing`. The player's own errors are the engine's to
 *   handle (it retries, skips or stops), so none is shown.
 * - Commands go to [RemoteCommands]: play/pause, seek, next, previous always; seek back 15 s and forward 30 s for an
 *   episode; shuffle and repeat for music. A controller can never set items, the volume (the sleep fade's) or the speed.
 *   A play with nothing current is the engine's cold start ([RemoteCommands.handlePlay]), so the session never asks
 *   for playback resumption itself.
 * - Releasing it (the service ends) only detaches it: the ExoPlayer and the engine are the app's.
 */
@UnstableApi
class SessionPlayer(
    player: Player,
    private val remote: RemoteCommands,
    private val nowPlaying: NowPlayingHub,
) : ForwardingSimpleBasePlayer(Detachable(player)) {
    private val changed: () -> Unit = { invalidateState() }

    init {
        nowPlaying.addListener(changed)
        remote.addListener(changed)
    }

    override fun getState(): State {
        val base = super.getState()
        val info = nowPlaying.info
        val engine = remote.engine
        val state = base.buildUpon()
            .setAvailableCommands(commands())
            .setPlayerError(null)
            .setShuffleModeEnabled(remote.shuffle)
            .setRepeatMode(repeatMode(remote.repeatMode))
            .setSeekBackIncrementMs(SEEK_BACK_MS)
            .setSeekForwardIncrementMs(SEEK_FORWARD_MS)
        if (info == null) {
            return state.setPlaylist(emptyList())
                .setCurrentMediaItemIndex(C.INDEX_UNSET)
                .setPlaybackState(Player.STATE_IDLE)
                .setIsLoading(false)
                .setPlayWhenReady(false, base.playWhenReadyChangeReason)
                .setContentPositionMs(0L)
                .setContentBufferedPositionMs(PositionSupplier.ZERO)
                .setTotalBufferedDurationMs(PositionSupplier.ZERO)
                .build()
        }
        val playing = engine?.playing ?: false
        val buffering = base.playbackState == Player.STATE_BUFFERING ||
            (playing && (base.playbackState == Player.STATE_IDLE || base.playbackState == Player.STATE_ENDED))
        val elapsed = info.elapsedMs
        return state.setPlaylist(listOf(item(info)))
            .setCurrentMediaItemIndex(0)
            .setPlaybackState(if (buffering) Player.STATE_BUFFERING else Player.STATE_READY)
            .setPlayWhenReady(playing, base.playWhenReadyChangeReason)
            .setContentPositionMs { remote.engine?.activePositionMs ?: elapsed }
            .build()
    }

    private fun commands(): Player.Commands {
        val b = Player.Commands.Builder().addAll(
            Player.COMMAND_PLAY_PAUSE, Player.COMMAND_STOP, Player.COMMAND_RELEASE,
            Player.COMMAND_GET_CURRENT_MEDIA_ITEM, Player.COMMAND_GET_TIMELINE, Player.COMMAND_GET_METADATA,
            Player.COMMAND_GET_AUDIO_ATTRIBUTES,
            Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
            Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
            Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
        )
        if (remote.skipEnabled) b.addAll(Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD)
        if (remote.modesEnabled) b.addAll(Player.COMMAND_SET_SHUFFLE_MODE, Player.COMMAND_SET_REPEAT_MODE)
        return b.build()
    }

    private fun item(info: NowPlayingInfo): MediaItemData {
        val type = if (info.kind == Item.Kind.episode) MediaMetadata.MEDIA_TYPE_PODCAST_EPISODE else MediaMetadata.MEDIA_TYPE_MUSIC
        val shown = MediaMetadata.Builder()
            .setTitle(info.title)
            .setArtist(info.artist)
            .setAlbumTitle(info.album)
            .setMediaType(type)
            .setIsPlayable(true)
            .setIsBrowsable(false)
            .apply { info.artwork?.let { setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) } }
            .build()
        // The song itself (the queue a car shows): stable while the shown title follows the lyrics.
        val song = remote.engine?.current?.takeIf { NowPlayingController.key(it) == info.key }
        val own = MediaMetadata.Builder()
            .setTitle(song?.title ?: info.title)
            .setArtist(song?.artist ?: info.artist)
            .setAlbumTitle(info.album)
            .setMediaType(type)
            .build()
        return MediaItemData.Builder(info.key)
            .setMediaItem(MediaItem.Builder().setMediaId(info.key).setMediaMetadata(own).build())
            .setMediaMetadata(shown)
            .setDurationUs(info.durationMs?.let { it * 1000 } ?: C.TIME_UNSET)
            .setIsSeekable(true)
            .build()
    }

    override fun handleSetPlayWhenReady(playWhenReady: Boolean): ListenableFuture<*> {
        if (playWhenReady) remote.playNow() else remote.pauseNow()
        return Futures.immediateVoidFuture()
    }

    /** The session prepares before a play from the idle state: the engine's play does what is needed. */
    override fun handlePrepare(): ListenableFuture<*> = Futures.immediateVoidFuture()

    override fun handleStop(): ListenableFuture<*> {
        remote.pauseNow()
        return Futures.immediateVoidFuture()
    }

    override fun handleSeek(mediaItemIndex: Int, positionMs: Long, seekCommand: Int): ListenableFuture<*> {
        when (seekCommand) {
            Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM, Player.COMMAND_SEEK_TO_MEDIA_ITEM ->
                if (positionMs != C.TIME_UNSET) remote.seekNow(positionMs / 1000.0)
            Player.COMMAND_SEEK_BACK -> remote.skipNow(-RemoteCommands.SKIP_BACKWARD_S)
            Player.COMMAND_SEEK_FORWARD -> remote.skipNow(RemoteCommands.SKIP_FORWARD_S)
            Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM -> remote.nextNow()
            // The engine decides between the start of this item and the one before (iOS: 3 s), as on the lock screen.
            Player.COMMAND_SEEK_TO_PREVIOUS, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM -> remote.previousNow()
            else -> {}
        }
        return Futures.immediateVoidFuture()
    }

    override fun handleSetShuffleModeEnabled(shuffleModeEnabled: Boolean): ListenableFuture<*> {
        remote.changeShuffleNow(shuffleModeEnabled)
        return Futures.immediateVoidFuture()
    }

    override fun handleSetRepeatMode(repeatMode: Int): ListenableFuture<*> {
        remote.changeRepeatNow(when (repeatMode) {
            Player.REPEAT_MODE_ONE -> RepeatMode.one
            Player.REPEAT_MODE_ALL -> RepeatMode.all
            else -> RepeatMode.off
        })
        return Futures.immediateVoidFuture()
    }

    /** Detaches from the hub and the commands; the wrapped player is only let go of ([Detachable]). */
    override fun handleRelease(): ListenableFuture<*> {
        nowPlaying.removeListener(changed)
        remote.removeListener(changed)
        return super.handleRelease()
    }

    /** The app-wide player as this session sees it: its `release` removes this session's listeners and nothing more. */
    private class Detachable(player: Player) : ForwardingPlayer(player) {
        private val added = mutableListOf<Player.Listener>()

        override fun addListener(listener: Player.Listener) {
            added.add(listener)
            super.addListener(listener)
        }

        override fun removeListener(listener: Player.Listener) {
            added.remove(listener)
            super.removeListener(listener)
        }

        override fun release() {
            added.toList().forEach { super.removeListener(it) }
            added.clear()
        }
    }

    companion object {
        val SEEK_BACK_MS = (RemoteCommands.SKIP_BACKWARD_S * 1000).toLong()
        val SEEK_FORWARD_MS = (RemoteCommands.SKIP_FORWARD_S * 1000).toLong()

        fun repeatMode(m: RepeatMode): Int = when (m) {
            RepeatMode.off -> Player.REPEAT_MODE_OFF
            RepeatMode.all -> Player.REPEAT_MODE_ALL
            RepeatMode.one -> Player.REPEAT_MODE_ONE
        }
    }
}
