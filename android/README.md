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
android/scripts/remote.sh smoke   # the emulator smoke test (below)
```

The script syncs `android/`, `ios/project.yml` (the version) and `docs/brand` to `~/lark-android-build`,
and sets `JAVA_HOME` (Homebrew `openjdk@17`) and `ANDROID_HOME`
(`/opt/homebrew/share/android-commandlinetools`) itself. With a local JDK and SDK, `cd android && ./gradlew
testDebugUnitTest lintDebug assembleDebug` is the same build.

## Emulator smoke test

`android/scripts/emulator-smoke.sh` runs the instrumented tests (`app/src/androidTest`) on an emulator against a
throwaway server with the made-up demo library (`web/e2e/start-server.sh`, `LARK_DEMO=1`) on `127.0.0.1:4700`,
which the emulator reaches as `10.0.2.2:4700`. It needs Go, Node, Python 3 and ffmpeg besides the JDK and SDK, boots
the AVD `lark_api35` headless unless a device is attached, and leaves the server log, logcat and a failure's
screenshot in `app/build/smoke`. `remote.sh smoke` syncs the server's sources too and runs it on the build machine;
extra arguments go to Gradle (`-Pandroid.testInstrumentationRunnerArguments.class=lark.SmokeFlowTest`). CI runs the
same script in the `android-emulator` job.

## Version

`versionName` is `MARKETING_VERSION` in `ios/project.yml` (one version for both apps, bumped by hand);
`versionCode` is X*10000 + Y*100 + Z. A release build overrides it with `-PlarkVersion=X.Y.Z`.
The logic is `buildSrc/src/main/kotlin/lark/build/LarkVersion.kt`, tested by `SmokeTest`.
`version.properties` repeats the versionCode for F-Droid's update check; bump it with the version
(`internal/config/release_test.go` fails otherwise). The store texts F-Droid shows are in `fastlane/` at the
repository root (docs/fdroid/README.md).

## Identifiers

Application id `io.github.aaronsuns.melarka`, name "Melarka"; Kotlin packages use `lark`.
