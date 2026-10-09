#!/usr/bin/env bash
# Release gate: secrets (gitleaks, full history) + a private denylist over the tree, binaries, file names,
# every commit diff, every commit message and every ref/tag message.
# Prints locations and pattern numbers only — never the matched text (CI logs are public).
#
#   scripts/oss-check.sh [--denylist FILE] [--no-history] [--allow-skip]
#
#   --denylist FILE  one extended regex per line; '#' comments and blank lines ignored; CRLF tolerated.
#                    Without it the denylist is read from the env var OSS_DENYLIST (the file's content; CI).
#   --no-history     gitleaks over the working tree instead of the full history, and no history scans.
#   --allow-skip     permit running with NO denylist at all (gitleaks only, with a warning). CI sets this
#                    ONLY for pull_request events from forks, which get no secrets. Without the flag a
#                    missing, empty or comment-only denylist is an error: the gate fails closed.
#
# Exit codes: 0 clean, 1 hits, 2 usage or denylist error (including an invalid pattern, reported by number).
# Output lines (never matched text; a path that itself matches is shown as #<index in ls-files>):
#   tree <path>:<line>|binary pattern#N     path #<index> pattern#N     history <sha12> pattern#N
#   message <sha12> pattern#N               ref pattern#N
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
usage() { echo "usage: $0 [--denylist FILE] [--no-history] [--allow-skip]" >&2; exit 2; }
deny=""; history=1; allow_skip=0; given=0
while [ $# -gt 0 ]; do case "$1" in
  --denylist) [ $# -ge 2 ] || usage
              [ -f "$2" ] || { echo "oss-check: denylist file not found or not a file" >&2; exit 2; }
              deny="$(cat "$2")"; given=1; shift 2 ;;
  --no-history) history=0; shift ;;
  --allow-skip) allow_skip=1; shift ;;
  *) usage ;;
esac; done
if [ "$given" = 0 ]; then deny="${OSS_DENYLIST:-}"; fi

# Parse the denylist: strip CR and trailing blanks, drop comments and blank lines, validate each regex.
pats=(); n=0
if [ -n "$deny" ]; then
  while IFS= read -r pat || [ -n "$pat" ]; do
    pat="${pat%$'\r'}"; pat="${pat%"${pat##*[![:space:]]}"}"
    case "$pat" in ''|'#'*) continue ;; esac
    n=$((n+1)); pats+=("$pat")
    rc=0; printf '' | grep -E -e "$pat" >/dev/null 2>&1 || rc=$?
    [ "$rc" -le 1 ] || { echo "oss-check: pattern#$n invalid (not a valid extended regex)" >&2; exit 2; }
  done <<<"$deny"
  [ "$n" -gt 0 ] || { echo "oss-check: denylist has no patterns (empty or comments only)" >&2; exit 2; }
elif [ "$given" = 1 ]; then
  echo "oss-check: denylist file is empty" >&2; exit 2
elif [ "$allow_skip" = 0 ]; then
  echo "oss-check: no denylist (use --denylist FILE or OSS_DENYLIST); pass --allow-skip only for fork PRs" >&2; exit 2
fi

fail=0
if [ "$history" = 1 ]; then gitleaks git --no-banner --redact --log-opts="--all" . || fail=1
else gitleaks dir --no-banner --redact . || fail=1; fi

if [ "$n" = 0 ]; then
  echo "oss-check: no denylist (--allow-skip) — denylist scan SKIPPED" >&2
else
  work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
  ok1() { "$@" || [ $? = 1 ]; }     # grep-style commands: status 1 means "no match", anything else is an error
  paths=(); while IFS= read -r -d '' p; do paths+=("$p"); done < <(git ls-files -z)
  bins=();  while IFS= read -r -d '' rec; do
    case "${rec%%$'\t'*}" in *i/-text*) bins+=("${rec#*$'\t'}") ;; esac
  done < <(git ls-files -z --eol)
  # Cache each tracked binary's index content and its printable strings once.
  i=0; for f in ${bins[@]+"${bins[@]}"}; do i=$((i+1))
    git show ":$f" > "$work/bin.$i"; strings -a < "$work/bin.$i" > "$work/bin.$i.str" 2>/dev/null || : > "$work/bin.$i.str"
  done
  git log --all --format='%x01%n%B' > "$work/msgs"; git log --all --format=%H > "$work/shas"
  git for-each-ref --format='%(refname)%0a%(contents)' > "$work/refs"

  # Label for a tracked path: the path itself, or #<index> when any pattern matches the path.
  label() { local f="$1" k=0 p q
    for q in "${pats[@]}"; do
      if printf '%s\n' "$f" | grep -qiE -e "$q"; then
        for p in "${paths[@]}"; do k=$((k+1)); [ "$p" = "$f" ] && { echo "#$k"; return; }; done
      fi
    done
    echo "$f"; }

  k=0
  for pat in "${pats[@]}"; do k=$((k+1))
    # File names. The path is never printed: only its index.
    idx=0; for p in "${paths[@]}"; do idx=$((idx+1))
      if printf '%s\n' "$p" | grep -qiE -e "$pat"; then echo "path #$idx pattern#$k"; fail=1; fi
    done
    # Tree text files, as staged in the index (not the working tree). Location only.
    while IFS= read -r -d '' f; do
      lab="$(label "$f")"
      for ln in $(git show ":$f" | { grep -naiE -e "$pat" || [ $? = 1 ]; } | cut -d: -f1); do echo "tree $lab:$ln pattern#$k"; done
      fail=1
    done < <(ok1 git grep --cached -I -l -z -iE -e "$pat")
    # Tree binary files: raw bytes, and printable strings.
    i=0; for f in ${bins[@]+"${bins[@]}"}; do i=$((i+1))
      if grep -qaiE -e "$pat" "$work/bin.$i" || grep -qaiE -e "$pat" "$work/bin.$i.str"; then
        echo "tree $(label "$f"):binary pattern#$k"; fail=1; fi
    done
    # Commit messages: one dump, grep line numbers, map each back to its commit.
    for ln in $(ok1 grep -naiE -e "$pat" "$work/msgs" | cut -d: -f1); do
      awk -v L="$ln" 'NR<=L && index($0, sprintf("%c",1))==1 {c++} NR==L {print c; exit}' "$work/msgs"
    done | sort -un | while read -r c; do
      sha=$(sed -n "${c}p" "$work/shas"); echo "message ${sha:0:12} pattern#$k"
    done > "$work/m.out"
    if [ -s "$work/m.out" ]; then cat "$work/m.out"; fail=1; fi
    # Ref names and annotated tag messages.
    if grep -qaiE -e "$pat" "$work/refs"; then echo "ref pattern#$k"; fail=1; fi
    # History: every commit whose diff adds or removes a matching line (catches words added then removed).
    if [ "$history" = 1 ]; then
      git log --all -a -i -G "$pat" --format=%H > "$work/h.out"
      if [ -s "$work/h.out" ]; then while read -r sha; do echo "history ${sha:0:12} pattern#$k"; done < "$work/h.out"; fail=1; fi
    fi
  done
fi
[ "$fail" = 0 ] && echo "oss-check: clean — checked $n patterns" || { echo "oss-check: FAILED — checked $n patterns" >&2; exit 1; }
