#!/usr/bin/env bash
# rclone test info against the self-starting servers, with the backend's
# encoding off, to see which names each server stores as-is. Results (JSON and
# logs) go to $OUTPUT.
#
#   test/test-info.sh [remote names...]   default: TestRemoteStorage TestRemoteStorageRSilo
#
# --check-base32768 is left out: both servers reject its long non-ASCII names
# (docs/upstream-issues.md), and the failures slow the run to over 30 minutes.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUTPUT=${OUTPUT:-$(mktemp -d)}
export RCLONE_CONFIG=$OUTPUT/rclone.conf
for name in "${@:-TestRemoteStorage TestRemoteStorageRSilo}"; do
  for n in $name; do
    s=$ROOT/fstest/testserver/init.d/$n
    up=$(tr a-z A-Z <<<"$n")
    while IFS='=' read -r k v; do
      case $k in _*|"") ;; *) export "RCLONE_CONFIG_${up}_$(tr a-z A-Z <<<"$k")=$v" ;; esac
    done < <("$s" start | grep -E '^[a-z_]+=')
    export "RCLONE_CONFIG_${up}_ENCODING=None"
    echo "== $n"
    "$ROOT/rclone-remotestorage" test info --check-control --check-length --check-normalization --check-streaming \
      --low-level-retries 1 --retries 1 --write-json "$OUTPUT/$n.json" "$n:rclone-info" >"$OUTPUT/$n.log" 2>&1 || true
    "$s" stop >/dev/null
    jq -c 'del(.ControlCharacters)' "$OUTPUT/$n.json"
    jq -r '[.ControlCharacters | to_entries[] | select(.value.InList.middle != "present" or .value.WriteError != {} or .value.GetError != {}) | .key | explode[0]]
      | "not stored as-is: \(length) characters (code points \(map(tostring) | join(",")))"' "$OUTPUT/$n.json"
  done
done
echo "results in $OUTPUT"
