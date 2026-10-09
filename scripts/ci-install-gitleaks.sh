#!/usr/bin/env bash
# Installs the pinned gitleaks binary on a Linux x64 CI runner, checksum-verified (no gitleaks-action needed).
# Bump the version and the sha256 together (from the release's gitleaks_<v>_checksums.txt).
set -euo pipefail
v=8.30.1
sum=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb
f="gitleaks_${v}_linux_x64.tar.gz"
dir="$(mktemp -d)"; trap 'rm -rf "$dir"' EXIT
curl -fsSLo "$dir/$f" "https://github.com/gitleaks/gitleaks/releases/download/v$v/$f"
echo "$sum  $dir/$f" | sha256sum -c -
tar -xzf "$dir/$f" -C "$dir" gitleaks
sudo install -m 0755 "$dir/gitleaks" /usr/local/bin/gitleaks
gitleaks version
