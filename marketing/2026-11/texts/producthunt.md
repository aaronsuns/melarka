# Product Hunt launch (draft)

Name: Melarka
Link: https://melarka.zexa.se
Tagline (max 60): Your family's own Spotify: a music server with phone apps
Topics: Open Source, Music, Android (and iOS if a 4th is allowed)

Description (max 260):
A music server you run at home, with native iPhone and Android apps. An account per family member, synced lyrics, offline favorites, and it starts playing when your phone connects to the car. One Docker container; it never rewrites your files. Open source.

Gallery: producthunt/gallery_1..5.png (1270x760), thumbnail producthunt/thumbnail.png (240x240).
Video: https://youtu.be/rkZQh5j8_ro

## Maker's first comment

Hi Product Hunt 👋

I built Melarka because my family wanted Spotify, and I wanted our music on our own server. I ran Navidrome for a long time, but no phone client held up for us where we listen most: in the car and on walks, with the screen off.

So I wrote the server and its apps together:

- it reads your music folders and never touches your files or tags
- everyone at home gets an account: their own favorites, playlists and radio
- synced lyrics, also on the lock screen and the car's display
- favorites cached on the phone for when there's no signal
- turn on car autoplay and your favorites start when the phone connects to the car

It's one Docker container (Go + SQLite), installed with one command, and it's open source (AGPL-3.0).

Honest caveats: it has its own API, so it isn't Subsonic-compatible. The iPhone app isn't in the App Store: you sign it with your own free Apple ID, easiest with a Mac (Xcode installs it and re-signs it weekly by itself), and the Android APK is on GitHub. And I built it with a lot of help from Claude Code: it wrote most of the code; I designed it, review it, and we use it every day.

I'd love to hear what you'd want from it, and what breaks. Thanks for looking!
