# The Melarka iPhone app

The iPhone app shows the same web app you use in a browser, full screen, but plays audio with a
native engine. That gives it what a web page on iOS can't do reliably:

- background playback that keeps going with the screen locked, with lock-screen, headphone and
  car controls;
- gapless transitions and a few tracks downloaded ahead;
- an offline cache of your favorites, synced on Wi-Fi, with a size limit you choose;
- Shortcuts actions, **Shuffle Favorites** and **Resume** (随机播放收藏 and 继续播放 on a Chinese phone), so
  music can start by itself when the phone connects to the car's Bluetooth;
- the current lyric line on the lock screen and car display.

It needs iOS 17 or later and a Melarka server reachable over **HTTPS**. The app accepts plain
`http://` only for `localhost` and `*.local` addresses, for development.

The app is not in the App Store. Each release publishes an unsigned IPA and a source file for
[SideStore](https://sidestore.io), which signs the app on your phone with your own Apple ID. A
free Apple ID is enough.

## 1. Install SideStore (once per phone)

SideStore's own guide, <https://docs.sidestore.io>, is the reference and is kept current; in
short:

1. On the iPhone, install **LocalDevVPN** from the App Store.
2. On a computer (macOS, Windows or Linux), download **iloader** from SideStore's site.
3. Connect the iPhone by USB and tap **Trust** on the phone if asked.
4. In iloader, sign in with your Apple ID, select your phone, and choose
   **Install SideStore (Stable)**.
5. On the iPhone, open **Settings → General → VPN & Device Management**, tap your Apple ID under
   *Developer App*, and trust it.
6. On iOS 16 or later, turn on **Settings → Privacy & Security → Developer Mode** (the phone
   restarts).
7. Open **LocalDevVPN** and tap **Connect**, then open **SideStore** and sign in with the same
   Apple ID.

## 2. Add the Melarka source

1. In SideStore, open the **Sources** tab and tap **+**.
2. Enter `https://aaronsuns.github.io/melarka/source.json` and add it.
3. Open the Melarka source and install **Melarka**. Keep LocalDevVPN connected while SideStore
   installs or refreshes apps.

## 3. Connect to your server

1. Open Melarka. On first launch it asks for your server address, for example
   `https://music.example.com`.
2. Tap connect. The app checks that the address answers `GET /api/v1/info`.
3. Sign in with your Melarka username and password, exactly as in the browser.

To switch servers later, open **Me → iPhone app settings** in the app and change the server
there; it signs you out of the old one.

## Native settings

**Me → iPhone app settings** opens the app's own settings:

- **Server**: the address in use, and changing it.
- **Offline cache**: how much is used, the size limit, **sync favorites now**, and clearing it.
  Favorites are cached automatically on Wi-Fi.
- **Car autoplay**: a step-by-step guide to starting playback when the car connects, and a test
  button.
- **About**: the app version.

### Starting playback in the car

Most cars send a "play" command when Bluetooth connects, and iOS passes it to the app that played
last. Melarka then resumes its last queue from the cache, or shuffles your favorites when the
queue is empty. For a sure start, add an automation in the **Shortcuts** app: **Automation → + →
Bluetooth**, pick your car, **Is Connected**, **Run Immediately**, and add Melarka's **Shuffle Favorites**
or **Resume** action (**随机播放收藏** or **继续播放** on a Chinese phone).

## Language

The app's own screens (the first-launch server screen, the native settings, the car guide, the
"can't connect" screen, alerts, and the Shortcuts actions and Siri phrases) follow the phone's language:
Chinese when the phone is set to Chinese (Simplified, or Traditional, which shows the Simplified text), and
English for every other language. There is no language setting in the app; change the phone's language in
**Settings → General → Language & Region**, or for Melarka alone in **Settings → Apps → Melarka → Language**.

The music pages inside the app are the web app, which keeps its own language: the server's default
(`LARK_LANGUAGE`) or the one chosen in **Me → Settings**.

## Updates and the 7-day refresh

Apps signed with a free Apple ID expire after 7 days. SideStore refreshes them in the background
while LocalDevVPN is connected; you can also refresh by hand in **My Apps**. If Melarka stops
opening, open SideStore with LocalDevVPN connected and refresh it.

When a new Melarka release is published, SideStore shows an update for it in **My Apps**. The app
and the server are released together, with the same version number.

A free Apple ID can have at most three sideloaded apps active at a time, SideStore included.

## Without the app

You don't need the app to use Melarka on an iPhone. Open your Melarka address in Safari and tap
**Share → Add to Home Screen**; it then opens full screen with lock-screen controls and its own
offline cache. Background playback is less reliable than in the app, because iOS can pause web
pages in the background.

**Volume normalization** (Settings) has no audible effect in Safari on an iPhone: iOS Safari
ignores a web page's volume control (`HTMLMediaElement.volume`), so every song plays at its own
level there. It works in desktop browsers and in the app, which applies the gain natively. Melarka
deliberately does not route playback through Web Audio to work around this: iOS suspends that when
the phone is locked, which would stop the music.

## Troubleshooting

- **"Can't connect" on first launch**: open `https://<your server>/api/v1/info` in Safari on the
  phone. It must load without a certificate warning. Self-signed certificates don't work.
- **The app won't open after a week**: the signature expired; refresh it in SideStore (see above).
- **Install fails in SideStore**: make sure LocalDevVPN is connected, then try again. SideStore's
  documentation covers pairing-file and signing errors.
