#!/usr/bin/env bash
# End-to-end browser login against a throwaway server container:
#   rclone config create (WebFinger, loopback listener) -> consent form
#   (POSTed with curl, as the user clicking Allow) -> redirect page loaded in
#   headless Chromium (its JS posts the token fragment back) -> remote works.
#
#   test/login-e2e.sh [armadietto|rsilo]   (default armadietto)
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SERVER=${1:-armadietto}
case $SERVER in armadietto|rsilo) ;; *) echo "unknown server $SERVER" >&2; exit 2;; esac
WORK=$(mktemp -d)
trap '"$ROOT/test/$SERVER.sh" stop; rm -rf "$WORK"' EXIT
CHROME=${CHROME:-$(ls -d /nix/store/*-playwright-chromium/*/chrome 2>/dev/null | head -1)}
[ -x "$CHROME" ] || { echo "no chromium found; set CHROME" >&2; exit 1; }
RCLONE=("$ROOT/rclone-remotestorage" --config "$WORK/rclone.conf")

eval "$("$ROOT/test/$SERVER.sh" start)"
if [ "$SERVER" = armadietto ]; then
  BASE=$ARMADIETTO_BASE USER_=$ARMADIETTO_USER
  ADDRESS="$USER_@127.0.0.1:${ARMADIETTO_PORT:-18011}"
  EXTRA=(vendor=armadietto)
else
  BASE=$RSILO_BASE USER_=$RSILO_USER
  ADDRESS="$USER_@${BASE#http://}"
  EXTRA=()
fi

echo "== $SERVER: rclone config create (address $ADDRESS)"
# BROWSER=true: xdg-open "opens" nothing, rclone still logs the URL
BROWSER=true "${RCLONE[@]}" config create rs remotestorage \
  user_address="$ADDRESS" "${EXTRA[@]}" >"$WORK/config.out" 2>&1 &
PID=$!
for _ in $(seq 40); do grep -q 'go to the following link: ' "$WORK/config.out" && break; sleep 0.25; done
AUTH=$(sed -n 's/.*go to the following link: \(http[^ ]*\).*/\1/p' "$WORK/config.out" | head -1)
[ -n "$AUTH" ] || { cat "$WORK/config.out" >&2; exit 1; }

echo "== consent"
if [ "$SERVER" = armadietto ]; then
  LOC=$(curl -s -o /dev/null -w '%{redirect_url}' -X POST "$BASE/oauth" \
    --data "${AUTH#*\?}&username=$USER_&password=$ARMADIETTO_PASS&allow=Allow")
else
  # The consent form's hidden fields are the authorize request's parameters.
  # RSilo signs localhost requests in as the owner and wants same-origin POSTs.
  LOC=$(curl -s -o /dev/null -w '%{redirect_url}' -X POST -H "Origin: $BASE" \
    "${AUTH%%\?*}" --data "${AUTH#*\?}&action=approve")
fi
case $LOC in http://127.0.0.1:*/\#*access_token=*) ;; *) echo "unexpected redirect: $LOC" >&2; exit 1;; esac

echo "== browser loads redirect page"
timeout 30 "$CHROME" --headless=new --no-sandbox --disable-gpu \
  --virtual-time-budget=5000 --dump-dom "$LOC" >/dev/null 2>&1 || true
wait $PID || { cat "$WORK/config.out" >&2; exit 1; }

echo "== use the new remote"
grep -q '^token = .' "$WORK/rclone.conf"
grep -q "^storage_root = $BASE/storage/$USER_" "$WORK/rclone.conf"
echo '{"ok":true}' | "${RCLONE[@]}" rcat rs:rclone-test/hello.json
[ "$("${RCLONE[@]}" cat rs:rclone-test/hello.json)" = '{"ok":true}' ]
"${RCLONE[@]}" lsf -R rs:
echo "PASS: $SERVER: browser login creates a working remote"
