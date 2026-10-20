#!/usr/bin/env bash
# End-to-end browser login against a throwaway armadietto container:
#   rclone config create (WebFinger, loopback listener) -> consent form
#   (POSTed with curl, as the user clicking Allow) -> redirect page loaded in
#   headless Chromium (its JS posts the token fragment back) -> remote works.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap '"$ROOT/test/armadietto.sh" stop; rm -rf "$WORK"' EXIT
CHROME=${CHROME:-$(ls -d /nix/store/*-playwright-chromium/*/chrome 2>/dev/null | head -1)}
[ -x "$CHROME" ] || { echo "no chromium found; set CHROME" >&2; exit 1; }
RCLONE=("$ROOT/rclone-remotestorage" --config "$WORK/rclone.conf")

eval "$("$ROOT/test/armadietto.sh" start)"
ADDRESS="$ARMADIETTO_USER@127.0.0.1:${ARMADIETTO_PORT:-18001}"

echo "== rclone config create (address $ADDRESS)"
# BROWSER=true: xdg-open "opens" nothing, rclone still logs the URL
BROWSER=true "${RCLONE[@]}" config create rs remotestorage \
  user_address="$ADDRESS" vendor=armadietto >"$WORK/config.out" 2>&1 &
PID=$!
for _ in $(seq 40); do grep -q 'go to the following link: ' "$WORK/config.out" && break; sleep 0.25; done
AUTH=$(sed -n 's/.*go to the following link: \(http[^ ]*\).*/\1/p' "$WORK/config.out" | head -1)
[ -n "$AUTH" ] || { cat "$WORK/config.out" >&2; exit 1; }

echo "== consent"
LOC=$(curl -s -o /dev/null -w '%{redirect_url}' -X POST "$ARMADIETTO_BASE/oauth" \
  --data "${AUTH#*\?}&username=$ARMADIETTO_USER&password=$ARMADIETTO_PASS&allow=Allow")
case $LOC in http://127.0.0.1:*/\#*access_token=*) ;; *) echo "unexpected redirect: $LOC" >&2; exit 1;; esac

echo "== browser loads redirect page"
timeout 30 "$CHROME" --headless=new --no-sandbox --disable-gpu \
  --virtual-time-budget=5000 --dump-dom "$LOC" >/dev/null 2>&1 || true
wait $PID || { cat "$WORK/config.out" >&2; exit 1; }

echo "== use the new remote"
grep -q '^token = .' "$WORK/rclone.conf"
grep -q "^storage_root = $ARMADIETTO_BASE/storage/$ARMADIETTO_USER" "$WORK/rclone.conf"
echo '{"ok":true}' | "${RCLONE[@]}" rcat rs:rclone-test/hello.json
[ "$("${RCLONE[@]}" cat rs:rclone-test/hello.json)" = '{"ok":true}' ]
"${RCLONE[@]}" lsf -R rs:
echo "PASS: browser login creates a working remote"
