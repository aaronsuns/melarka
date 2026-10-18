#!/usr/bin/env bash
# The emulator smoke test: the app's instrumented tests (app/src/androidTest) on an Android emulator, against a
# throwaway Melarka server with the made-up demo library (web/e2e/start-server.sh, LARK_DEMO=1) on this machine's
# 127.0.0.1:4700, which the emulator reaches as 10.0.2.2:4700 (plain http there is allowed in debug builds only).
#
# Usage: android/scripts/emulator-smoke.sh [extra gradle args, e.g. -Pandroid.testInstrumentationRunnerArguments.class=lark.SmokeFlowTest]
# Needs Go, Node (only when web/dist is missing), Python 3, ffmpeg, a JDK 17 and the Android SDK (ANDROID_HOME).
# Boots the AVD $LARK_AVD (default lark_api35) headless unless a device is attached already (CI: the emulator runner
# boots one), and shuts it down again if it booted it. The server's admin password is made up for this run (a test
# value, like the e2e tests'), handed to the tests as an instrumentation argument.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
: "${ANDROID_HOME:?set ANDROID_HOME to the Android SDK}"
ADB="$ANDROID_HOME/platform-tools/adb"
AVD="${LARK_AVD:-lark_api35}"
PORT=4700
LOGS="$ROOT/android/app/build/smoke"
mkdir -p "$LOGS"
LARK_E2E_PW="smoke-$(python3 -c 'import secrets; print(secrets.token_hex(12))')"
export LARK_E2E_PW

server_pid="" emulator_pid=""
# shellcheck disable=SC2329  # the EXIT trap runs it
cleanup() {
  if [[ -n "$server_pid" ]]; then
    pkill -TERM -P "$server_pid" 2>/dev/null || true    # the server and the feed server (children of start-server.sh)
    kill "$server_pid" 2>/dev/null || true
  fi
  if [[ -n "$emulator_pid" ]]; then
    "$ADB" emu kill >/dev/null 2>&1 || true
    wait "$emulator_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

if curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/v1/info" 2>/dev/null; then
  echo "something already answers on 127.0.0.1:$PORT; stop it first" >&2
  exit 1
fi

# The web app the server serves (LARK_WEB_BUILD=1: built afresh even when there is one).
if [[ "${LARK_WEB_BUILD:-}" == 1 || ! -f "$ROOT/web/dist/index.html" ]]; then
  (cd "$ROOT/web" && npm ci --no-audit --no-fund && npm run build)
fi

echo "== server (demo library) on 127.0.0.1:$PORT"
LARK_DEMO=1 bash "$ROOT/web/e2e/start-server.sh" >"$LOGS/server.log" 2>&1 &
server_pid=$!

if [[ "$("$ADB" devices | awk 'NR > 1 && $2 == "device"' | wc -l)" -eq 0 ]]; then
  echo "== booting $AVD (headless)"
  "$ANDROID_HOME/emulator/emulator" -avd "$AVD" -no-window -no-snapshot -no-audio -no-boot-anim -gpu swiftshader_indirect \
    >"$LOGS/emulator.log" 2>&1 &
  emulator_pid=$!
fi
"$ADB" wait-for-device
for _ in $(seq 1 180); do
  [[ "$("$ADB" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" == 1 ]] && break
  sleep 2
done
[[ "$("$ADB" shell getprop sys.boot_completed | tr -d '\r')" == 1 ]] || { echo "the emulator did not boot" >&2; exit 1; }
# No animations (Espresso), the network on (a previous run may have been stopped while it was off).
for s in window_animation_scale transition_animation_scale animator_duration_scale; do "$ADB" shell settings put global "$s" 0; done
"$ADB" shell svc wifi enable || true
"$ADB" shell svc data enable || true

echo "== waiting for the server"
for _ in $(seq 1 180); do
  curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/v1/info" 2>/dev/null && break
  kill -0 "$server_pid" 2>/dev/null || { echo "the server exited:" >&2; tail -50 "$LOGS/server.log" >&2; exit 1; }
  sleep 1
done
curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/v1/info" || { tail -50 "$LOGS/server.log" >&2; exit 1; }

echo "== instrumented tests"
cd "$ROOT/android"
status=0
./gradlew --console=plain connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.larkServer="http://10.0.2.2:$PORT" \
  -Pandroid.testInstrumentationRunnerArguments.larkPassword="$LARK_E2E_PW" "$@" || status=$?
"$ADB" logcat -d >"$LOGS/logcat.txt" 2>&1 || true
"$ADB" pull /sdcard/googletest/test_outputfiles "$LOGS/failures" >/dev/null 2>&1 || true   # a failure's screenshot and page
"$ADB" shell svc wifi enable || true
"$ADB" shell svc data enable || true
exit "$status"
