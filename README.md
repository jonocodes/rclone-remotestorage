# rclone-remotestorage

A generic [rclone](https://rclone.org) backend for the [remoteStorage](https://remotestorage.io) protocol ([draft-dejong-remotestorage](https://datatracker.ietf.org/doc/draft-dejong-remotestorage/)). It should work with any compliant server: 5apps, armadietto, php-remote-storage, mysteryshack, RSilo, and others. The end goal is `rclone mount` of a remoteStorage account as a local drive.

Status: early. Read, write, list, delete and `rclone mount` work with a pasted token. rclone's fstests integration suite passes against armadietto. Login from `rclone config` (WebFinger plus a browser) is not built yet.

## How it is built

This is an out-of-tree backend. `backend/remotestorage` is an ordinary rclone backend package, and `cmd/rclone` builds stock rclone (all backends and commands, pinned in `go.mod`) with it added. No fork of rclone is needed. Upstreaming later means copying `backend/remotestorage/` into rclone.

## Usage

```sh
just build
./rclone-remotestorage config create myrs remotestorage \
    storage_root=https://example.com/storage/alice token=<bearer token>
./rclone-remotestorage lsf myrs:
./rclone-remotestorage copy ./notes myrs:documents/notes
```

- `storage_root` is the `href` of the remoteStorage link in the server's WebFinger response (`https://<host>/.well-known/webfinger?resource=acct:<user>@<host>`).
- `token` is an OAuth bearer token. Its scope (`*:rw`, `documents:rw`, …) limits which paths the remote can reach.

### What to expect

| | |
|---|---|
| Checksums | none. ETags are opaque, so sync compares sizes. |
| Modification times | can't be set. Shows the server's Last-Modified (upload time) if it sends one, else `--default-time`. |
| Empty folders | can't exist. A folder appears when a document is written below it. |
| Copy/move on the server | not in the protocol. rclone downloads and re-uploads. |
| Content-Type | preserved on upload and download, which matters because remoteStorage apps rely on it |
| Concurrent app edits | overwrite and delete use `If-Match` (`conditional_writes`, on by default), so an app's newer write fails with a conflict instead of being lost |
| `vendor = armadietto` | works around armadietto returning percent-encoded listing keys and HEAD responses without Content-Length. Names that literally contain `%XX` can't be told apart there. |

## Development

The environment comes from [flox](https://flox.dev) (Go, just, `CGO_ENABLED=0`). Docker or podman is needed for the test server.

```sh
flox activate
just                    # list recipes
just test               # unit tests against an in-process fake server
just test-integration   # rclone's fstests suite; starts/stops armadietto in docker itself
eval "$(just server)"   # throwaway armadietto + RCLONE_CONFIG_TESTREMOTESTORAGE_* for manual poking
```

`fstest/testserver/init.d/TestRemoteStorage` follows rclone's convention for self-starting test servers, so the integration suite manages the container on its own.

## Docs

- [Research: feasibility, protocol → rclone mapping, auth, testing, upstreaming](docs/research/rclone-remotestorage-backend.md)
- [Spike: loopback implicit-grant token capture](docs/spikes/loopback-implicit-grant.md). Works with armadietto and 5apps.

## Mounting

```sh
./rclone-remotestorage mount myrs: ~/rs --vfs-cache-mode full --dir-cache-time 30s
```

Edits made by remoteStorage apps show up once the directory cache expires (`--dir-cache-time`). Change polling via the root folder's ETag is planned.
