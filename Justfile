# List recipes
default:
    @just --list

# Build rclone with the remotestorage backend compiled in
build:
    go build -o rclone-remotestorage ./cmd/rclone

# Fast unit tests (no network, no docker)
test:
    go test ./backend/remotestorage/ -run 'Test[^I]'

# rclone's fstests integration suite against a throwaway armadietto container
test-integration *args:
    go test ./backend/remotestorage/ -run TestIntegration -timeout 30m {{args}}

# Start armadietto and print RCLONE_CONFIG_* exports: eval "$(just server)"
server:
    @./test/armadietto.sh start

# Stop the armadietto test container
server-stop:
    ./test/armadietto.sh stop

# Spike: loopback implicit-grant token capture, end to end against armadietto (docker)
spike-loopback:
    ./spike/loopback/armadietto.sh

# Spike against a real server: prints an auth URL to open in your browser
spike-loopback-live address:
    go run ./spike/loopback -address {{address}}
