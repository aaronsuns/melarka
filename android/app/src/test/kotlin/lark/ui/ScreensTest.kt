package lark.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isToggleable
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.Density
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import lark.app.ServicesTestCase
import lark.bridge.Item
import lark.bridge.NativeEvent
import lark.bridge.QueueSource
import lark.bridge.WebMessage
import lark.player.toItem
import lark.testing.cachedFavorite
import lark.testing.lyricsDoc
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.robolectric.annotation.Config

/**
 * The native screens laid out and tapped (iOS `testTheViewLaysOutEmptyAndPlaying`, and the same for the other screens):
 * no crash, the texts there, the taps reaching their callbacks.
 */
@OptIn(ExperimentalCoroutinesApi::class)
@Config(sdk = [34])
class ScreensTest : ServicesTestCase() {
    @get:Rule val compose = createComposeRule()

    private class Recorder : OfflinePlayerControl {
        val messages = mutableListOf<WebMessage>()
        override fun handle(m: WebMessage) { messages += m }
        override fun emitAll() {}
    }

    private val favorites = listOf(cachedFavorite(1, "Alpha"), cachedFavorite(2, "Beta"), cachedFavorite(3, "Gamma"))

    /** Nothing playing with no favorites, then playing with a queue and lyrics, at twice the text size. */
    /**
     * Text with no colour of its own (titles, labels) takes the theme's text colour, not Compose's default black, which
     * was unreadable on the dark background (found on the emulator, Task 14).
     */
    @Test fun textDefaultsToTheThemesTextColour() {
        var content = androidx.compose.ui.graphics.Color.Unspecified
        compose.setContent { LarkTheme { content = androidx.compose.material3.LocalContentColor.current } }
        compose.waitForIdle()
        org.junit.Assert.assertEquals(LarkColors.text, content)
    }

    @Test fun theOfflinePlayerLaysOutEmptyAndPlaying() {
        val player = Recorder()
        var shown = emptyList<lark.api.ApiTrack>()
        val docs = mapOf(1L to lyricsDoc(lines = listOf(0L to "first line", 60_000L to "second line")))
        val model = OfflinePlayerModel(player, favorites = { shown }, retry = {}, lyrics = { docs[it] })
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                LarkTheme { OfflinePlayerScreen(model, "https://music.example.com/", stayOffline = false, onStayOffline = {}, onClose = {}) }
            }
        }
        compose.onNodeWithText("Nothing playing").assertIsDisplayed()
        compose.onNodeWithText("Offline Mode").assertIsDisplayed()
        compose.onNodeWithText("music.example.com").assertIsDisplayed()

        shown = favorites
        model.reload()
        val items = favorites.map { it.toItem() }
        model.receive(NativeEvent.Queue(Item.Kind.track, items, 0, QueueSource.favorites))
        model.receive(NativeEvent.State(Item.Kind.track, "1", 0, playing = true, positionMs = 1_000, durationMs = 200_000,
            buffering = false, error = null, rate = 1.0))
        compose.waitForIdle()
        compose.onNodeWithText("first line").assertIsDisplayed()           // the lyrics preview: the line being sung
        compose.onNodeWithTag("offline-list").performScrollToNode(hasText("Up Next"))
        compose.onNodeWithTag("offline-list").performScrollToNode(hasText("Gamma"))
        compose.onNodeWithText("Gamma").performClick()
        val play = player.messages.last() as WebMessage.SetQueue
        assertEquals(QueueSource.favorites, play.source)
        assertEquals("3", play.items[play.index].id)
    }

    @Test fun theOfflinePlayersHeaderSaysWhyItIsOffline() {
        val model = OfflinePlayerModel(Recorder(), favorites = { emptyList() }, retry = {})
        var closed = 0
        compose.setContent {
            LarkTheme { OfflinePlayerScreen(model, "https://music.example.com/", stayOffline = true, onStayOffline = {}, onClose = { closed++ }) }
        }
        compose.onNodeWithText("Stay Offline").assertIsDisplayed()
        compose.onNodeWithText("No favorites are cached on this phone yet.").assertIsDisplayed()
        compose.onNodeWithContentDescription("Close").performClick()
        assertEquals(1, closed)
    }

    /** The retry button keeps its name while it spins. */
    @Test fun theRetryButtonIsNamedWhileItRuns() {
        val model = OfflinePlayerModel(Recorder(), favorites = { emptyList() }, retry = {})
        compose.setContent {
            LarkTheme { OfflinePlayerScreen(model, "https://music.example.com/", stayOffline = false, onStayOffline = {}, onClose = {}) }
        }
        compose.onNodeWithContentDescription("Retry Connection").performClick()
        assertTrue(model.connecting)
        compose.onNodeWithContentDescription("Retry Connection").assertExists()
        compose.onNodeWithText("Connecting…").assertIsDisplayed()
    }

    @Test fun theServerScreenChecksTheAddress() {
        val connected = mutableListOf<String>()
        val checked = mutableListOf<String>()
        compose.setContent { LarkTheme { ServerScreen(onConnected = { connected += it }, check = { checked += it.toString(); true }) } }
        compose.onNodeWithText("Server Address").assertIsDisplayed()
        compose.onNodeWithTag("server").performTextInput("ftp://music.example.com")
        compose.onNodeWithText("Connect").performClick()
        compose.onNodeWithText("Enter an address that starts with https:// (for development, http://…local or localhost also works)")
            .assertIsDisplayed()
        assertTrue(checked.isEmpty())
        compose.onNodeWithTag("server").performTextClearance()
        compose.onNodeWithTag("server").performTextInput(" https://Music.Example.com/web ")
        compose.onNodeWithText("Connect").performClick()
        compose.waitForIdle()
        assertEquals(listOf("https://music.example.com/"), checked)
        assertEquals(listOf("https://music.example.com/"), connected)
    }

    @Test fun anUnreachableServerIsSaid() {
        compose.setContent { LarkTheme { ServerScreen(onConnected = {}, check = { false }) } }
        compose.onNodeWithTag("server").performTextInput("https://music.example.com")
        compose.onNodeWithText("Connect").performClick()
        compose.waitForIdle()
        compose.onNodeWithText("Can't connect to this server").assertIsDisplayed()
    }

    @Test fun theCantConnectScreensButtons() {
        val taps = mutableListOf<String>()
        compose.setContent {
            LarkTheme {
                CantConnectScreen("https://music.example.com/", stayOffline = false, hasOfflineFavorites = true,
                    onRetry = { taps += "retry" }, onSettings = { taps += "settings" }, onPlayOfflineFavorites = { taps += "play" },
                    onStayOffline = { taps += "stay:$it" })
            }
        }
        compose.onNodeWithText("Can't Connect to the Server").assertIsDisplayed()
        compose.onNodeWithText("Retry").performClick()
        compose.onNodeWithText("Settings").performClick()
        compose.onNodeWithText("Play Offline Favorites").performClick()
        compose.onNode(hasText("Stay Offline") and isToggleable()).assertIsOff().performClick()   // the whole row is the switch
        assertEquals(listOf("retry", "settings", "play", "stay:true"), taps)
    }

    @Test fun withStayOfflineRetryIsOffAndWithNoFavoritesThereIsNothingToPlay() {
        compose.setContent {
            LarkTheme {
                CantConnectScreen("https://music.example.com/", stayOffline = true, hasOfflineFavorites = false,
                    onRetry = {}, onSettings = {}, onPlayOfflineFavorites = {}, onStayOffline = {})
            }
        }
        compose.onNodeWithText("Retry").assertIsNotEnabled()
        compose.onAllNodesWithText("Play Offline Favorites").assertCountEquals(0)
    }

    /** Settings' usage follows the cache while it is open (a sync storing songs, a clear). */
    @Test fun settingsFollowTheCache() {
        scope = TestScope()
        val s = makeServices(cache = null)
        var changes by androidx.compose.runtime.mutableIntStateOf(s.cacheChanges)
        s.observe { changes = s.cacheChanges }
        compose.setContent {
            LarkTheme {
                SettingsScreen(s, car = null, host = SettingsHost(), stayOffline = false, changingServer = false,
                    cacheChanges = changes, onDone = {})
            }
        }
        compose.onNodeWithText("Used 0 B / limit 2 GB").assertIsDisplayed()
        val file = java.io.File(dir, "in.m4a").apply { writeBytes(ByteArray(1000) { 1 }) }
        val t = lark.testing.trackModel(9)
        compose.runOnIdle { s.cacheStore!!.store(9, file, t.track, t.json, favorite = true) }
        compose.onNodeWithText("Used 1000 B / limit 2 GB").assertIsDisplayed()
    }

    /** On a 320 dp phone the cache limit chips wrap to a second row; no label breaks over lines ("10 GB" did, per letter). */
    @Config(sdk = [34], qualifiers = "w320dp-h640dp-mdpi")
    @org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)     // real text measuring
    @Test fun theCacheLimitChipsKeepTheirLabelsOnOneLineOnANarrowPhone() {
        scope = TestScope()
        val s = makeServices(cache = null)
        compose.setContent {
            LarkTheme {
                SettingsScreen(s, car = null, host = SettingsHost(), stayOffline = false, changingServer = false, cacheChanges = 0,
                    onDone = {})
            }
        }
        for (gb in SettingsModel.capChoices) {
            val node = compose.onNodeWithText("$gb GB", useUnmergedTree = true).fetchSemanticsNode()
            val layouts = mutableListOf<androidx.compose.ui.text.TextLayoutResult>()
            node.config[androidx.compose.ui.semantics.SemanticsActions.GetTextLayoutResult].action!!.invoke(layouts)
            assertEquals("$gb GB on one line", 1, layouts.single().lineCount)
        }
    }

    /** Settings lays out with a signed-in user; the cap chips set the cap; Change Server asks first. */
    @Test fun settingsLaysOutAndAsksBeforeChangingServer() {
        scope = TestScope()
        val s = makeServices(cache = null)
        compose.setContent {
            LarkTheme {
                SettingsScreen(s, car = null, host = SettingsHost(), stayOffline = false, changingServer = false, cacheChanges = 0,
                    onDone = {})
            }
        }
        compose.onNodeWithText("Melarka Settings").assertIsDisplayed()
        compose.onNodeWithText("Used 0 B / limit 2 GB").assertIsDisplayed()
        compose.onNodeWithText("5 GB").performClick()
        assertEquals(5, s.cacheCapGB)
        compose.onNodeWithText("Change Server").performClick()
        compose.onNodeWithText("Change server?").assertIsDisplayed()
        compose.onNodeWithText("Cancel").performClick()
        assertEquals(serverUrl, s.serverUrl)
        compose.onNodeWithTag("settings").performScrollToNode(hasText(SettingsModel.versionText))
        compose.onNodeWithText("Steps for Your Phone").assertExists()
        compose.onNodeWithText("Allow Background Playback").assertExists()
    }
}
