#!/usr/bin/env bash
# Rehearse upstreaming: copy the backend into a checkout of rclone at the
# version pinned in go.mod and run rclone's full integration matrix
# (go run ./fstest/test_all) against the self-starting armadietto and RSilo
# servers. A clean run of this is what rclone asks for in a new-backend PR.
#
#   test/test-all.sh [test_all flags...]   e.g. -tests fs/sync -verbose
#
#   RCLONE_SRC  where to keep the rclone checkout
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
# Start from a clean tree each time, then add the backend as a PR would
git -C "$SRC" checkout -q -- .
git -C "$SRC" clean -qfd -- backend fstest
cp -r "$ROOT/backend/remotestorage" "$SRC/backend/remotestorage"
grep -rl 'github.com/jonocodes/rclone-remotestorage/' "$SRC/backend/remotestorage" |
  xargs sed -i 's#github.com/jonocodes/rclone-remotestorage/#github.com/rclone/rclone/#g'
sed -i 's#^\t_ "github.com/rclone/rclone/backend/s3"$#\t_ "github.com/rclone/rclone/backend/remotestorage"\n&#' \
  "$SRC/backend/all/all.go"
grep -q 'backend/remotestorage"' "$SRC/backend/all/all.go" || { echo "failed to register backend in all.go" >&2; exit 1; }
cp "$ROOT"/fstest/testserver/init.d/TestRemoteStorage* "$SRC/fstest/testserver/init.d/"
for f in run.bash docker.bash; do
  cmp -s "$ROOT/fstest/testserver/init.d/$f" "$SRC/fstest/testserver/init.d/$f" ||
    echo "warning: $f differs from rclone $VERSION's; refresh the copy in fstest/testserver/init.d" >&2
done
# gitannex only accepts remotes from a config file, not env vars, so every
# self-starting test server skips it (as webdav's Nextcloud/ownCloud do)
cat >>"$SRC/fstest/test_all/config.yaml" <<'EOF'
 - backend:  "remotestorage"
   remote:   "TestRemoteStorage:"
   fastlist: false
   ignoretests:
     - cmd/gitannex
 - backend:  "remotestorage"
   remote:   "TestRemoteStorageRSilo:"
   fastlist: false
   ignoretests:
     - cmd/gitannex
EOF

echo "== rclone $VERSION in $SRC, logs in $OUTPUT"
cd "$SRC"
go vet ./backend/remotestorage/
go run ./fstest/test_all -backends remotestorage -output "$OUTPUT" "$@"
