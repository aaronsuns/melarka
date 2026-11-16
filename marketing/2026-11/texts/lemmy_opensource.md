I built a music server for my family and put it under AGPL-3.0: Melarka. Go + SQLite in one Docker container, plus native Android (Kotlin) and iPhone apps, all in one public repo: https://github.com/aaronsuns/melarka

Why it might be of interest here:

- Everything is in the repo: server, both apps, docs and the build scripts. You can run it, read it and fork it.
- It reads your own music folders and never rewrites your files or tags. No account at a third party, no tracking; the only call home is an optional, opt-in install ping.
- An account per family member (own favorites, playlists, personal radio), synced lyrics, an offline cache for walks, and the Android app starts playing when the phone connects to the car.
- Honest limits: it has its own API, so it is not Subsonic-compatible, and the iPhone app is not in the App Store (Apple's developer fee for something that earns nothing); you install it from a Mac with Xcode.

**AI disclosure:** most of the code, tests and docs were written with Claude Code. I designed it, steer it, review what goes in and use it every day on our phones. I know that is a sore point in open source; the repo says so up front, and I'd rather have the argument in the open than hide it.

Site with a one-minute tour: https://melarka.zexa.se
