# Submitting Melarka to F-Droid

`io.github.aaronsuns.melarka.yml` here is the F-Droid build recipe. It goes into F-Droid's
[fdroiddata](https://gitlab.com/fdroid/fdroiddata) repository as `metadata/io.github.aaronsuns.melarka.yml`
through a merge request that the owner opens. The recipe builds our APK from source and, because the
build is reproducible, F-Droid then ships **our** signed APK (`Binaries:` + `AllowedAPKSigningKeys:`): an
F-Droid install and a GitHub install have the same signature and update each other.

## Which version to submit

Submit the **first release after 0.2.16**. 0.2.16 itself reproduces (see below), but it lacks three things
F-Droid needs, which are on `main` after it:

- the store texts are at the repository root (`fastlane/metadata/android`); in 0.2.16 they were under
  `android/`, where F-Droid doesn't look, so the listing would have no summary, description or screenshots;
- the signed APK has no Google "dependency metadata" signing block (`dependenciesInfo` off), which F-Droid
  rejects in an APK it ships;
- `android/version.properties` gives F-Droid's update check (`UpdateCheckData`) the versionCode; without it
  `fdroid checkupdates`, which the merge request pipeline runs, fails on every tag.

The recipe below is written for 0.2.16, the version that was verified. Before copying it, change the
build entry to the new release X.Y.Z (code X*10000 + Y*100 + Z; the full commit hash of the tag):

```sh
V=0.2.17; CODE=217; SHA=$(git rev-parse "v$V^{commit}")
sed -e "s/0\.2\.16/$V/g" -e "s/216/$CODE/g" \
    -e "s/e9ae8e0f80129a05bc861d33e5ae5449a421a632/$SHA/" \
    docs/fdroid/io.github.aaronsuns.melarka.yml > /tmp/io.github.aaronsuns.melarka.yml
```

and check that the release's GitHub page has `Melarka-X.Y.Z.apk` (the `Binaries:` URL).

## Steps

1. A GitLab.com account. Use a public username; it shows on the merge request.
2. Fork <https://gitlab.com/fdroid/fdroiddata> (**Fork**, into your namespace, visibility **Public**).
   F-Droid needs the fork public and the branch unprotected.
3. Clone the fork and add the recipe on a new branch:

   ```sh
   git clone --depth 50 https://gitlab.com/<you>/fdroiddata.git && cd fdroiddata
   git checkout -b io.github.aaronsuns.melarka
   cp /tmp/io.github.aaronsuns.melarka.yml metadata/
   git add metadata/io.github.aaronsuns.melarka.yml
   git commit -m "New app: Melarka"
   git push -u origin io.github.aaronsuns.melarka
   ```

   Optional, before pushing (any machine with Python; `pipx install fdroidserver`): in the clone run
   `fdroid lint io.github.aaronsuns.melarka` and `fdroid rewritemeta io.github.aaronsuns.melarka`; the
   second must leave `git diff` empty.
4. Open the merge request from that branch into `fdroid/fdroiddata` `master`, title
   **`New app: Melarka`**, template **App inclusion**. Leave "Allow commits from members who can merge"
   on. The pipeline runs lint, rewritemeta, checkupdates and a test build that compares its result with
   `Melarka-X.Y.Z.apk`; all must pass. If GitLab asks for a phone number or a card to run CI, don't give
   one: say so in the merge request and a reviewer starts it.
5. Record the merge request link in `docs/android.md` (the F-Droid comment under "Updates") and commit
   `docs(android): F-Droid`.
6. Afterwards: answer reviewer comments in the merge request. Once merged, F-Droid's bot picks up each
   new `vX.Y.Z` tag by itself (`AutoUpdateMode`); a release only has to keep `MARKETING_VERSION`,
   `android/version.properties` and `fastlane/metadata/android/en-US/changelogs/<code>.txt` in step (the
   Go tests and the release gate check them).

## The merge request template

Text for the **App inclusion** template (keep its headings; tick each box):

**Abstract.** Melarka is the Android client of Melarka, a self-hosted music server (AGPL-3.0-or-later,
same repository). The app shows the server's web app full screen and plays audio with a native Media3
player: background playback with lock-screen, notification, headset and car controls, gapless playback,
an offline cache of favorites, launcher shortcuts and car autoplay. It talks only to the server address the
user enters (signing in with the user's account on that server), even for YouTube thumbnails, which the
server proxies; there are no analytics, ads or tracking. Screenshots: `fastlane/metadata/android/en-US/images`.

**Why.** It is the client for Melarka servers, which have their own API (no Subsonic or Jellyfin API), so
no other app in F-Droid can play from them.

Checklist answers:

- *Inclusion criteria*: yes. All code is AGPL-3.0-or-later; dependencies are AndroidX, Media3, Compose,
  OkHttp and kotlinx from Google's Maven and Maven Central, all Apache-2.0; no Google Play Services,
  Firebase or proprietary SDKs.
- *Author notified*: I am the author (Aaron Sun, the GitHub account `aaronsuns`).
- *Fastlane metadata*: yes, `fastlane/metadata/android` at the repository root: en-US (plus zh-CN,
  zh-TW, sv-SE) title, short and full description, changelogs and five phone screenshots. The icon comes
  from the APK.
- *Templates, Build Metadata Reference, Quick Start Guide, Git guide*: read.
- *Fork public, branch unprotected*: yes.
- *Related issues*: none.
- *One app*: yes.
- *Metadata*: `metadata/io.github.aaronsuns.melarka.yml`, valid YAML, LF; passes `fdroid lint` and
  `fdroid rewritemeta` unchanged; no summary/description/images in fdroiddata.
- *Tags and auto update*: releases are tagged `vX.Y.Z`; `AutoUpdateMode: Version`,
  `UpdateCheckMode: Tags ^v[0-9.]+$`. The Gradle build computes the version from `ios/project.yml`
  (one version for the iOS and Android apps), so `UpdateCheckData` reads the versionCode from
  `android/version.properties` and the name from `ios/project.yml`; tests keep the two in step.
- *Issue tracker and contact*: GitHub issues.
- *AuthorName*: Aaron Sun.
- *Submodules/srclibs*: none needed.
- *Reproducible builds*: yes, with our signature (`Binaries:`, `AllowedAPKSigningKeys:`). Verified: the
  unsigned release build is byte-identical from two clean checkouts, and between macOS (Homebrew OpenJDK
  17) and the F-Droid buildserver image (Debian, OpenJDK 17 from bookworm); `apksigcopier compare`
  accepts it against the published signed APK. The recipe installs JDK 17 (the build uses a Java 17
  toolchain). There are no timestamps or git-derived values in the APK; AGP's
  `META-INF/version-control-info.textproto` is constant because the Gradle root (`android/`) has no
  `.git`. Upstream builds with GitHub Actions (Temurin 17) and signs with the release key whose
  certificate SHA-256 is in `android/signing-cert.sha256`.
- *ABI split*: not needed; the native code is one small AndroidX library (about 10 KB per ABI) in a
  ~11 MB APK.
- *Only latest version, no disabled versions, full commit hash*: yes.
- *Pipeline*: green (or explain any warning here).

**Anti-feature NonFreeNet.** Declared. The app itself only talks to the user's own Melarka server, which
is free software, and plays the user's own library without any third-party service. But the app's UI
offers "Search YouTube" and "Download from a YouTube link": the user's server fetches that content from
YouTube with yt-dlp, so those features depend on a non-free network service, exactly as for the Invidious
client Clipious (`NonFreeNet: video content comes from YouTube`). F-Droid's rule covers an app that
promotes or depends on a non-free network service even through an intermediary, so declaring it is the
accurate, conservative choice; a reviewer may drop it, and that is fine too.

## What was verified (0.2.16, on the private Mac)

- Two clean clones of `v0.2.16`, `./gradlew --no-daemon --no-build-cache clean assembleRelease` with no
  signing variables (Homebrew OpenJDK 17): the same unsigned APK, SHA-256 `55fc60ec…64ee30`.
- `apksigcopier compare Melarka-0.2.16.apk --unsigned app-release-unsigned.apk`: passes.
- `main` after the three changes above builds the same unsigned APK, byte for byte; signed with a
  throwaway key its signing block no longer has the dependency-metadata block (0x504b4453).
- In F-Droid's `buildserver-trixie` image (amd64, run the way the fdroiddata pipeline runs it, with
  fdroidserver master): `fdroid rewritemeta` leaves the recipe unchanged, `fdroid lint` and
  `fdroid readmeta` pass, and `fdroid build --test --on-server io.github.aaronsuns.melarka:216` builds the
  same unsigned APK (same SHA-256), finds no scanner problems, downloads `Melarka-0.2.16.apk`, verifies it
  against the build and accepts its signer ("1 build succeeded").
- `fdroid checkupdates --auto` against a scratch copy of the repository with a test tag reads
  `UpdateCheckData` and adds the new build entry by itself.
