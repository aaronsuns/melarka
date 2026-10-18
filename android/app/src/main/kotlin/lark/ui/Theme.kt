package lark.ui

import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.dp

/** The web app's colour tokens (`web/src/styles.css` `:root`): the native screens look like the pages around them. */
object LarkColors {
    val bg = Color(0xFF121212)
    val surface = Color(0xFF1E1E1E)
    val surface2 = Color(0xFF2A2A2A)
    val text = Color(0xFFF2F2F2)
    val muted = Color(0xFF9A9A9A)
    val accent = Color(0xFF1ED760)
    val danger = Color(0xFFFF5A5F)
    val separator = Color(0x17FFFFFF)        // rgba(255, 255, 255, .09)
    val material = Color(0xFF1C1C1E)          // --material-solid
    /** Stay Offline's on state (iOS shows it orange). */
    val offline = Color(0xFFFF9F0A)
}

private val scheme = darkColorScheme(
    primary = LarkColors.accent,
    onPrimary = Color.Black,                  // `.primary` buttons: black on the accent
    secondary = LarkColors.muted,
    onSecondary = Color.Black,
    background = LarkColors.bg,
    onBackground = LarkColors.text,
    surface = LarkColors.bg,
    onSurface = LarkColors.text,
    surfaceVariant = LarkColors.surface,
    onSurfaceVariant = LarkColors.muted,
    surfaceContainerLowest = LarkColors.bg,
    surfaceContainerLow = LarkColors.surface,
    surfaceContainer = LarkColors.surface,
    surfaceContainerHigh = LarkColors.surface2,
    surfaceContainerHighest = LarkColors.surface2,
    outline = LarkColors.muted,
    outlineVariant = LarkColors.separator,
    error = LarkColors.danger,
    onError = Color.Black,
)

/**
 * Material 3, dark, in the web's colours. Text and icons outside a `Surface` (the screens draw their own backgrounds)
 * take the theme's text colour, not Compose's default black.
 */
@Composable
fun LarkTheme(content: @Composable () -> Unit) = MaterialTheme(colorScheme = scheme) {
    CompositionLocalProvider(LocalContentColor provides scheme.onBackground, content = content)
}

/**
 * The few icons the native screens use, as 24 dp vectors (Material Design icons, Apache 2.0), so the app needs no
 * icon library.
 */
object LarkIcons {
    val play = icon("M8,5v14l11,-7z")
    val pause = icon("M6,19h4V5H6v14zM14,5v14h4V5h-4z")
    val next = icon("M6,18l8.5,-6L6,6v12zM16,6v12h2V6h-2z")
    val previous = icon("M6,6h2v12H6zM9.5,12l8.5,6V6z")
    val shuffle = icon("M10.59,9.17L5.41,4 4,5.41l5.17,5.17 1.42,-1.41zM14.5,4l2.04,2.04L4,18.59 5.41,20 17.96,7.46 20,9.5V4h-5.5zM14.83,13.41l-1.41,1.41 3.13,3.13L14.5,20H20v-5.5l-2.04,2.04 -3.13,-3.13z")
    val repeat = icon("M7,7h10v3l4,-4 -4,-4v3H5v6h2V7zM17,17H7v-3l-4,4 4,4v-3h12v-6h-2v4z")
    val repeatOne = icon("M7,7h10v3l4,-4 -4,-4v3H5v6h2V7zM17,17H7v-3l-4,4 4,4v-3h12v-6h-2v4zM13,15V9h-1l-2,1v1h1.5v4H13z")
    val close = icon("M19,6.41L17.59,5 12,10.59 6.41,5 5,6.41 10.59,12 5,17.59 6.41,19 12,13.41 17.59,19 19,17.59 13.41,12z")
    val refresh = icon("M17.65,6.35C16.2,4.9 14.21,4 12,4c-4.42,0 -7.99,3.58 -7.99,8s3.57,8 7.99,8c3.73,0 6.84,-2.55 7.73,-6h-2.08c-0.82,2.33 -3.04,4 -5.65,4 -3.31,0 -6,-2.69 -6,-6s2.69,-6 6,-6c1.66,0 3.14,0.69 4.22,1.78L13,11h7V4l-2.35,2.35z")
    val airplane = icon("M21,16v-2l-8,-5V3.5C13,2.67 12.33,2 11.5,2S10,2.67 10,3.5V9l-8,5v2l8,-2.5V19l-2,1.5V22l3.5,-1 3.5,1v-1.5L13,19v-5.5l8,2.5z")
    val cloudOff = icon("M19.35,10.04C18.67,6.59 15.64,4 12,4c-1.48,0 -2.85,0.43 -4.01,1.17l1.46,1.46C10.21,6.23 11.08,6 12,6c3.04,0 5.5,2.46 5.5,5.5v0.5H19c1.66,0 3,1.34 3,3 0,1.13 -0.64,2.11 -1.56,2.62l1.45,1.45C23.16,18.16 24,16.68 24,15c0,-2.64 -2.05,-4.78 -4.65,-4.96zM3,5.27l2.75,2.74C2.56,8.15 0,10.77 0,14c0,3.31 2.69,6 6,6h11.73l2,2L21,20.73 4.27,4 3,5.27zM7.73,10l8,8H6c-2.21,0 -4,-1.79 -4,-4s1.79,-4 4,-4h1.73z")
    val musicNote = icon("M12,3v10.55C11.41,13.21 10.73,13 10,13c-2.21,0 -4,1.79 -4,4s1.79,4 4,4 4,-1.79 4,-4V7h4V3h-6z")
    val speaker = icon("M3,9v6h4l5,5V4L7,9H3zM16.5,12c0,-1.77 -1.02,-3.29 -2.5,-4.03v8.05c1.48,-0.73 2.5,-2.25 2.5,-4.02zM14,3.23v2.06c2.89,0.86 5,3.54 5,6.71s-2.11,5.85 -5,6.71v2.06c4.01,-0.91 7,-4.49 7,-8.77s-2.99,-7.86 -7,-8.77z")
    val lyrics = icon("M6,17h3l2,-4V7H5v6h3zM14,17h3l2,-4V7h-6v6h3z")
    val back = icon("M20,11H7.83l5.59,-5.59L12,4l-8,8 8,8 1.41,-1.41L7.83,13H20v-2z")

    private fun icon(path: String): ImageVector = ImageVector.Builder(defaultWidth = 24.dp, defaultHeight = 24.dp,
        viewportWidth = 24f, viewportHeight = 24f)
        .addPath(PathParser().parsePathString(path).toNodes(), fill = SolidColor(Color.White))
        .build()
}
