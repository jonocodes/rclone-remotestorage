# rclone config for this project, never ~/.config/rclone/rclone.conf. Tests use
# a fresh throwaway config instead.
export RCLONE_CONFIG := env("RCLONE_CONFIG", justfile_directory() / "rclone.conf")
export CGO_ENABLED := env("CGO_ENABLED", "0")

# List recipes
default:
    @just --list

# Build rclone with the remotestorage backend compiled in
build:
    go build -o rclone-remotestorage ./cmd/rclone

# Fast unit tests (no network, no docker)
test:
    RCLONE_CONFIG=$(mktemp -d)/rclone.conf go test ./backend/remotestorage/ -run 'Test[^I]'

# rclone's fstests suite against throwaway armadietto (docker) and RSilo (offline) servers
test-integration *args:
    RCLONE_CONFIG=$(mktemp -d)/rclone.conf go test ./backend/remotestorage/ -run TestIntegration -timeout 30m {{args}}

# fstests against a remote in ./rclone.conf, e.g. just test-remote rs5:rclone-test
# (works in a random subfolder of the path and removes it afterwards). Fails
# if the remote isn't configured, which fstests would otherwise skip as a pass.
test-remote remote *args:
    #!/usr/bin/env bash
    set -uo pipefail
    log=$(mktemp)
    go test ./backend/remotestorage/ -run TestIntegration -remote {{remote}} -timeout 30m -v {{args}} >"$log" 2>&1
    rc=$?
    if grep -q "Didn't find .* in config file" "$log"; then
        echo "FAIL: remote {{remote}} isn't configured in ${RCLONE_CONFIG}; nothing was tested" >&2
        exit 1
    fi
    grep -E -- '^\s*--- FAIL' "$log" || true
    echo "passed=$(grep -cE -- '^\s*--- PASS: TestIntegration/' "$log") failed=$(grep -cE -- '^\s*--- FAIL: TestIntegration/' "$log") skipped=$(grep -cE -- '^\s*--- SKIP: TestIntegration/' "$log")  (log: $log)"
    tail -1 "$log"
    exit $rc

# Start armadietto and print RCLONE_CONFIG_* exports: eval "$(just server)"
server:
    @./test/armadietto.sh start

# Stop the armadietto test container
server-stop:
    ./test/armadietto.sh stop

# End-to-end browser login (rclone config create) against armadietto
test-login: build
    ./test/login-e2e.sh

# Spike: loopback implicit-grant token capture, end to end against armadietto (docker)
spike-loopback:
    ./spike/loopback/armadietto.sh

# Spike against a real server: prints an auth URL to open in your browser
spike-loopback-live address:
    go run ./spike/loopback -address {{address}}

# State of the upstream tickets listed under "Waiting on upstream" in the README
upstream-status:
    @grep -oE '\[[a-z0-9_.-]+/[a-zA-Z0-9_.-]+#[0-9]+\]' README.md | tr -d '[]' | sort -u | while IFS='#' read -r repo n; do \
      gh api "repos/$repo/issues/$n" --jq "\"$repo#$n  \(.state)  comments=\(.comments)  updated=\(.updated_at[:10])  \(.title)\""; \
    done

# rclone mount against armadietto: writes, Content-Type on edits, renames, app edits (needs FUSE)
test-mount: build
    ./test/mount-e2e.sh

# rclone's full integration matrix (fstest/test_all) with the backend copied into an rclone checkout
test-all *args:
    ./test/test-all.sh {{args}}

# rclone test info (which names each server stores as-is) against armadietto and RSilo
test-info *remotes: build
    ./test/test-info.sh {{remotes}}
