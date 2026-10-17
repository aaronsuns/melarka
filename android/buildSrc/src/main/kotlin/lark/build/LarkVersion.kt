package lark.build

import java.io.File

/** versionCode for "X.Y.Z": X*10000 + Y*100 + Z (0.2.15 -> 215, 1.10.3 -> 11003). */
fun versionCode(versionName: String): Int {
    val parts = versionName.split(".").map { it.toIntOrNull() ?: -1 }
    require(parts.size == 3 && parts[0] >= 0 && parts[1] in 0..99 && parts[2] in 0..99) {
        "version must be X.Y.Z with minor and patch 0..99, got '$versionName'"
    }
    return parts[0] * 10000 + parts[1] * 100 + parts[2]
}

/**
 * The shared app version: `override` (release CI passes -PlarkVersion=X.Y.Z) or MARKETING_VERSION
 * from ios/project.yml, which is bumped by hand together with Android's.
 */
fun larkVersion(repoRoot: File, override: String? = null): String {
    if (!override.isNullOrBlank()) return override
    val yml = File(repoRoot, "ios/project.yml")
    val match = Regex("""MARKETING_VERSION:\s*"?([0-9]+\.[0-9]+\.[0-9]+)"?""").find(yml.readText())
    return requireNotNull(match) { "no MARKETING_VERSION in ${yml.path}" }.groupValues[1]
}
