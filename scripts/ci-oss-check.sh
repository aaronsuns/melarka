#!/usr/bin/env bash
# CI wrapper for scripts/oss-check.sh (full mode: gitleaks over the whole history + the denylist scans).
#   OSS_DENYLIST  the denylist's content (the repository secret); written to a private temp file.
#   OSS_FORK_PR   "true" only for pull_request events from a fork. Forks get no secrets, so only then
#                 may the denylist be missing (--allow-skip; gitleaks still runs). Anything else,
#                 including an unset or empty OSS_FORK_PR, fails closed on a missing denylist.
# Never prints the denylist.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
if [ "${OSS_FORK_PR:-}" = true ]; then
  echo "oss-check: pull request from a fork: the denylist may be skipped" >&2
  OSS_DENYLIST="" exec "$here/oss-check.sh" --allow-skip "$@"
fi
deny="$(umask 077; mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/oss-denylist.XXXXXX")"
trap 'rm -f "$deny"' EXIT
printf '%s\n' "${OSS_DENYLIST:-}" > "$deny"
unset OSS_DENYLIST
"$here/oss-check.sh" --denylist "$deny" "$@"
