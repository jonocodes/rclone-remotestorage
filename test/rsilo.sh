#!/usr/bin/env bash
# Throwaway RSilo server for the login and mount tests, like armadietto.sh.
#
#   test/rsilo.sh start   # start container, mint a *:rw token, print
#                         # RCLONE_CONFIG_* exports for the test remote
#   test/rsilo.sh stop
#
# It runs on the host network listening on 127.0.0.1, which keeps RSilo's dev
# identity on: requests to localhost are signed in as the account owner, so
# the OAuth consent form can be approved without a login (Linux only). The
# fstests server (fstest/testserver/init.d/TestRemoteStorageRSilo) doesn't
# need that and publishes a port instead.
set -euo pipefail

IMAGE=${RSILO_IMAGE:-ghcr.io/jonocodes/rsilo:0.3.0}
NAME=${RSILO_NAME:-rclone-rs-rsilo-dev}
PORT=${RSILO_PORT:-18012}
BASE=http://localhost:$PORT
USER_=alice # the offline server's default account

case ${1:-} in
start)
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker run -d --name "$NAME" --network host -e LISTEN_HOST=127.0.0.1 -e PORT="$PORT" "$IMAGE" >/dev/null
  for _ in $(seq 60); do curl -sf -o /dev/null "$BASE/health" && break; sleep 0.5; done
  TOKEN=$(docker exec "$NAME" bun src/scripts/dev-token.ts "$USER_" '*:rw' </dev/null 2>/dev/null | tail -1)
  [ -n "$TOKEN" ] || { echo "failed to mint token" >&2; docker logs "$NAME" >&2; exit 1; }
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_TYPE=remotestorage"
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_STORAGE_ROOT=$BASE/storage/$USER_"
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_TOKEN=$TOKEN"
  echo "export RSILO_USER=$USER_ RSILO_BASE=$BASE"
  ;;
stop)
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  ;;
*)
  echo "usage: $0 start|stop" >&2; exit 2;;
esac
