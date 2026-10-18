# The Melarka Android app

The Android app shows the same web app you use in a browser, full screen, but plays audio with a
native player. That gives it what a browser tab on a phone can't do reliably:

- playback that keeps going with the screen off, with lock-screen, notification, headphone and car
  (Bluetooth) controls;
- gapless transitions and a few songs downloaded ahead;
- an offline cache of your favorites, synced on Wi-Fi, with a size limit you choose;
- launcher shortcuts, **Shuffle Favorites** and **Resume** (long-press the Melarka icon), which
  automation apps can run too;
- **car autoplay**: your favorites start by themselves when the phone connects to the car;
- the current lyric line on the lock screen and the car's display.

It needs Android 8.0 or later and a Melarka server reachable over **HTTPS**. Plain `http://` works
only for `localhost` and `*.local` addresses, for development.

## 1. Install

Each release on GitHub has an APK: open
<https://github.com/aaronsuns/melarka/releases/latest> on the phone and download
`Melarka-X.Y.Z.apk`.

1. Open the downloaded file. Android asks whether your browser (or file manager) may install
   apps: tap **Settings**, turn on **Allow from this source**, and go back.
2. Tap **Install**.

### Check who signed it

Every release APK is signed with the same Melarka release key. Its certificate fingerprint
(SHA-256) is

```
42c929f4f00f837e74df5be81a43d59829704b1e136654445ea1836992daa02b
```

(also in the repository as `android/signing-cert.sha256`). To check a downloaded file on a computer
with the Android SDK build tools:

```sh
apksigner verify --print-certs Melarka-X.Y.Z.apk
```

`Signer #1 certificate SHA-256 digest` must equal the fingerprint above. If it differs, don't install the file.

### Updates

Install the newer APK over the old one; your server, sign-in and cache stay (Android accepts it
because the signing key is the same). Melarka doesn't check for updates itself. The
[Obtainium](https://obtainium.imranr.dev) app does it for you: in Obtainium choose **Add App**
and enter `https://github.com/aaronsuns/melarka`. It then installs new releases as they come out.

## 2. Connect to your server

1. Open Melarka. On first launch it asks for your server address, for example
   `https://music.example.com`.
2. Tap **Connect**. The app checks that the address answers `/api/v1/info`.
3. Sign in with your Melarka username and password, exactly as in the browser.

**A server with a certificate from your own CA** (a home server behind a private certificate
authority or a LAN proxy): install the CA certificate on the phone first, in **Settings → Security
(& privacy) → More security settings → Encryption & credentials → Install a certificate → CA
certificate** (the menu names vary a little between phones). Melarka trusts certificates you
installed this way, as the phone's browser does. A self-signed server certificate alone is not
enough.

## Settings

The app's own settings open from the web's **Me → Settings** (the **Android app settings** entry) and from the
"can't connect" screen:

- **Server**: the address in use, and changing it (it signs you out of the old one).
- **Offline cache**: how much is used, the size limit (1, 2, 5 or 10 GB), **Sync Favorites Now**,
  and clearing it. Favorites are cached automatically on Wi-Fi.
- **Stay Offline**: for a flight. The app stops trying to reach the server and plays from the
  offline cache until you turn it off.
- **Car autoplay** (below).
- **Background playback** (below).
- **About**: the app version.

When the server can't be reached, the app shows a "can't connect" screen instead of the page:
**Retry**, **Settings**, **Play Offline Favorites** (when some are cached) and **Stay Offline**. It
tries again by itself when the network comes back.

## Car autoplay

Most cars send "play" when Bluetooth connects, and Android passes it to the app that played last:
Melarka then resumes its last queue (from the cache when it can). For a sure start, use car
autoplay:

1. In Melarka's settings, turn on **Car autoplay**. On Android 12 and later, allow **Nearby
   devices** when asked (it is needed to list the cars paired with your phone).
2. Choose your car from the list (pair it in Android's Bluetooth settings first), or **Search
   Nearby**.
3. Android asks once to confirm that Melarka may use the car. Confirm.

From then on, when the car's Bluetooth connects, Melarka shuffles your favorites, even in the
background. If music is already playing (the car's own "play" came first), nothing changes.
**Test: Shuffle Favorites** in the settings runs the same thing now; under it the settings say what
happened the last time the car connected. Turning car autoplay off lets the car go.

On Android 8 to 11, Android doesn't wake Melarka for the car: autoplay works only while Melarka is
allowed to run in the background (see below), and not after you force-stop it.

## Automation apps: Tasker, Samsung Modes & Routines, …

Automation apps that can send an Android intent to start an activity (Tasker, Automate,
MacroDroid, …) can run the two shortcuts, for example when you get in the car or at a set time.
Have the app send an intent that starts an **activity**, with package
`io.github.aaronsuns.melarka` and one of these actions (nothing else is needed; extras and data
are ignored):

- `io.github.aaronsuns.melarka.action.SHUFFLE_FAVORITES`: Shuffle Favorites;
- `io.github.aaronsuns.melarka.action.RESUME`: Resume (your last queue, or your favorites
  shuffled when there is none).

In Tasker, for example, that is its **Send Intent** action with the target set to **Activity**.
Samsung's Modes and Routines can use them only if it offers a step on your phone that opens an
app with an action or sends an intent; otherwise, have it run a task in one of the apps above.
Melarka shows the same short message as the shortcuts; Android may not let an automation app
start it while that app itself runs in the background.

## Background playback and your phone's battery settings

If music stops when the screen is off, or car autoplay doesn't start, the phone's battery
optimisation is stopping Melarka. In Melarka's settings, **Background Playback → Allow Background
Playback** asks Android to let Melarka run in the background; confirm it. By hand, the same
setting is **Settings → Apps → Melarka → Battery → Unrestricted**.

Some phone makers add their own power management on top. The settings show the line for your
phone; the steps are:

- **Xiaomi, Redmi, POCO**: turn on **Autostart** for Melarka (Settings → Apps → Manage apps →
  Melarka) and set its **Battery saver** to **No restrictions**.
- **Huawei, Honor**: Settings → Battery → **App launch**, set Melarka to **Manage manually** and
  turn on all three switches.
- **OPPO, realme, OnePlus**: allow Melarka to **auto-launch** and allow its **background
  activity** (Settings → Apps → Melarka → Battery).
- **vivo, iQOO**: allow **Autostart** for Melarka and allow its **high background power
  consumption** (Settings → Battery).
- **Samsung**: add Melarka to **Never sleeping apps**: Settings → Battery → **Background usage
  limits**.

Menu names change between versions; <https://dontkillmyapp.com> keeps current steps for each
phone maker, with a page per brand: [Xiaomi](https://dontkillmyapp.com/xiaomi),
[Huawei](https://dontkillmyapp.com/huawei), [OnePlus](https://dontkillmyapp.com/oneplus),
[OPPO](https://dontkillmyapp.com/oppo), [vivo](https://dontkillmyapp.com/vivo),
[Samsung](https://dontkillmyapp.com/samsung).

## Language

The app's own screens (the server screen, the settings, the "can't connect" screen and the
offline player) follow the phone's language: English, 简体中文, 繁體中文 or svenska, and English for
every other language. Android 13 and later can set it for Melarka alone in **Settings → Apps →
Melarka → Language**. The music pages are the web app, which keeps its own language (the server's
default, or the one chosen in **Me → Settings**).

## Troubleshooting

- **"Can't connect" on first launch**: open `https://<your server>/api/v1/info` in the phone's
  browser. It must load without a certificate warning; for a private CA, install its certificate
  (see "Connect to your server").
- **Music stops with the screen off**: see "Background playback" above.
- **Car autoplay doesn't start**: check that the car is the one shown in the settings, that the
  battery setting above is done, and run **Test: Shuffle Favorites** to see whether favorites play
  at all (offline, only cached ones can).
- **Android blocks the install ("Install blocked", or Play Protect asks)**: allow your browser or
  file manager to install apps (step 1 above). If Play Protect says the app is unknown, choose
  **Install anyway**: Melarka is not on Google Play, and Play Protect doesn't know its key yet.
- **The fingerprint doesn't match**: the file isn't the one the release published. Download it again
  from the releases page, over a trusted network.
- **The server screen says the address isn't valid**: use `https://`, with no path. Plain `http://` is
  accepted only for `localhost` and `*.local`.
- **"App not installed" when updating**: the new APK must be signed by the same key as the
  installed one. An install from another source (such as F-Droid's own build) has to be
  uninstalled first; this deletes the offline cache.
