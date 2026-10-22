# rclone-remotestorage

A generic [rclone](https://rclone.org) backend for the [remoteStorage](https://remotestorage.io) protocol ([draft-dejong-remotestorage](https://datatracker.ietf.org/doc/draft-dejong-remotestorage/)). It should work with any compliant server: 5apps, armadietto, php-remote-storage, mysteryshack, RSilo, and others. The end goal is `rclone mount` of a remoteStorage account as a local drive.

Status: early. Browser login, read, write, list, delete and `rclone mount` work. rclone's fstests integration suite passes against armadietto (automated, docker) and against a real 5apps account (manual run, 2026-10-19). Login is tested end to end against armadietto and was used for real against 5apps.

## How it is built

This is an out-of-tree backend. `backend/remotestorage` is an ordinary rclone backend package, and `cmd/rclone` builds stock rclone (all backends and commands, pinned in `go.mod`) with it added. No fork of rclone is needed. Upstreaming later means copying `backend/remotestorage/` into rclone.

## Usage

```sh
just build
./rclone-remotestorage config create myrs remotestorage user_address=alice@5apps.com
./rclone-remotestorage lsf myrs:
./rclone-remotestorage copy ./notes myrs:documents/notes
```

Logging in looks the account up with WebFinger, opens your browser on the server's consent page, and captures the token on a local listener (`http://127.0.0.1:53682`). Interactive `rclone config` asks the same questions. Re-run the login with `rclone config reconnect myrs:`, for example when a token expires.

- **Narrower access:** set `scope` (advanced, default `*:rw`) to a category such as `documents:rw` to limit the remote to that folder. This is a good idea for a first try on an account holding live app data.
- **No browser on this machine:** answer "n" to "Use web browser…". rclone prints an `rclone authorize "remotestorage" "…"` command to run on a machine that has a browser and this build; paste its output back. You can also paste a bearer token directly.
- **By hand:** leave `user_address` blank and give `storage_root` (the `href` of the remoteStorage link in the server's WebFinger response) and `token`. Example: `config create myrs remotestorage storage_root=https://example.com/storage/alice token=<bearer token>`.

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

You need Go, [just](https://just.systems) and [bun](https://bun.sh), plus Docker or podman for the armadietto test server. The Justfile sets `CGO_ENABLED=0` and points `RCLONE_CONFIG` at `./rclone.conf` (gitignored), so remotes made while working on this project never touch `~/.config/rclone/rclone.conf`. Test recipes each use a fresh throwaway config. Plain `go test` would fall back to the global config, so prefer the `just` recipes.

```sh
just                    # list recipes
just test               # unit tests against an in-process fake server
just test-integration   # rclone's fstests suite against armadietto and RSilo; starts/stops both itself
just test-login         # end-to-end browser login (config create -> consent -> headless Chromium)
eval "$(just server)"   # throwaway armadietto + RCLONE_CONFIG_TESTREMOTESTORAGE_* for manual poking
```

The integration suite runs against two local servers, started and stopped automatically through rclone's convention for self-starting test servers (`fstest/testserver/init.d/`):

| Remote | Server | Covers |
|---|---|---|
| `TestRemoteStorage:` | armadietto (classic) in Docker | old-style server: draft -10, no Last-Modified, no Range, its listing-name quirks (`vendor = armadietto`) |
| `TestRemoteStorageRSilo:` | [RSilo](https://github.com/jonocodes/RSilo) offline mode (bun + SQLite + a local directory) | modern server: draft -22, Last-Modified, HEAD with Content-Length, `206` byte ranges, spec-correct item names |

RSilo is cloned into `~/.cache/rclone-remotestorage/rsilo` at `RSILO_REF` (default `main`). Set `RSILO_SRC=<path>` to run a local RSilo checkout instead, for example while changing RSilo. Setting `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` makes that RSilo store its files in S3, e.g. a local SeaweedFS.

## Docs

- [Research: feasibility, protocol → rclone mapping, auth, testing, upstreaming](docs/research/rclone-remotestorage-backend.md)
- [Spike: loopback implicit-grant token capture](docs/spikes/loopback-implicit-grant.md). Works with armadietto and 5apps.
- [Upstream issues](docs/upstream-issues.md): server bugs the backend works around, with repros. Filed: armadietto [#173](https://github.com/remotestorage/armadietto/issues/173), [#174](https://github.com/remotestorage/armadietto/issues/174); SeaweedFS [#11908](https://github.com/seaweedfs/seaweedfs/issues/11908). RSilo was fixed in [jonocodes/RSilo#41](https://github.com/jonocodes/RSilo/pull/41).

## Waiting on upstream

Tickets we've filed and are waiting on. `just upstream-status` shows their current state. When one is fixed, drop the workaround and record the fixed version in [docs/upstream-issues.md](docs/upstream-issues.md).

| Ticket | Filed | What it is | Remove when fixed |
|---|---|---|---|
| [remotestorage/armadietto#173](https://github.com/remotestorage/armadietto/issues/173) | 2026-10-19 | Classic server's folder listings return percent-encoded item names | name decoding under `vendor = armadietto` (`armadiettoName`) |
| [remotestorage/armadietto#174](https://github.com/remotestorage/armadietto/issues/174) | 2026-10-19 | Classic server's HEAD omits Content-Length | `vendor = armadietto` skipping HEAD for metadata |

Done: RSilo's item names and Range support were fixed in [jonocodes/RSilo#41](https://github.com/jonocodes/RSilo/pull/41) (merged 2026-10-20). SeaweedFS turning `\` into `/` in S3 keys ([seaweedfs/seaweedfs#11908](https://github.com/seaweedfs/seaweedfs/issues/11908)) was fixed by the opt-in `-s3.allowBackslashInKey` flag in [#11911](https://github.com/seaweedfs/seaweedfs/pull/11911), merged 2026-10-22. No release has it yet (4.48 predates it), and once one does, RSilo's CI should add the flag. Not filed: 5apps answering 401 instead of 403 (probably not worth filing).

## Mounting

```sh
./rclone-remotestorage mount myrs: ~/rs --vfs-cache-mode full --dir-cache-time 30s
```

Edits made by remoteStorage apps show up once the directory cache expires (`--dir-cache-time`). Change polling via the root folder's ETag is planned.

## Testing against a real account

Log in to a scratch scope (the remote lands in `./rclone.conf`), then point the integration suite at it:

```sh
./rclone-remotestorage config create rs5 remotestorage user_address=<user>@5apps.com scope=rclone-test:rw
just test-remote rs5:rclone-test
```

It works in a random subfolder and removes it afterwards. Last run against 5apps (2026-10-19): 86 passed, 0 failed, in 75s.

## License

[GPL-3.0](LICENSE).
