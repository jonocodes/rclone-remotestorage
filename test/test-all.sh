#!/usr/bin/env bash
# Rehearse upstreaming: export the backend (test/upstream-sync.sh) into a
# clean checkout of rclone at the version pinned in go.mod and run rclone's
# full integration matrix (go run ./fstest/test_all) against the
# self-starting armadietto and RSilo servers. A clean run of this is what
# rclone asks for in a new-backend PR. `just upstream-test-all` does the same
# against the fork at rclone master.
#
#   test/test-all.sh [test_all flags...]   e.g. -tests fs/sync -verbose
#
#   RCLONE_SRC  where to keep the rclone checkout; it is reset on every run
#               (default: ~/.cache/rclone-remotestorage/rclone-<version>)
#   OUTPUT      where test_all writes its logs and report (default: a temp dir)
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION=$(cd "$ROOT" && go list -m -f '{{.Version}}' github.com/rclone/rclone)
SRC=${RCLONE_SRC:-${XDG_CACHE_HOME:-$HOME/.cache}/rclone-remotestorage/rclone-$VERSION}
OUTPUT=${OUTPUT:-$(mktemp -d)}

if [ ! -d "$SRC/.git" ]; then
  git clone -q --depth 1 --branch "$VERSION" https://github.com/rclone/rclone "$SRC"
fi
# Start from a clean tree each time, then add the backend as the PR does
git -C "$SRC" checkout -q -- .
git -C "$SRC" clean -qfd
"$ROOT/test/upstream-sync.sh" "$SRC"

echo "== rclone $VERSION in $SRC, logs in $OUTPUT"
cd "$SRC"
go vet ./backend/remotestorage/
go run ./fstest/test_all -backends remotestorage -output "$OUTPUT" "$@"
