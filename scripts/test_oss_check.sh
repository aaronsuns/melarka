#!/usr/bin/env bash
# Tests scripts/oss-check.sh against throwaway repos. Uses generic test words only.
set -euo pipefail
script="$(cd "$(dirname "$0")" && pwd)/oss-check.sh"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
g() { git -c user.email=t@example.com -c user.name=t "$@"; }
die() { echo "FAIL: $1"; cat "$tmp/out" 2>/dev/null || true; exit 1; }
printf 'forbiddenword\notherword\n' > "$tmp/deny.txt"

mk() { # $1 dir, $2 file content, $3 second-commit message
  mkdir -p "$1"; ( cd "$1"; git init -q; printf '%s\n' "$2" > a.txt; git add a.txt
    g commit -qm "first"; g commit -q --allow-empty -m "$3" ); }
# run DIR EXPECTED_RC ARGS... : runs the gate in DIR, output to $tmp/out
run() { local d="$1" want="$2"; shift 2; local rc=0
  ( cd "$d" && "$script" "$@" >"$tmp/out" 2>&1 ) || rc=$?
  [ "$rc" = "$want" ] || die "expected exit $want, got $rc ($*)"; }
has() { grep -qE -- "$1" "$tmp/out" || die "missing output /$1/"; }
lacks() { ! grep -q -- "$1" "$tmp/out" || die "output leaks '$1'"; }

mk "$tmp/bad" "hello forbiddenword" "mentions otherword here"
run "$tmp/bad" 1 --denylist "$tmp/deny.txt"
has '^tree a.txt:1 pattern#1$'; has '^message [0-9a-f]{12} pattern#2$'
lacks forbiddenword; lacks otherword

mk "$tmp/good" "hello world" "plain message"
run "$tmp/good" 0 --denylist "$tmp/deny.txt"
has 'oss-check: clean'; has 'checked 2 patterns'

# No denylist: fails closed; skipping needs the explicit flag.
run "$tmp/good" 2 --no-history
run "$tmp/good" 2 --no-history --denylist "$tmp/missing.txt"
: > "$tmp/empty.txt"; run "$tmp/good" 2 --no-history --denylist "$tmp/empty.txt"
printf '# only a comment\n\n' > "$tmp/comment.txt"; run "$tmp/good" 2 --no-history --denylist "$tmp/comment.txt"
( cd "$tmp/good" && OSS_DENYLIST="" "$script" --no-history >"$tmp/out" 2>&1 ) && die "empty OSS_DENYLIST must fail" || [ $? = 2 ] || die "empty env: wrong exit"
run "$tmp/bad" 0 --no-history --allow-skip; has SKIPPED
run "$tmp/good" 2 --bogus

# Env var path (CI).
rc=0; ( cd "$tmp/bad" && OSS_DENYLIST="$(cat "$tmp/deny.txt")" "$script" --no-history >"$tmp/out" 2>&1 ) || rc=$?
[ "$rc" = 1 ] || die "OSS_DENYLIST expected exit 1, got $rc"

# Invalid regex: exit 2, names the number only.
printf 'fine\n(unbalancedword\n' > "$tmp/badre.txt"
run "$tmp/good" 2 --no-history --denylist "$tmp/badre.txt"
has 'pattern#2 invalid'; lacks unbalancedword

# CRLF and trailing blanks in the denylist.
printf 'forbiddenword\r\notherword  \r\n' > "$tmp/crlf.txt"
run "$tmp/bad" 1 --no-history --denylist "$tmp/crlf.txt"
has '^tree a.txt:1 pattern#1$'; has '^message [0-9a-f]{12} pattern#2$'

# Binary files are scanned too (the match is in a NUL-bearing blob).
mk "$tmp/bin" "ok" "ok"
( cd "$tmp/bin"; printf '\0\0PNG\0xx forbiddenword yy\0' > img.png; git add img.png; g commit -qm "add image" )
run "$tmp/bin" 1 --no-history --denylist "$tmp/deny.txt"
has '^tree img.png:binary pattern#1$'; lacks forbiddenword

# History: a word added and later removed is still caught, by commit only.
mkdir "$tmp/hist"; ( cd "$tmp/hist"; git init -q; echo "secret forbiddenword" > f.txt; git add f.txt; g commit -qm one
  echo "clean" > f.txt; g commit -qam two )
run "$tmp/hist" 0 --no-history --denylist "$tmp/deny.txt"
run "$tmp/hist" 1 --denylist "$tmp/deny.txt"
has '^history [0-9a-f]{12} pattern#1$'; lacks forbiddenword

# File names are scanned, and never printed when they are what matched.
mkdir "$tmp/path"; ( cd "$tmp/path"; git init -q; echo "x forbiddenword" > forbiddenword-notes.txt; echo hi > ok.txt
  git add .; g commit -qm one )
run "$tmp/path" 1 --no-history --denylist "$tmp/deny.txt"
has '^path #[0-9]+ pattern#1$'; has '^tree #[0-9]+:1 pattern#1$'; lacks forbiddenword

# The tree scan reads the index: a tracked file deleted from disk is still scanned.
( cd "$tmp/bad"; rm a.txt )
run "$tmp/bad" 1 --no-history --denylist "$tmp/deny.txt"; has '^tree a.txt:1 pattern#1$'

# Annotated tag messages and ref names are scanned.
mk "$tmp/tag" "fine" "fine"
( cd "$tmp/tag"; g tag -a v1 -m "release otherword" )
run "$tmp/tag" 1 --no-history --denylist "$tmp/deny.txt"; has '^ref pattern#2$'; lacks otherword
# CI wrapper (scripts/ci-oss-check.sh): the secret goes through a temp file; only a fork PR may skip.
wrap="$(dirname "$script")/ci-oss-check.sh"
cw() { local d="$1" want="$2"; shift 2; local rc=0
  ( cd "$d" && env "$@" "$wrap" --no-history >"$tmp/out" 2>&1 ) || rc=$?
  [ "$rc" = "$want" ] || die "ci wrapper: expected exit $want, got $rc ($*)"; }
cw "$tmp/tag" 1 OSS_DENYLIST="$(cat "$tmp/deny.txt")"; lacks otherword
cw "$tmp/good" 0 OSS_DENYLIST="$(cat "$tmp/deny.txt")"; has 'checked 2 patterns'
cw "$tmp/good" 2 OSS_DENYLIST=
cw "$tmp/good" 2 OSS_DENYLIST= OSS_FORK_PR=false
cw "$tmp/good" 2 OSS_DENYLIST= OSS_FORK_PR=
cw "$tmp/tag" 0 OSS_DENYLIST= OSS_FORK_PR=true; has SKIPPED
cw "$tmp/tag" 0 OSS_DENYLIST="$(cat "$tmp/deny.txt")" OSS_FORK_PR=true; has SKIPPED
mkdir "$tmp/rt"; cw "$tmp/good" 0 OSS_DENYLIST="$(cat "$tmp/deny.txt")" RUNNER_TEMP="$tmp/rt"
[ -z "$(ls -A "$tmp/rt")" ] || die "ci wrapper left the denylist temp file behind"
echo "test_oss_check: PASS"
