# Contributing to Melarka

Thanks for helping. Bug reports, fixes and translations are all welcome. For a larger feature,
open an issue first so we can agree on the shape before you write it.

Found a security problem? Please don't open an issue; follow [SECURITY.md](SECURITY.md).

## Melarka and `lark`

The product is called **Melarka**, but the code uses the internal code name **`lark`**: the Go
module and packages, the `lark` binary, the `LARK_*` environment variables, the database
`lark.db`, the `window.larkNative` bridge, the Swift types and the Xcode target `Lark`.

- New code keeps using `lark` identifiers. Please don't rename existing ones to melarka.
- Every new string a user can see says **Melarka**.

## Build and test

You need Go 1.26, Node.js 22 and ffmpeg. Playwright installs its own browsers with
`cd web && npx playwright install webkit chromium`.

```bash
go vet ./... && go test ./...          # server
cd web && npm ci && npm test           # web unit tests
cd web && npm run build && npm run e2e # end-to-end, against the freshly built web app
make dev                               # run it locally on 127.0.0.1:4600 (admin / admin)
```

`make dev` serves the built web app from disk; for hot reload, also run `cd web && npm run dev`
(port 5173). No test touches the network: test fixtures are trimmed recordings of the public
APIs, with any keys replaced by fake ones.

The **iPhone app** in `ios/` needs a Mac with Xcode and XcodeGen; see [ios/README.md](ios/README.md).

The **README screenshots** are captured from the end-to-end test server, started with a made-up
demo library and channels (invented names, generated test tones and covers, `web/e2e/demo/`):
`cd web && npm run build && LARK_SCREENSHOTS=1 npx playwright test screenshots --project=iphone-webkit`.
The app icons are rendered from `docs/brand/melarka-icon.svg` by `scripts/render-icons.sh`.

The conventions (package layout, no network in tests, append-only migrations, translations,
error codes, admin routes, commit messages) are in [AGENTS.md](AGENTS.md), "Working on the
code". They apply to people as much as to agents. [docs/architecture.md](docs/architecture.md)
gives the overview.

## Pull requests

- One topic per pull request, with commit messages like `fix(web): …` or `feat(lyrics): …`.
- Tests for the change, and `go test ./...`, `cd web && npm test` and `npm run build` pass
  (plus `npm run e2e` for UI flows). CI runs them all on your pull request.
- New UI text exists in all four locales (`en` first, then `zh-Hans`, `zh-Hant`, `sv`). If you
  don't speak one of them, say so in the pull request and we'll help.
- A new `config.yaml` key or environment variable is documented in
  [docs/configuration.md](docs/configuration.md) and `deploy/config.example.yaml`
  (`internal/config/docs_test.go` checks this).
- Nothing writes to users' audio files, and no test calls a real external service.
- No personal data: no real email addresses, tokens, hostnames or account names in code, fixtures
  or docs. Use `example.com` and made-up users such as `alice` and `bob`.
- User-visible changes get a line under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md).

## Contributor License Agreement

Melarka is licensed under the AGPL-3.0-or-later and is also offered under commercial licences by
its author, Aaron Sun. To keep that possible, every contribution is made under the
[Contributor License Agreement](CLA.md) (CLA).

**CLA Assistant asks you to sign on your first PR.** A bot comments on the pull request with a
link; sign in with GitHub and accept. You sign once, and it covers all your later
contributions. A pull request can't be merged until the CLA is signed.

In short, the CLA:

- grants Aaron Sun and his successors and assignees a copyright licence to use, modify,
  sublicense and relicense your contribution under any terms, including the AGPL and commercial
  licences;
- grants a patent licence for any of your patents that your contribution necessarily infringes;
- confirms that you have the right to contribute it, including your employer's permission if
  your employer has rights to your work;
- waives moral rights in the contribution, as far as the law allows;
- leaves you the copyright to your contribution.

The [full text](CLA.md) is what you agree to; this summary is not.

## Code of conduct

Be kind and assume good intent. Harassment, insults or personal attacks are not welcome in issues,
pull requests or anywhere else in the project, and the maintainer may remove such content and
block those responsible.
