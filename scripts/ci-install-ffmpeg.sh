#!/usr/bin/env bash
# Installs Ubuntu's ffmpeg on a CI runner, fast: the runners' package mirror sometimes serves the ~90 MB of
# ffmpeg and its libraries at a few dozen kB/s, which turned one apt-get into a 24-minute step.
#
# - No recommended packages (the tests need the ffmpeg binary and its libraries, nothing else).
# - The .deb files are kept in DEB_CACHE (restored and saved by actions/cache in the workflow), so a run
#   downloads only what changed since the cache was made. apt checks every cached .deb against the hash in
#   the signed package index before it installs it, so a stale or tampered file is fetched again, never used.
# - The man-db trigger, which rebuilds the manual page index after every install, is switched off.
set -euo pipefail
cache="${DEB_CACHE:?set DEB_CACHE to a directory the runner user owns}"
cache="${cache/#\~/$HOME}"   # a workflow env value is not tilde-expanded
mkdir -p "$cache"
apt_opts=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o APT::Keep-Downloaded-Packages=true)

echo 'man-db man-db/auto-update boolean false' | sudo debconf-set-selections
sudo rm -f /var/lib/man-db/auto-update

shopt -s nullglob
debs=("$cache"/*.deb)
[ ${#debs[@]} -eq 0 ] || sudo cp "${debs[@]}" /var/cache/apt/archives/
echo "restored ${#debs[@]} cached packages"

sudo apt-get "${apt_opts[@]}" update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get "${apt_opts[@]}" install -y -qq --no-install-recommends ffmpeg

# Keep exactly what this install used, for the next run.
rm -f "$cache"/*.deb
cp /var/cache/apt/archives/*.deb "$cache"/
echo "cached $(ls "$cache" | wc -l) packages"
ffmpeg -hide_banner -version | head -1
