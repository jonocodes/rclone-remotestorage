# rclone config for this project, never ~/.config/rclone/rclone.conf. flox
# activation sets the same path; tests use a fresh throwaway config instead.
export RCLONE_CONFIG := env("RCLONE_CONFIG", justfile_directory() / "rclone.conf")

# List recipes
default:
    @just --list

# Build rclone with the remotestorage backend compiled in
build:
    go build -o rclone-remotestorage ./cmd/rclone

# Fast unit tests (no network, no docker)
test:
    RCLONE_CONFIG=$(mktemp -d)/rclone.conf go test ./backend/remotestorage/ -run 'Test[^I]'

# rclone's fstests integration suite against a throwaway armadietto container
test-integration *args:
    RCLONE_CONFIG=$(mktemp -d)/rclone.conf go test ./backend/remotestorage/ -run TestIntegration -timeout 30m {{args}}

# fstests against a remote in ./rclone.conf, e.g. just test-remote rs5:rclone-test
# (works in a random subfolder of the path and removes it afterwards)
test-remote remote *args:
    go test ./backend/remotestorage/ -run TestIntegration -remote {{remote}} -timeout 30m {{args}}

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
