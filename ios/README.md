# Melarka for iPhone

The iPhone app for Melarka: a full-screen WKWebView of the web app plus a native playback engine
(background play, lock-screen and car controls, offline favorites, Siri and Shortcuts). The code name is
`lark`: the Xcode project, targets and scheme are `Lark`, and Swift types keep the `Lark` prefix. Only
user-visible text says Melarka. Installing the app on a phone is covered in [docs/iphone.md](../docs/iphone.md).

## Layout

- `project.yml`: the [XcodeGen](https://github.com/yonaskolb/XcodeGen) source of truth. **Never edit
  `Lark.xcodeproj`**: it is generated and git-ignored.
- `Sources/App`: app entry, assets and `Info.plist`. `Info.plist` is generated from `project.yml` and
  committed; regenerate it with `xcodegen generate` (or `scripts/remote.sh gen`) after changing `info:` there.
- `Sources/Bridge`, `Player`, `Cache`, `Intents`, `Settings`, `API`: the web bridge, playback engine, offline
  cache, App Shortcuts, native settings and the server client.
- `Tests`: the `LarkTests` XCTest target, hosted in the app.
- `scripts/remote.sh`: build and test on another Mac over SSH.
- `scripts/make_source.py` and `source-template.json`: the SideStore `source.json`, built from the GitHub
  Releases by the Pages workflow.

The bundle id is `io.github.aaronsuns.melarka` and the deployment target is iOS 17.

## Build and test locally

You need a Mac with Xcode and XcodeGen (`brew install xcodegen`). From `ios/`:

    xcodegen generate
    xcodebuild test -project Lark.xcodeproj -scheme Lark \
      -destination 'platform=iOS Simulator,name=iPhone 17'

Or open `Lark.xcodeproj` in Xcode and run the `Lark` scheme. To run on your own device, pick your team in
Signing & Capabilities, or pass `DEVELOPMENT_TEAM=<id>` to `xcodebuild`; the committed project has none.

Tests for the source generator: `python3 -m unittest discover -s scripts`.

## Build and test on a remote Mac

`scripts/remote.sh` syncs the tree to a Mac with Xcode (and XcodeGen and rsync from Homebrew) reachable over
SSH, and builds and tests there. Set `LARK_BUILD_HOST=user@host`; there is no default.

    scripts/remote.sh gen                            # sync, xcodegen, copy Info.plist back
    scripts/remote.sh build
    scripts/remote.sh test [LarkTests/Class[/method]]
    scripts/remote.sh run '<shell>'                  # e.g. 'tail -200 build/test.log'

The `Lark Test iPhone` simulator is created on the remote Mac on demand. A cold first boot of a freshly
created simulator can fail once with "failed to launch"; rerun `test`.

## Releases

Releases are built by GitHub Actions, not on a developer machine: a `v*` tag builds the unsigned IPA
(`Melarka-X.Y.Z.ipa`) in the release workflow and attaches it to the GitHub Release, and the Pages workflow
then rebuilds the SideStore `source.json` from the releases. SideStore signs the app on the phone. See the
root [README](../README.md) and [docs/iphone.md](../docs/iphone.md). The IPA is public, so the app must
contain no secrets.

CI runs the iOS tests on a macOS runner for every change under `ios/`.
