package lark.ui

import android.graphics.BitmapFactory
import android.net.Uri
import android.util.LruCache
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.min
import androidx.compose.ui.unit.sp
import lark.app.R
import lark.bridge.Item
import lark.bridge.RepeatMode
import java.io.File

/**
 * Offline Mode (port of iOS `OfflinePlayerView`): the native player over the failed page, opened from Play Offline
 * Favorites. Now playing (cover, title, lyrics, progress, transport, shuffle and repeat), Up Next, and the cached
 * favorites; every control goes to the engine through [OfflinePlayerModel], so the lock screen and the car stay in step.
 * Retry Connection loads the web page again; the app closes this screen once it has loaded, and the music plays on.
 */
@Composable
fun OfflinePlayerScreen(
    model: OfflinePlayerModel,
    server: String,
    stayOffline: Boolean,
    onStayOffline: (Boolean) -> Unit,
    onClose: () -> Unit,
) {
    LaunchedEffect(model) { model.reload() }
    Column(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background)) {
        Header(model, server, stayOffline, onStayOffline, onClose)
        HorizontalDivider(color = LarkColors.separator)
        val current = model.current
        LazyColumn(Modifier.fillMaxSize().testTag("offline-list")) {
            item(key = "now") { NowPlayingCard(model) }
            val upNext = model.upNext
            if (upNext.isNotEmpty()) {
                item(key = "up-next") { SectionHeader(stringResource(R.string.up_next)) }
                items(upNext, key = { "q${it.index}" }) { entry ->
                    SongRow(model, entry.item, isCurrent = false) { model.jump(entry.index) }
                }
            }
            item(key = "favorites") { SectionHeader(stringResource(R.string.offline_favorites)) }
            if (model.favorites.isEmpty()) {
                item(key = "none") {
                    Text(stringResource(R.string.offline_no_favorites), color = LarkColors.muted,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp))
                }
            }
            items(model.favorites, key = { "f${it.id}" }) { item ->
                SongRow(model, item, isCurrent = item.id == current?.id) { model.play(item) }
            }
        }
    }
}

/** One row: the state on the left (the server, or why it isn't loading); Stay Offline, retry and close on the right. */
@Composable
private fun Header(model: OfflinePlayerModel, server: String, stayOffline: Boolean, onStayOffline: (Boolean) -> Unit, onClose: () -> Unit) {
    val status = when {
        stayOffline -> stringResource(R.string.stay_offline)
        model.connecting -> stringResource(R.string.offline_connecting)
        model.stillOffline -> stringResource(R.string.offline_still_unreachable)
        else -> Uri.parse(server).host ?: server
    }
    val round = IconButtonDefaults.iconButtonColors(containerColor = LarkColors.surface2, contentColor = LarkColors.text)
    Row(Modifier.fillMaxWidth().background(LarkColors.material).padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).semantics(mergeDescendants = true) { heading() }) {
            Text(stringResource(R.string.offline_mode), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(status, style = MaterialTheme.typography.bodySmall, color = if (stayOffline) LarkColors.offline else LarkColors.muted,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        // Round buttons, grey; Stay Offline turns orange when on.
        val stayLabel = stringResource(R.string.stay_offline)
        val stayState = stringResource(if (stayOffline) R.string.on else R.string.off)
        IconButton(onClick = { onStayOffline(!stayOffline) },
            colors = if (stayOffline) IconButtonDefaults.iconButtonColors(contentColor = LarkColors.offline) else round,
            modifier = Modifier.semantics { contentDescription = stayLabel; stateDescription = stayState; selected = stayOffline }) {
            Icon(LarkIcons.airplane, contentDescription = null)
        }
        if (!stayOffline) {
            val retryLabel = stringResource(R.string.retry_connection)
            IconButton(onClick = { model.retry() }, enabled = !model.connecting, colors = round) {
                if (model.connecting) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                else Icon(LarkIcons.refresh, contentDescription = retryLabel)
            }
        }
        IconButton(onClick = onClose, colors = round) { Icon(LarkIcons.close, contentDescription = stringResource(R.string.close)) }
    }
}

@Composable
private fun SectionHeader(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, color = LarkColors.muted,
        modifier = Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 20.dp, bottom = 6.dp).semantics { heading() })
}

/** Cover, title and artist, the lyrics, the progress slider, ⏮ ▶/⏸ ⏭ and the shuffle and repeat toggles. */
@Composable
private fun NowPlayingCard(model: OfflinePlayerModel) {
    val item = model.current
    val card = LarkColors.surface
    // A wash of the song's colour (the same hue as its drawn tile) fading into the card.
    val wash = item?.let { Brush.verticalGradient(listOf(tileColor(it.id, 0.5f).copy(alpha = 0.35f), card)) }
        ?: Brush.verticalGradient(listOf(card, card))
    Box(Modifier.fillMaxWidth().padding(12.dp).clip(RoundedCornerShape(16.dp)).background(wash)) {
        if (item == null) {
            Column(Modifier.fillMaxWidth().padding(vertical = 20.dp).semantics(mergeDescendants = true) {},
                horizontalAlignment = Alignment.CenterHorizontally) {
                Text(stringResource(R.string.nothing_playing), style = MaterialTheme.typography.titleMedium)
                Text(stringResource(R.string.tap_a_song), style = MaterialTheme.typography.bodyMedium, color = LarkColors.muted)
            }
        } else {
            Playing(model, item)
        }
    }
}

@Composable
private fun Playing(model: OfflinePlayerModel, item: Item) {
    var scrubMs by remember { mutableStateOf<Float?>(null) }
    var showLyrics by remember { mutableStateOf(false) }
    val duration = maxOf(model.durationMs, item.durationMs)
    val shownMs = scrubMs?.toLong() ?: model.positionMs
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val cover = min(min(maxWidth - 64.dp, 280.dp), 220.dp)
        Column(Modifier.fillMaxWidth().padding(16.dp), horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Artwork(model.artworkFile(item), item.id, item.title, Modifier.size(cover))
            Column(Modifier.semantics(mergeDescendants = true) {}, horizontalAlignment = Alignment.CenterHorizontally) {
                Text(item.title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center,
                    maxLines = 3, overflow = TextOverflow.Ellipsis)
                if (item.artist.isNotEmpty()) {
                    Text(item.artist, style = MaterialTheme.typography.bodyLarge, color = LarkColors.muted, textAlign = TextAlign.Center,
                        maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            model.lyrics?.let { lyrics ->
                val label = stringResource(R.string.lyrics)
                Box(Modifier.clickable { showLyrics = true }.semantics { contentDescription = label }) {
                    LyricsPreview(lyrics, model.positionMs)
                }
            }
            val positionLabel = stringResource(R.string.playback_position)
            val positionValue = stringResource(R.string.position_of, OfflinePlayerModel.time(shownMs), OfflinePlayerModel.time(duration))
            Column(Modifier.fillMaxWidth()) {
                Slider(
                    value = minOf(shownMs, maxOf(duration, 1)).toFloat(),
                    onValueChange = { scrubMs = it },
                    onValueChangeFinished = { scrubMs?.let { model.seek(it.toLong()) }; scrubMs = null },   // a drag seeks once, when the finger lifts
                    valueRange = 0f..maxOf(duration, 1).toFloat(),
                    enabled = duration > 0,
                    colors = SliderDefaults.colors(thumbColor = LarkColors.text, activeTrackColor = LarkColors.text,
                        inactiveTrackColor = LarkColors.surface2),
                    modifier = Modifier.semantics { contentDescription = positionLabel; stateDescription = positionValue },
                )
                Row(Modifier.fillMaxWidth()) {
                    Text(OfflinePlayerModel.time(shownMs), style = MaterialTheme.typography.labelSmall, color = LarkColors.muted)
                    Spacer(Modifier.weight(1f))
                    Text("-" + OfflinePlayerModel.time(maxOf(0, duration - shownMs)), style = MaterialTheme.typography.labelSmall,
                        color = LarkColors.muted)
                }
            }
            Transport(model)
            val error = model.error
            if (error != null && !model.playing) {
                Text(error, style = MaterialTheme.typography.bodySmall, color = LarkColors.muted, textAlign = TextAlign.Center)
            }
        }
    }
    if (showLyrics) LyricsSheet(model) { showLyrics = false }
}

@Composable
private fun Transport(model: OfflinePlayerModel) {
    val on = stringResource(R.string.on)
    val off = stringResource(R.string.off)
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
        val shuffleLabel = stringResource(R.string.media_shuffle)
        IconButton(onClick = { model.toggleShuffle() },
            modifier = Modifier.semantics { contentDescription = shuffleLabel; stateDescription = if (model.shuffle) on else off; selected = model.shuffle }) {
            Icon(LarkIcons.shuffle, contentDescription = null, tint = if (model.shuffle) LarkColors.accent else LarkColors.muted)
        }
        IconButton(onClick = { model.previous() }) {
            Icon(LarkIcons.previous, contentDescription = stringResource(R.string.previous), modifier = Modifier.size(32.dp))
        }
        IconButton(onClick = { model.togglePlay() }, modifier = Modifier.size(72.dp),
            colors = IconButtonDefaults.iconButtonColors(containerColor = LarkColors.text, contentColor = Color.Black)) {
            Icon(if (model.playing) LarkIcons.pause else LarkIcons.play,
                contentDescription = stringResource(if (model.playing) R.string.pause else R.string.play), modifier = Modifier.size(40.dp))
        }
        IconButton(onClick = { model.next() }) {
            Icon(LarkIcons.next, contentDescription = stringResource(R.string.next), modifier = Modifier.size(32.dp))
        }
        val repeatLabel = stringResource(when (model.repeatMode) {
            RepeatMode.off -> R.string.media_repeat_off
            RepeatMode.all -> R.string.media_repeat_all
            RepeatMode.one -> R.string.media_repeat_one
        })
        IconButton(onClick = { model.cycleRepeat() },
            modifier = Modifier.semantics { contentDescription = repeatLabel; selected = model.repeatMode != RepeatMode.off }) {
            Icon(if (model.repeatMode == RepeatMode.one) LarkIcons.repeatOne else LarkIcons.repeat, contentDescription = null,
                tint = if (model.repeatMode == RepeatMode.off) LarkColors.muted else LarkColors.accent)
        }
    }
}

/** Under the title: the line being sung and the next one (timed lyrics), or a button to read plain ones. */
@Composable
private fun LyricsPreview(lyrics: OfflineLyrics, positionMs: Long) {
    if (lyrics.synced) {
        val now = lyrics.index(positionMs)
        val current = now?.let { lyrics.lines[it].text }?.takeUnless { OfflineLyrics.blank(it) }
        val next = lyrics.nextWords(now)?.let { lyrics.lines[it].text }
        Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(LarkColors.surface2.copy(alpha = 0.6f))
            .padding(horizontal = 12.dp, vertical = 10.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Text(current ?: "♪", style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold, maxLines = 1,
                overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center)
            Text(next ?: " ", style = MaterialTheme.typography.bodyMedium, color = LarkColors.muted, maxLines = 1,
                overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center)
        }
    } else {
        Row(Modifier.clip(CircleShape).background(LarkColors.surface2.copy(alpha = 0.6f)).padding(horizontal = 14.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically) {
            Icon(LarkIcons.lyrics, contentDescription = null, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text(stringResource(R.string.lyrics), style = MaterialTheme.typography.labelLarge)
        }
    }
}

/**
 * The whole lyrics: timed lines follow the song (the current one bold, kept near the middle; a tap seeks there), plain
 * lyrics as text.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun LyricsSheet(model: OfflinePlayerModel, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = LarkColors.surface) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(model.current?.title ?: "", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, maxLines = 1,
                overflow = TextOverflow.Ellipsis)
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.done)) }
        }
        val lyrics = model.lyrics
        when {
            lyrics == null -> Text("♪", style = MaterialTheme.typography.headlineLarge, color = LarkColors.muted,
                modifier = Modifier.fillMaxWidth().padding(40.dp), textAlign = TextAlign.Center)
            lyrics.synced -> TimedLyrics(model, lyrics)
            else -> Text(lyrics.text ?: "", style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(20.dp))
        }
    }
}

@Composable
private fun TimedLyrics(model: OfflinePlayerModel, lyrics: OfflineLyrics) {
    val now = lyrics.index(model.positionMs)
    val state = rememberLazyListState()
    LaunchedEffect(now) { if (now != null) state.animateScrollToItem(maxOf(0, now - 3)) }
    LazyColumn(state = state, modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp)) {
        itemsIndexed(lyrics.lines) { i, line ->
            Text(if (OfflineLyrics.blank(line.text)) "♪" else line.text,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = if (i == now) FontWeight.Bold else FontWeight.Normal,
                color = if (i == now) LarkColors.text else LarkColors.muted,
                modifier = Modifier.fillMaxWidth().clickable { model.seek(lyrics.startMs(i)) }.padding(vertical = 9.dp))
        }
        item { Spacer(Modifier.size(200.dp)) }
    }
}

/** A song in Up Next or Offline Favorites; a tap plays it. */
@Composable
private fun SongRow(model: OfflinePlayerModel, item: Item, isCurrent: Boolean, onClick: () -> Unit) {
    val nowPlaying = stringResource(R.string.now_playing)
    val hint = stringResource(R.string.plays_this_song)
    Row(Modifier.fillMaxWidth().clickable(onClickLabel = hint, onClick = onClick)
        .semantics(mergeDescendants = true) { if (isCurrent) stateDescription = nowPlaying }
        .padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Artwork(model.artworkFile(item), item.id, item.title, Modifier.size(44.dp))
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(item.title, style = MaterialTheme.typography.bodyLarge, color = if (isCurrent) LarkColors.accent else LarkColors.text,
                maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (item.artist.isNotEmpty()) {
                Text(item.artist, style = MaterialTheme.typography.bodyMedium, color = LarkColors.muted, maxLines = 1,
                    overflow = TextOverflow.Ellipsis)
            }
        }
        if (isCurrent) Icon(LarkIcons.speaker, contentDescription = null, tint = LarkColors.accent,
            modifier = Modifier.size(if (model.playing) 22.dp else 18.dp))
    }
}

/** The cached cover, or a tile drawn from the song (a colour from its id, the title's first letter). */
@Composable
private fun Artwork(file: File?, seed: String, title: String, modifier: Modifier) {
    val image = remember(file) { file?.let { Covers.load(it) } }
    BoxWithConstraints(modifier.clip(RoundedCornerShape(8.dp))) {
        val width = maxWidth
        if (image != null) {
            Image(image, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        } else {
            Box(Modifier.fillMaxSize().background(Brush.linearGradient(listOf(tileColor(seed, 0.55f), tileColor(seed, 0.35f)))),
                contentAlignment = Alignment.Center) {
                val letter = title.trim().firstOrNull()
                val size = (width.value * 0.42f).sp
                if (letter != null) {
                    Text(letter.uppercase(), color = Color.White.copy(alpha = 0.9f), fontSize = size, fontWeight = FontWeight.SemiBold)
                } else {
                    Icon(LarkIcons.musicNote, contentDescription = null, tint = Color.White.copy(alpha = 0.9f),
                        modifier = Modifier.size(width * 0.4f))
                }
            }
        }
    }
}

/** A stable hue per song (FNV-1a of its id, as iOS), never random between launches. */
internal fun tileColor(seed: String, brightness: Float): Color {
    var h = 2_166_136_261u
    for (b in seed.toByteArray()) h = (h xor b.toUByte().toUInt()) * 16_777_619u
    return Color.hsv((h % 360u).toFloat(), 0.5f, minOf(1f, brightness + 0.25f))
}

/** Decoded covers, by file (a few dozen fit; they are small). */
private object Covers {
    private val cache = LruCache<String, ImageBitmap>(64)

    fun load(file: File): ImageBitmap? {
        cache.get(file.path)?.let { return it }
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(file.path, bounds)
        if (bounds.outWidth <= 0) return null
        var sample = 1
        while (bounds.outWidth / (sample * 2) >= 600) sample *= 2
        val bitmap = BitmapFactory.decodeFile(file.path, BitmapFactory.Options().apply { inSampleSize = sample }) ?: return null
        return bitmap.asImageBitmap().also { cache.put(file.path, it) }
    }
}
