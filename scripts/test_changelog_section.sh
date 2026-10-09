#!/usr/bin/env bash
# Tests scripts/changelog-section.sh against a fixture CHANGELOG.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/changelog-section.sh"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
die() { echo "FAIL: $1"; exit 1; }
cat > "$tmp/CHANGELOG.md" <<'MD'
# Changelog

## [Unreleased]

## [1.10.0] - 2026-11-01

Ten.

## [1.1.0] - 2026-10-20

### Fixed

- One.

## [1.0.0] - 2026-10-01

First.

[Unreleased]: https://example.com/compare/v1.10.0...HEAD
[1.0.0]: https://example.com/v1.0.0
MD
[ "$("$script" 1.1.0 "$tmp/CHANGELOG.md")" = "$(printf '### Fixed\n\n- One.')" ] || die "middle section"
[ "$("$script" 1.0.0 "$tmp/CHANGELOG.md")" = "First." ] || die "last section must stop before link references"
[ "$("$script" 1.10.0 "$tmp/CHANGELOG.md")" = "Ten." ] || die "1.10.0"
rc=0; "$script" 1.1 "$tmp/CHANGELOG.md" 2>/dev/null || rc=$?; [ "$rc" = 1 ] || die "prefix version must not match"
rc=0; "$script" 1.0.0.0 "$tmp/CHANGELOG.md" 2>/dev/null || rc=$?; [ "$rc" = 1 ] || die "unknown version"
rc=0; "$script" Unreleased "$tmp/CHANGELOG.md" 2>/dev/null || rc=$?; [ "$rc" = 1 ] || die "empty section"
rc=0; "$script" 1x1.0 "$tmp/CHANGELOG.md" 2>/dev/null || rc=$?; [ "$rc" = 1 ] || die "dots are literal"
echo "test_changelog_section: PASS"
