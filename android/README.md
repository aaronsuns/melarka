# Melarka for Android

The server's web app in a WebView plus a native Media3 player, the Android counterpart of `ios/`.
Design: `docs/superpowers/specs/android-client-design.md`; plan: `docs/superpowers/plans/android-client-plan.md`.

## Build and test

Needs JDK 17 and the Android SDK (platform 35, build-tools 35.0.0). Nothing is installed on the
development Mac; builds run on a machine reachable over SSH:

```sh
export LARK_BUILD_HOST=user@build-mac
android/scripts/remote.sh test    # unit tests and lint
android/scripts/remote.sh build   # app/build/outputs/apk/debug/app-debug.apk on the build machine
android/scripts/remote.sh run '<cmd>'
```

The script syncs `android/`, `ios/project.yml` (the version) and `docs/brand` to `~/lark-android-build`,
and sets `JAVA_HOME` (Homebrew `openjdk@17`) and `ANDROID_HOME`
(`/opt/homebrew/share/android-commandlinetools`) itself. With a local JDK and SDK, `cd android && ./gradlew
testDebugUnitTest lintDebug assembleDebug` is the same build.

## Version

`versionName` is `MARKETING_VERSION` in `ios/project.yml` (one version for both apps, bumped by hand);
`versionCode` is X*10000 + Y*100 + Z. A release build overrides it with `-PlarkVersion=X.Y.Z`.
The logic is `buildSrc/src/main/kotlin/lark/build/LarkVersion.kt`, tested by `SmokeTest`.

## Identifiers

Application id `io.github.aaronsuns.melarka`, name "Melarka"; Kotlin packages use `lark`.
