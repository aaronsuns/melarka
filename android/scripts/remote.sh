#!/usr/bin/env bash
# Build and test the Android app on a Mac (or Linux box) with a JDK 17 and the Android SDK, reachable over SSH
# (set LARK_BUILD_HOST=user@host). Nothing is installed on this machine.
# Usage: android/scripts/remote.sh sync | test | build | run '<cmd>' | smoke [gradle args]
# smoke: the emulator smoke test (emulator-smoke.sh) there, with the server's sources synced too.
# Releases are built by the GitHub release workflow, not here.
set -euo pipefail
HOST="${LARK_BUILD_HOST:?set LARK_BUILD_HOST=user@your-mac (a machine with JDK 17 and the Android SDK, reachable over SSH)}"
RDIR="lark-android-build"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

sync_tree() {
  # android/ plus what the build reads from the rest of the repo: the version (ios/project.yml) and the brand art.
  # Excluded paths are protected from --delete, so the remote's build dirs and Gradle state survive.
  ssh -o BatchMode=yes "$HOST" "mkdir -p $RDIR/android $RDIR/ios $RDIR/docs/brand"
  rsync -az --delete --exclude /build --exclude /app/build --exclude /buildSrc/build --exclude .gradle --exclude .kotlin --exclude local.properties \
    --exclude '.env*' --exclude .vscode \
    --exclude-from=<(cd "$ROOT" && git ls-files -oi --exclude-standard --directory | sed 's|^|/|') \
    --rsync-path=/opt/homebrew/bin/rsync -e "ssh -o BatchMode=yes" "$ROOT/android/" "$HOST:$RDIR/android/"
  rsync -az --rsync-path=/opt/homebrew/bin/rsync -e "ssh -o BatchMode=yes" "$ROOT/ios/project.yml" "$HOST:$RDIR/ios/"
  rsync -az --delete --rsync-path=/opt/homebrew/bin/rsync -e "ssh -o BatchMode=yes" "$ROOT/docs/brand/" "$HOST:$RDIR/docs/brand/"
}

sync_server() {  # what emulator-smoke.sh needs besides android/: the server's Go sources and the web app (built there)
  local d
  for d in cmd internal scripts web; do
    rsync -az --delete --exclude node_modules --exclude /dist --exclude test-results --exclude playwright-report \
      --rsync-path=/opt/homebrew/bin/rsync -e "ssh -o BatchMode=yes" "$ROOT/$d/" "$HOST:$RDIR/$d/"
  done
  rsync -az --rsync-path=/opt/homebrew/bin/rsync -e "ssh -o BatchMode=yes" "$ROOT/go.mod" "$ROOT/go.sum" "$HOST:$RDIR/"
}

remote() {  # runs $1 in zsh on the build host, in the synced android/ tree, with the JDK and SDK selected
  ssh -o BatchMode=yes "$HOST" /bin/zsh -s <<REMOTE
set -euo pipefail
export JAVA_HOME=/opt/homebrew/opt/openjdk@17
export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
export PATH=\$JAVA_HOME/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
cd ~/$RDIR/android
$1
REMOTE
}

cmd="${1:-}"; shift || true
case "$cmd" in
  sync)  sync_tree ;;
  test)  sync_tree; remote "./gradlew --console=plain testDebugUnitTest lintDebug" ;;
  build) sync_tree; remote "./gradlew --console=plain assembleDebug && ls -l app/build/outputs/apk/debug/app-debug.apk" ;;
  run)   sync_tree; remote "$1" ;;
  smoke) sync_tree; sync_server; args=""; [[ $# -gt 0 ]] && args=$(printf '%q ' "$@")
         remote "LARK_WEB_BUILD=1 scripts/emulator-smoke.sh $args" ;;
  *) echo "usage: $0 sync|test|build|run '<cmd>'|smoke [gradle args]" >&2; exit 2 ;;
esac
