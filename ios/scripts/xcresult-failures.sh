#!/usr/bin/env bash
# Prints each failed test of an .xcresult bundle with its failure message, and the pass/fail counts:
# `xcodebuild -quiet` names a failing test but not why it failed.
set -euo pipefail
xcrun xcresulttool get test-results summary --path "$1" | python3 -c '
import json, sys
s = json.load(sys.stdin)
print("passed: %s  failed: %s  skipped: %s" % (s.get("passedTests"), s.get("failedTests"), s.get("skippedTests")))
for f in s.get("testFailures", []):
    print("FAILED %s: %s" % (f.get("testIdentifierString") or f.get("testName"), f.get("failureText", "").strip()))
'
