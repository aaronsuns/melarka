#!/bin/bash
# Keep Melarka on an iPhone signed with a free Apple ID, from a Mac with Xcode, without SideStore.
# A free Apple ID signs apps for 7 days. A launchd job runs this every 15 minutes; when the installed build has
# fewer than RENEW_DAYS left, or a newer release is out, and the Mac is unlocked, it builds the newest release tag
# with a fresh profile and installs it over the app on the phone (USB, or Wi-Fi on the same network), which keeps
# the app's data and sign-in.
# Usage: resign.sh install | uninstall | run | status      (see docs/iphone.md "Re-sign from a Mac")
#
# Config, in ~/.config/melarka/resign.env (KEY=value lines) or the environment:
#   MELARKA_DEVICE  the phone: its UDID or name, as `xcrun devicectl list devices` shows it (required)
#   MELARKA_TEAM    the Apple ID's team id (10 characters); read from the installed app's bundle id if unset
#   MELARKA_BUNDLE  bundle id, default io.github.aaronsuns.melarka.$MELARKA_TEAM (what SideStore and
#                   iloader install), so the first run upgrades a sideloaded app in place
#   RENEW_DAYS      renew once fewer days than this are left (default 4)
#   NOTIFY_CMD      optional command run on trouble with the subject as $1 and the details on stdin;
#                   a macOS notification is always shown as well
set -uo pipefail
umask 077
export DEVELOPER_DIR=${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}
export PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

LABEL=io.github.aaronsuns.melarka.resign
REPO=https://github.com/aaronsuns/melarka.git
CONFIG=$HOME/.config/melarka/resign.env
STATE="$HOME/Library/Application Support/melarka-resign"   # src/, dd/, expires, version, notified
LOGDIR=$HOME/Library/Logs/melarka-resign
PROFILES="$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"
PLIST=$HOME/Library/LaunchAgents/$LABEL.plist
SELF=$(cd "$(dirname "$0")" && pwd -P)/$(basename "$0")

# shellcheck source=/dev/null
[ -f "$CONFIG" ] && . "$CONFIG"
RENEW_DAYS=${RENEW_DAYS:-4}
WARN_HOURS=36   # Mac locked or phone unreachable this close to expiry: notify

when() { date -r "$1" '+%a %d %b %H:%M'; }
expires() { cat "$STATE/expires" 2>/dev/null || echo 0; }

install_job() {
  [ -n "${MELARKA_DEVICE:-}" ] || { echo "set MELARKA_DEVICE in $CONFIG first (xcrun devicectl list devices)" >&2; exit 2; }
  command -v xcodegen >/dev/null || { echo "xcodegen is missing: brew install xcodegen" >&2; exit 2; }
  mkdir -p "$(dirname "$PLIST")" "$LOGDIR"
  # A GUI-session agent (not a daemon, not SSH): codesign needs the unlocked login keychain.
  # StartInterval: an interval missed while the Mac slept runs once it wakes.
  cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key><array><string>/bin/bash</string><string>$SELF</string><string>run</string></array>
  <key>StartInterval</key><integer>900</integer>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>$LOGDIR/launchd.out</string>
  <key>StandardErrorPath</key><string>$LOGDIR/launchd.out</string>
</dict>
</plist>
EOF
  plutil -lint "$PLIST" >/dev/null || exit 1
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST" && echo "installed $PLIST; it runs now and every 15 minutes. Log: $LOGDIR"
}

case "${1:-}" in
  install) install_job; exit ;;
  uninstall) launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null; rm -f "$PLIST"; echo "removed $LABEL"; exit 0 ;;
  status)
    e=$(expires); v=$(cat "$STATE/version" 2>/dev/null || true)
    (( e > 0 )) && echo "installed build ${v:+($v) }expires $(when "$e")" || echo "no renewal recorded yet"
    launchctl print "gui/$(id -u)/$LABEL" 2>/dev/null | grep -E '^\s*(state|runs|last exit code) ' || echo "job not installed"
    exit 0 ;;
  run) ;;
  *) echo "usage: $0 install | uninstall | run | status" >&2; exit 2 ;;
esac

mkdir -p "$STATE" "$LOGDIR"
LOG=$LOGDIR/$(date +%F).log
exec >>"$LOG" 2>&1
log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }

LOCK=$STATE/lock
if ! mkdir "$LOCK" 2>/dev/null; then
  pid=$(cat "$LOCK/pid" 2>/dev/null || true)
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then log "another run ($pid) is still active; exiting"; exit 0; fi
  log "removing stale lock (pid ${pid:-?})"; rm -rf "$LOCK"
  mkdir "$LOCK" 2>/dev/null || { log "could not take the lock; exiting"; exit 0; }
fi
echo $$ > "$LOCK/pid"
trap 'rm -rf "$LOCK"' EXIT
find "$LOGDIR" -name '*.log' -type f -mtime +30 -delete

now=$(date +%s)
expires=$(expires)
left=$(( expires - now ))
expiry_text() { (( expires > 0 )) && when "$expires" || echo "unknown"; }

# At most once per 12 hours. $1 = subject, rest = body lines.
notify() {
  local last; last=$(cat "$STATE/notified" 2>/dev/null || echo 0)
  if (( now - last < 12 * 3600 )); then log "not notifying again yet: $1"; return; fi
  osascript -e 'on run a' -e 'display notification (item 2 of a) with title "Melarka re-sign" subtitle (item 1 of a)' \
    -e 'end run' "$1" "${2:-}" >/dev/null 2>&1 || true
  if [ -n "${NOTIFY_CMD:-}" ]; then
    { printf '%s\n' "${@:2}"; echo; echo "Log: $LOG"; echo; tail -n 25 "$LOG"; } |
      /bin/bash -c "$NOTIFY_CMD \"\$1\"" notify "Melarka re-sign: $1" || log "NOTIFY_CMD failed"
  fi
  echo "$now" > "$STATE/notified"; log "notified: $1"
}

fail() {
  log "FAILED: $1"
  notify "$1" "Renewing Melarka failed: $1. The app on the phone expires $(expiry_text)." \
    "If the log mentions accounts or credentials, sign in again in Xcode > Settings > Accounts."
  exit 1
}

[ -n "${MELARKA_DEVICE:-}" ] || fail "MELARKA_DEVICE is not set (in $CONFIG)"

# The newest release tag. Offline, the tags fetched before do.
src=$STATE/src
if [ ! -d "$src/.git" ]; then git clone --quiet "$REPO" "$src" || fail "git clone failed"; fi
git -C "$src" fetch --quiet --tags --force origin || log "git fetch failed; using the tags fetched before"
tag=$(git -C "$src" tag -l 'v[0-9]*' --sort=-v:refname | head -1)
[ -n "$tag" ] || fail "no release tag found"
version=${tag#v}
installed=$(cat "$STATE/version" 2>/dev/null || true)

# Due when the signature runs low, or when a newer release than the one on the phone is out.
if (( left > RENEW_DAYS * 86400 )) && [ "$version" = "$installed" ]; then
  log "not due; $installed expires $(when "$expires")"; exit 0
fi
if [ "$version" != "$installed" ]; then log "due: $version is out (the phone has ${installed:-an unknown version})"
else log "due (app expires $(expiry_text))"; fi

# While the screen is locked, Xcode can't read the Apple ID from the keychain to make a profile ("missing
# Xcode-Username"), so wait for an unlocked moment: a run every 15 minutes catches one soon after.
# (A string match, not `ioreg | grep -q`: grep quitting early fails ioreg with SIGPIPE, and pipefail makes that false.)
if [[ $(ioreg -n Root -d1) == *'"IOConsoleLocked" = Yes'* ]]; then
  log "Mac is locked; renewing once it is unlocked"
  if (( left < WARN_HOURS * 3600 )); then
    notify "Mac locked" "Melarka on the iPhone expires $(expiry_text) and renewing it needs the Mac unlocked for a moment." \
      "Unlock the Mac (with the phone on the same Wi-Fi, or plugged in); it renews within 15 minutes."
  fi
  exit 0
fi

# Xcode lists its signed-in Apple IDs in its defaults. With none (signed out, or the account was dropped),
# the build would only fail with "No Accounts", so say what to do instead.
if [[ $(defaults read com.apple.dt.Xcode DVTDeveloperAccountManagerAppleIDLists 2>/dev/null) != *identifier* ]]; then
  log "FAILED: Xcode is not signed in to an Apple ID"
  notify "sign in to Xcode" "Melarka on the iPhone expires $(expiry_text) and Xcode on this Mac is not signed in to an Apple ID." \
    "Open Xcode > Settings > Accounts and add the Apple ID that signs Melarka; it renews within 15 minutes."
  exit 1
fi

if ! xcrun devicectl device info details --device "$MELARKA_DEVICE" --timeout 60 >/dev/null 2>&1; then
  log "phone not reachable"
  if (( left < WARN_HOURS * 3600 )); then
    notify "phone not reachable" "Melarka on the iPhone expires $(expiry_text) and the Mac could not reach the phone." \
      "Put the phone on the same Wi-Fi as the Mac, or plug it in, while the Mac is awake; the next try is within 15 minutes."
  fi
  exit 0
fi

if [ -z "${MELARKA_TEAM:-}" ] && [ -z "${MELARKA_BUNDLE:-}" ]; then
  apps=$(mktemp)
  if ! xcrun devicectl device info apps --device "$MELARKA_DEVICE" --timeout 120 --json-output "$apps" >/dev/null 2>&1; then
    rm -f "$apps"; log "could not list the phone's apps; trying again next run"; exit 0
  fi
  MELARKA_BUNDLE=$(grep -oE '"bundleIdentifier" *: *"io\.github\.aaronsuns\.melarka\.[A-Z0-9]{10}"' "$apps" |
    grep -oE 'io\.github[^"]*' | head -1)
  rm -f "$apps"
  [ -n "$MELARKA_BUNDLE" ] || fail "no sideloaded Melarka on the phone to read the team id from; set MELARKA_TEAM"
  MELARKA_TEAM=${MELARKA_BUNDLE##*.}
fi
[ -n "${MELARKA_TEAM:-}" ] || fail "MELARKA_TEAM is not set"
BUNDLE=${MELARKA_BUNDLE:-io.github.aaronsuns.melarka.$MELARKA_TEAM}

# The newest release tag, built the way the release workflow builds it, but signed for this phone.
{ git -C "$src" checkout --quiet --force --detach "$tag" && git -C "$src" clean -qfdx; } || fail "checkout $tag failed"
(cd "$src/ios" && xcodegen generate --quiet) || fail "xcodegen failed"

# A free team's profile lasts 7 days from when it was made; removing ours makes Xcode create a new one.
for f in "$PROFILES"/*.mobileprovision; do
  [ -f "$f" ] || continue
  id=$(security cms -D -i "$f" 2>/dev/null | plutil -extract Entitlements.application-identifier raw -o - - 2>/dev/null)
  [ "$id" = "$MELARKA_TEAM.$BUNDLE" ] && rm -f "$f" && log "removed old profile $(basename "$f")"
done

log "building $tag as $BUNDLE"
xcodebuild -project "$src/ios/Lark.xcodeproj" -scheme Lark -configuration Release -destination generic/platform=iOS \
  -derivedDataPath "$STATE/dd" -allowProvisioningUpdates -quiet build \
  DEVELOPMENT_TEAM="$MELARKA_TEAM" PRODUCT_BUNDLE_IDENTIFIER="$BUNDLE" CODE_SIGN_STYLE=Automatic \
  MARKETING_VERSION="$version" CURRENT_PROJECT_VERSION="$version" || fail "build of $tag failed"
app=$STATE/dd/Build/Products/Release-iphoneos/Lark.app

iso=$(security cms -D -i "$app/embedded.mobileprovision" | plutil -extract ExpirationDate raw -o - -) || fail "no profile in the build"
newexp=$(date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$iso" +%s) || fail "bad profile date $iso"

xcrun devicectl device install app --device "$MELARKA_DEVICE" "$app" || fail "install on the phone failed"
echo "$newexp" > "$STATE/expires"
echo "$version" > "$STATE/version"
rm -f "$STATE/notified"
log "installed Melarka $version; expires $(when "$newexp")"
