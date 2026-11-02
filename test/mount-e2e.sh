#!/usr/bin/env bash
# rclone mount against a throwaway armadietto container, with curl playing a
# remoteStorage app: writes, edits that must keep an app document's
# Content-Type, renames (which need the backend's Copy), app edits showing up
# in the mount, and a read-only mount.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
M=$WORK/mnt
MPID=
cleanup() {
  if [ -n "$MPID" ]; then fusermount3 -u "$M" 2>/dev/null || true; wait "$MPID" 2>/dev/null || true; fi
  "$ROOT/test/armadietto.sh" stop
  rm -rf "$WORK"
}
trap cleanup EXIT
export RCLONE_CONFIG=$WORK/rclone.conf
eval "$("$ROOT/test/armadietto.sh" start)"
R=$RCLONE_CONFIG_TESTREMOTESTORAGE_STORAGE_ROOT
AUTH="Authorization: Bearer $RCLONE_CONFIG_TESTREMOTESTORAGE_TOKEN"
mkdir -p "$M"

fail() { echo "FAIL: $*" >&2; [ -f "$WORK/mount.log" ] && grep -E 'ERROR' "$WORK/mount.log" | tail -5 >&2; exit 1; }
app_put() { curl -sf -o /dev/null -X PUT -H "$AUTH" -H "Content-Type: $2" --data-binary "$3" "$R/$1"; }
srv() { curl -sf -H "$AUTH" "$R/$1" || true; }
ctype() { curl -sfI -H "$AUTH" "$R/$1" | tr -d '\r' | sed -n 's/^[Cc]ontent-[Tt]ype: //p'; }
# wait_for <description> <command...>: retry for up to 20s (write-back, dir cache)
wait_for() {
  local what=$1; shift
  for _ in $(seq 80); do "$@" && return; sleep 0.25; done
  fail "$what"
}
body_is() { [ "$(srv "$1")" = "$2" ]; }
gone() { [ -z "$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$R/$1" | grep -v 404)" ]; }
mnt() {
  "$ROOT/rclone-remotestorage" mount TestRemoteStorage: "$M" --dir-cache-time 2s --vfs-write-back 1s \
    -vv --log-file "$WORK/mount.log" "$@" &
  MPID=$!
  for _ in $(seq 40); do mountpoint -q "$M" && return; sleep 0.25; done
  fail "mount $*"
}
umnt() { fusermount3 -u "$M"; wait "$MPID" || true; MPID=; }

JSON='application/json; charset=UTF-8'
for mode in full writes; do
  d=$mode
  echo "== --vfs-cache-mode $mode"
  app_put "$d/note" "$JSON" '{"n":1}'
  app_put "$d/sub/a.txt" text/plain 'file a'
  mnt --vfs-cache-mode "$mode"

  echo hello >"$M/$d/new.txt"
  wait_for "new file reaches the server" body_is "$d/new.txt" hello

  printf '{"n":2}' >"$M/$d/note"
  wait_for "in-place edit reaches the server" body_is "$d/note" '{"n":2}'
  [ "$(ctype "$d/note")" = "$JSON" ] || fail "in-place edit changed Content-Type to $(ctype "$d/note")"

  printf '{"n":3}' >"$M/$d/.note.swp"
  mv "$M/$d/.note.swp" "$M/$d/note" || fail "temp file renamed over a document"
  wait_for "temp-file save reaches the server" body_is "$d/note" '{"n":3}'
  [ "$(ctype "$d/note")" = "$JSON" ] || fail "temp-file save changed Content-Type to $(ctype "$d/note")"
  wait_for "temp file removed" gone "$d/.note.swp"

  mv "$M/$d/new.txt" "$M/$d/renamed.txt" || fail "rename file"
  wait_for "renamed file on the server" body_is "$d/renamed.txt" hello
  wait_for "old name removed" gone "$d/new.txt"

  mv "$M/$d" "$M/$d-moved" || fail "rename folder"
  wait_for "renamed folder on the server" body_is "$d-moved/sub/a.txt" 'file a'
  wait_for "old folder removed" gone "$d/sub/a.txt"
  [ "$(ctype "$d-moved/note")" = "$JSON" ] || fail "folder rename changed Content-Type to $(ctype "$d-moved/note")"

  umnt
done

echo "== app edits show up in the mount (writes mode)"
app_put app/doc text/plain 'version 1'
mnt --vfs-cache-mode writes
[ "$(cat "$M/app/doc")" = 'version 1' ] || fail "read app document"
app_put app/doc text/plain 'VERSION 2' # same size: only a fresh read can tell
app_put app/added text/plain 'added by app'
wait_for "same-size app edit visible" sh -c '[ "$(cat "$0")" = "VERSION 2" ]' "$M/app/doc"
wait_for "app's new document visible" test -f "$M/app/added"
umnt

echo "== read-only mount"
mnt --read-only
if echo x 2>/dev/null >"$M/app/ro.txt"; then fail "read-only mount accepted a write"; fi
[ "$(cat "$M/app/doc")" = 'VERSION 2' ] || fail "read through read-only mount"
umnt

echo "PASS: mount writes, keeps Content-Type, renames, sees app edits"
