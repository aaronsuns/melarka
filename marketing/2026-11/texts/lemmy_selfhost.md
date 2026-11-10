I ran Navidrome for a long time, but never found a phone client that held up for my family in the background and in the car. So I wrote a server and its apps together, and it has replaced Spotify for us.

- one Docker container (Go + SQLite); it reads your music folders and never rewrites your files or tags
- an account per family member: their own favorites, playlists and personal radio
- synced lyrics; anyone can nudge lyrics that run early or late
- native Android (Kotlin) and iPhone apps: background play, lock-screen and car controls, gapless, an offline cache of favorites, and it starts playing when the phone connects to the car
- optional: add songs from YouTube (yt-dlp) into your own library

The catch: it has its own API, so it is not Subsonic-compatible and your existing clients won't work with it. It's about a month old; we use it every day at home.

**AI disclosure:** built with a lot of help from Claude Code. It wrote most of the code, tests and docs; I designed it, steer it, review what goes in and test it on our phones every day.

Site with a one-minute tour: https://melarka.zexa.se
