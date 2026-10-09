#!/usr/bin/env bash
# Prints the body of CHANGELOG.md's "## [VERSION]" section (the GitHub Release notes).
# Exit 1 when the section is missing or empty, so a tag without release notes fails early.
#   scripts/changelog-section.sh 0.1.0 [CHANGELOG.md]
set -euo pipefail
[ $# -ge 1 ] || { echo "usage: $0 VERSION [CHANGELOG]" >&2; exit 2; }
v="$1"; file="${2:-CHANGELOG.md}"
out="$(awk -v head="## [$v]" '
  index($0, head) == 1 && (length($0) == length(head) || substr($0, length(head) + 1, 1) == " ") { f = 1; next }
  /^## \[/ || /^\[[^]]+\]: / { f = 0 }
  f' "$file" | sed -e '/./,$!d')"
# Drop trailing blank lines.
out="$(printf '%s\n' "$out" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')"
[ -n "$out" ] || { echo "changelog-section: no '## [$v]' section in $file" >&2; exit 1; }
printf '%s\n' "$out"
