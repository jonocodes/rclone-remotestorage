#!/usr/bin/env bash
# Export the backend into an rclone checkout the way the upstream PR adds it:
# the backend package (minus *_outoftree.go), its test servers, its docs, and
# one-line registrations in rclone's shared lists. Safe to re-run: owned files
# are overwritten and registrations are only added if missing.
#
#   test/upstream-sync.sh <rclone-checkout>
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SRC=${1:?usage: $0 <rclone-checkout>}
SRC=$(cd "$SRC" && pwd)
[ -f "$SRC/backend/all/all.go" ] || { echo "$SRC is not an rclone checkout" >&2; exit 1; }

# Files the backend owns
rm -rf "$SRC/backend/remotestorage"
cp -r "$ROOT/backend/remotestorage" "$SRC/backend/remotestorage"
rm -f "$SRC"/backend/remotestorage/*_outoftree.go
grep -rl 'github.com/jonocodes/rclone-remotestorage/' "$SRC/backend/remotestorage" |
  xargs sed -i 's#github.com/jonocodes/rclone-remotestorage/#github.com/rclone/rclone/#g'
cp "$ROOT"/fstest/testserver/init.d/TestRemoteStorage* "$SRC/fstest/testserver/init.d/"
for f in run.bash docker.bash; do
  cmp -s "$ROOT/fstest/testserver/init.d/$f" "$SRC/fstest/testserver/init.d/$f" ||
    echo "warning: $f differs from the checkout's; refresh the copy in fstest/testserver/init.d" >&2
done
cp "$ROOT/docs/rclone/remotestorage.md" "$SRC/docs/content/remotestorage.md"
if [ -f "$ROOT/docs/rclone/remotestorage.yaml" ]; then
  cp "$ROOT/docs/rclone/remotestorage.yaml" "$SRC/docs/data/backends/remotestorage.yaml"
fi

# add <file> <before|after-last> <anchor> <line>: insert line before the first
# or after the last line matching the anchor regex, unless it's already there
add() {
  local file=$SRC/$1 where=$2 anchor=$3 line=$4
  grep -qxF -- "$line" "$file" && return
  grep -qE -- "$anchor" "$file" || { echo "anchor /$anchor/ not found in $1" >&2; exit 1; }
  # Pass strings through the environment: awk -v would interpret backslashes
  RE=$anchor LINE=$line WHERE=$where awk '
    NR == FNR { if ($0 ~ ENVIRON["RE"]) { last = FNR; if (!first) first = FNR }; next }
    ENVIRON["WHERE"] == "before" && FNR == first { print ENVIRON["LINE"] }
    { print }
    ENVIRON["WHERE"] == "after-last" && FNR == last { print ENVIRON["LINE"] }
    ' "$file" "$file" >"$file.tmp"
  # Overwrite in place to keep the file mode
  cat "$file.tmp" >"$file" && rm "$file.tmp"
}

add backend/all/all.go before '_ "github.com/rclone/rclone/backend/s3"$' \
  $'\t_ "github.com/rclone/rclone/backend/remotestorage"'
add fstest/testserver/init.d/PORTS.md after-last '^[|] 286[0-9][0-9] [|]' '| 28642 | TestRemoteStorage |'
add fstest/testserver/init.d/PORTS.md after-last '^[|] 286[0-9][0-9] [|]' '| 28643 | TestRemoteStorageRSilo |'
add README.md before '^- rsync\.net ' \
  '- remoteStorage [:page_facing_up:](https://rclone.org/remotestorage/)'
add docs/content/docs.md before '^- [[]rsync[.]net[]]' \
  '- [remoteStorage](/remotestorage/)'
add docs/content/_index.md before '^[{][{]< provider name="rsync[.]net"' \
  '{{< provider name="remoteStorage" home="https://remotestorage.io/" config="/remotestorage/" >}}'
add docs/layouts/chrome/navbar.html before 'dropdown-letter-heading">S &ndash; Z' \
  '          <a class="dropdown-item" href="/remotestorage/">remoteStorage</a>'
add bin/make_manual.py before '^    "seafile\.md",$' \
  '    "remotestorage.md",'

# gitannex only accepts remotes from a config file, not env vars, so every
# self-starting test server skips it (as webdav's Nextcloud/ownCloud do)
if ! grep -q 'remote:   "TestRemoteStorage:"' "$SRC/fstest/test_all/config.yaml"; then
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
fi

echo "synced into $SRC"
