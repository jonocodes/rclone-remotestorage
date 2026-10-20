#!/usr/bin/env bash
# Repeatable end-to-end run of the loopback-implicit-grant spike against a
# throwaway armadietto container. The user's two browser actions are scripted:
# the consent form is POSTed with curl, and the redirect page (which must run
# JS to read the fragment) is loaded in headless Chromium.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
IMAGE=${ARMADIETTO_IMAGE:-docker.io/remotestorage/armadietto-monolithic:latest}
NAME=rs-spike-armadietto
HOSTPORT=${ARMADIETTO_PORT:-18000}
BASE=http://127.0.0.1:$HOSTPORT
USER_=spike$RANDOM
PASS=spikepass1
WORK=$(mktemp -d)
trap 'docker rm -f $NAME >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT

CHROME=${CHROME:-$(ls -d /nix/store/*-playwright-chromium/*/chrome 2>/dev/null | head -1)}
[ -x "$CHROME" ] || { echo "no chromium found; set CHROME" >&2; exit 1; }

echo "== build"
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/loopback" ./spike/loopback)

echo "== start $IMAGE on $BASE"
docker rm -f $NAME >/dev/null 2>&1 || true
docker run -d --name $NAME -p 127.0.0.1:$HOSTPORT:8000 "$IMAGE" >/dev/null
for _ in $(seq 60); do curl -sf -o /dev/null $BASE/ && break; sleep 0.5; done

echo "== signup $USER_"
curl -sf -o /dev/null -X POST $BASE/signup \
  --data "username=$USER_&email=$USER_@example.com&password=$PASS"

echo "== run spike"
"$WORK/loopback" -address "$USER_@127.0.0.1:$HOSTPORT" -insecure-http -timeout 60s \
  >"$WORK/out" 2>"$WORK/err" &
PID=$!
for _ in $(seq 40); do grep -q '^AUTH_URL ' "$WORK/out" 2>/dev/null && break; sleep 0.25; done
AUTH=$(sed -n 's/^AUTH_URL //p' "$WORK/out")
[ -n "$AUTH" ] || { cat "$WORK/err" >&2; exit 1; }

echo "== consent (as the user would click Allow)"
LOC=$(curl -s -o /dev/null -w '%{redirect_url}' -X POST $BASE/oauth \
  --data "${AUTH#*\?}&username=$USER_&password=$PASS&allow=Allow")
case $LOC in http://127.0.0.1:53682/\#*access_token=*) ;; *) echo "unexpected redirect: $LOC" >&2; exit 1;; esac

echo "== browser loads redirect page"
timeout 30 "$CHROME" --headless=new --no-sandbox --disable-gpu \
  --virtual-time-budget=5000 --dump-dom "$LOC" >/dev/null 2>&1 || true

if wait $PID; then
  sed -n 's/^RESULT //p' "$WORK/out"
  echo "PASS: loopback implicit grant works against armadietto"
else
  cat "$WORK/err" >&2
  echo "FAIL" >&2
  exit 1
fi
