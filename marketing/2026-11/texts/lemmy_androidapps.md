The Android app for Melarka, a music server you run at home (one Docker container). I built both because no Subsonic client held up for my family in the background and in the car.

- native Kotlin app, not a web wrapper: keeps playing with the screen off, lock-screen and car controls, gapless
- an offline cache of your favorites for walks with no signal
- starts playing by itself when the phone connects to the car over Bluetooth, with the current lyric line on the car display
- synced lyrics, an account per family member
- interface in English, Chinese and Swedish

It needs your own Melarka server, so it's no use without one. Signed APK on the GitHub releases page; F-Droid is submitted but waiting until the app is a bit older.

**AI disclosure:** built with a lot of help from Claude Code (most of the code, tests and docs); I designed it, review it and test it on our phones every day.
