#!/usr/bin/env bash
# Throwaway armadietto server for integration tests.
#
#   test/armadietto.sh start   # start container, create a user, mint a *:rw token,
#                              # print RCLONE_CONFIG_* exports for the test remote
#   test/armadietto.sh stop
#
# The test remote is named TestRemoteStorage (see remotestorage_test.go).
set -euo pipefail

IMAGE=${ARMADIETTO_IMAGE:-docker.io/remotestorage/armadietto-monolithic:latest}
NAME=${ARMADIETTO_NAME:-rclone-rs-armadietto}
PORT=${ARMADIETTO_PORT:-18001}
BASE=http://127.0.0.1:$PORT
USER_=rclone${RANDOM}
PASS=rclonepass1
CLIENT=http://127.0.0.1:53682

case ${1:-} in
start)
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8000" "$IMAGE" >/dev/null
  for _ in $(seq 60); do curl -sf -o /dev/null "$BASE/" && break; sleep 0.5; done
  curl -sf -o /dev/null -X POST "$BASE/signup" \
    --data "username=$USER_&email=$USER_@example.com&password=$PASS"
  # Approve the consent form directly; the token comes back in the redirect fragment
  LOC=$(curl -s -o /dev/null -w '%{redirect_url}' -X POST "$BASE/oauth" \
    --data-urlencode "client_id=$CLIENT" --data-urlencode "redirect_uri=$CLIENT/" \
    --data "response_type=token&scope=*:rw&username=$USER_&password=$PASS&allow=Allow")
  TOKEN=$(sed -n 's/.*[#&]access_token=\([^&]*\).*/\1/p' <<<"$LOC")
  [ -n "$TOKEN" ] || { echo "failed to mint token (redirect: $LOC)" >&2; exit 1; }
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_TYPE=remotestorage"
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_STORAGE_ROOT=$BASE/storage/$USER_"
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_TOKEN=$TOKEN"
  echo "export RCLONE_CONFIG_TESTREMOTESTORAGE_VENDOR=armadietto"
  echo "export ARMADIETTO_USER=$USER_ ARMADIETTO_PASS=$PASS ARMADIETTO_BASE=$BASE"
  ;;
stop)
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  ;;
*)
  echo "usage: $0 start|stop" >&2; exit 2;;
esac
