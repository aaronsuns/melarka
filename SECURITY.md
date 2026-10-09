# Security policy

## Reporting a vulnerability

Please **don't open a public issue** for a security problem. Report it privately through GitHub:

1. Go to the repository's [**Security** tab](https://github.com/aaronsuns/melarka/security).
2. Choose **Report a vulnerability**
   ([direct link](https://github.com/aaronsuns/melarka/security/advisories/new)).
3. Describe the problem, the affected version (`docker run --rm ghcr.io/aaronsuns/melarka:<tag> --version`,
   or **Me** in the web app), and how to reproduce it.

You'll get an answer in the advisory, usually within a week. Once a fix is released, the
advisory is published with credit to you, unless you prefer otherwise.

Melarka is maintained by one person in their spare time, so please allow a reasonable time for a
fix before disclosing the problem publicly.

## Supported versions

Only the newest release gets security fixes. Upgrade with
`docker compose pull && docker compose up -d` after changing the image tag (see the README).

## Scope

In scope: the Melarka server, its web app, the iPhone app, the Docker image and the release
artifacts published from this repository.

Out of scope: your reverse proxy and TLS setup, and the third-party services Melarka can talk to
(YouTube, lyrics and cover providers, Last.fm). Report problems in yt-dlp or ffmpeg to those
projects.

## Hardening tips

- Expose Melarka only through an HTTPS reverse proxy; the compose example binds to `127.0.0.1`.
- Rate-limit `POST /api/v1/auth/login` at the proxy (the README's nginx example does).
- Change the first admin's password after the first sign-in, and give household members the
  member role, not admin.
