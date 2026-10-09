#!/usr/bin/env bash
# Renders the app icons from docs/brand/melarka-icon.svg: the web favicon and
# PWA icons in web/public/ and the iPhone app icon. Needs rsvg-convert (librsvg)
# and ImageMagick's magick. Icons are opaque (iOS refuses an app icon with
# alpha) and carry no metadata.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SVG="$ROOT/docs/brand/melarka-icon.svg"
render() { # size, output
  rsvg-convert -w "$1" -h "$1" "$SVG" | magick - -background '#121212' -alpha remove -alpha off -strip -define png:exclude-chunks=date,time "PNG24:$2"
}
render 32 "$ROOT/web/public/favicon-32.png"
render 180 "$ROOT/web/public/apple-touch-icon.png"
render 192 "$ROOT/web/public/icon-192.png"
render 512 "$ROOT/web/public/icon-512.png"
render 1024 "$ROOT/ios/Sources/App/Assets.xcassets/AppIcon.appiconset/icon-1024.png"
cp "$SVG" "$ROOT/web/public/favicon.svg"
