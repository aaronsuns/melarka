#!/usr/bin/env bash
# Build and test the iOS app on a Mac with Xcode, reachable over SSH (set LARK_BUILD_HOST=user@host).
# Usage: scripts/remote.sh gen | build | test [LarkTests/Class[/method]] | run '<cmd>'
# Releases are built by the GitHub release workflow, not here.
set -euo pipefail
HOST="${LARK_BUILD_HOST:?set LARK_BUILD_HOST=user@your-mac (a Mac with Xcode, reachable over SSH)}"
RDIR="lark-ios-build"
SIM_NAME="Lark Test iPhone"
SIM_TYPE="com.apple.CoreSimulator.SimDeviceType.iPhone-17"
SIM_RUNTIME="com.apple.CoreSimulator.SimRuntime.iOS-27-0"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

sync_tree() {
  # Excluded paths are also protected from --delete, so the remote's generated project and build dir survive.
  # Git-ignored files, editor settings and any .env* never travel (local rsync is openrsync: no dir-merge filters).
  rsync -az --delete --exclude .git --exclude build --exclude DerivedData --exclude Lark.xcodeproj \
    --exclude '.env*' --exclude .vscode \
    --exclude-from=<(cd "$ROOT" && git ls-files -oi --exclude-standard --directory | sed 's|^|/|') \
    --rsync-path=/opt/homebrew/bin/rsync -e "ssh -o BatchMode=yes" "$ROOT/" "$HOST:$RDIR/"
}

remote() {  # runs $1 in zsh on the build Mac, in the synced tree, with Xcode selected
  ssh -o BatchMode=yes "$HOST" /bin/zsh -s <<EOF
set -euo pipefail
export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
export PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
cd ~/$RDIR
$1
EOF
}

# Info.plist is generated from project.yml and committed; warn when the committed copy is stale.
GEN='mkdir -p build; cp Sources/App/Info.plist build/Info.plist.committed 2>/dev/null || : >build/Info.plist.committed
xcodegen generate --quiet
cmp -s Sources/App/Info.plist build/Info.plist.committed || echo "warning: Sources/App/Info.plist is stale; run scripts/remote.sh gen and commit it"'
SIM='udid=$(xcrun simctl list devices available | sed -n "s/^ *'"$SIM_NAME"' (\([0-9A-F-]*\)).*/\1/p" | head -1)
if [[ -z "$udid" ]]; then udid=$(xcrun simctl create "'"$SIM_NAME"'" '"$SIM_TYPE"' '"$SIM_RUNTIME"'); fi'
XB='xcodebuild -project Lark.xcodeproj -scheme Lark -derivedDataPath build/dd -destination "id=$udid"'

cmd="${1:-}"; shift || true
case "$cmd" in
  gen)   sync_tree; remote "$GEN"   # brings the generated Info.plist back, to be committed
         scp -q -o BatchMode=yes "$HOST:$RDIR/Sources/App/Info.plist" "$ROOT/Sources/App/Info.plist" ;;
  build) sync_tree; remote "$GEN; $SIM; mkdir -p build; $XB build 2>&1 | tee build/build.log | grep -E 'error:|warning: .*Lark/|Info.plist is stale|BUILD (SUCCEEDED|FAILED)'; exit \${pipestatus[1]}" ;;
  test)
    only=""
    if [[ -n "${1:-}" ]]; then
      # Spliced into the remote script, so only a plain test identifier is accepted.
      [[ "$1" =~ ^[A-Za-z0-9_/]+$ ]] || { echo "bad test filter: $1" >&2; exit 2; }
      only="-only-testing:$1"
    fi
    sync_tree
    remote "$GEN; $SIM; mkdir -p build; $XB $only test 2>&1 | tee build/test.log | grep -E 'error:|Info.plist is stale|Test Case .*(passed|failed)|Executed|TEST (SUCCEEDED|FAILED)'; exit \${pipestatus[1]}" ;;
  run)   sync_tree; remote "$1" ;;
  *) echo "usage: $0 gen|build|test [LarkTests/Class]|run '<cmd>'" >&2; exit 2 ;;
esac
