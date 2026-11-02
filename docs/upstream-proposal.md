# Proposal: a remoteStorage backend in rclone

**Filed 2026-11-02 as [rclone/rclone#10236](https://github.com/rclone/rclone/issues/10236).** The draft below matches what was posted. Before filing, this was a draft of the issue that proposes the backend to rclone, before any PR is opened.

Test numbers were re-run 2026-11-02 (`just test-integration`: armadietto and RSilo each 86 passed, 0 failed; `just test-all`: all suites passed first try). For the PR, take ports 18001 and 18002 in rclone's `fstest/testserver/init.d/PORTS.md` (free in v1.75.2). The 5apps fstests run was repeated 2026-11-02 after all the fixes and passed in 76s (no per-test count; the 2026-10-19 run was 86 passed, 0 failed). A manual `nfsmount` check on macOS against 5apps passed the same day.

## Where to file

As a **GitHub issue** on `rclone/rclone`, using the Feature request form. CONTRIBUTING.md says "If it is a big feature, then make an issue first so it can be discussed", and earlier new-backend proposals were filed this way and given the `new backend` label (e.g. [#9661](https://github.com/rclone/rclone/issues/9661), [#8456](https://github.com/rclone/rclone/issues/8456)). The form also has an optional forum-post field. A forum thread first is encouraged but not required. ncw reviews new backends himself and is busy (see the kDrive PR, [#8941](https://github.com/rclone/rclone/pull/8941)), so the issue is short and ends with concrete questions.

The form's checkboxes ask you to confirm you've searched the forum and issues. As of 2026-11-02, `gh search issues remotestorage --repo rclone/rclone` finds only an unrelated issue (#9308).

## Decide before filing

- [x] **Test account.** The draft offers to *try* to arrange a dedicated 5apps account for integration.rclone.org, without promising one (decided 2026-11-02). Update it if you secure one.
- [x] **RSilo as a Docker image.** The draft offers to package RSilo as an image for rclone's integration server, which may not have bun (decided 2026-11-02).
- [x] **Maintainer.** Yes: the draft says you'll maintain the backend (decided 2026-11-02).
- [ ] **Use in practice.** You may want some real-world mileage first (mount and sync against an account with live app data). The draft doesn't claim any beyond the test runs.
- [ ] **AI assistance.** rclone allows AI-assisted contributions, but you must understand every line and be able to answer review questions without the tool (CONTRIBUTING "AI-assisted contributions"). That applies to the PR, not this issue. Before the PR, drop the two comments that only make sense in this repo (the `init()` logger hack, the spike-doc pointer in `auth.go`); comment density already matches rclone's.
- [x] `rclone version` output is in the draft (from the build used for `just test-all`).

## Draft

Fields follow the Feature request form; the project owner edited three lines when posting. Shortened 2026-11-02: the question about ETags in the VFS fingerprint (same-size app edits go unnoticed with `--vfs-cache-mode full`) was cut, to raise later if the backend is accepted. Earlier versions are in git history.

**Title:** New backend proposal: remoteStorage (remotestorage.io)

**rclone version:**

```
rclone v1.75.2-DEV
- os/version: nixos 26.05 (64 bit)
- os/kernel: 6.18.57 (x86_64)
- os/type: linux
- os/arch: amd64
- go/version: go1.26.4
- go/linking: static
- go/tags: none
```

**What problem are you trying to solve?**

> [remoteStorage](https://remotestorage.io) is an open protocol for per-user storage that web apps write to, specified as an IETF Internet-Draft ([draft-dejong-remotestorage](https://datatracker.ietf.org/doc/draft-dejong-remotestorage/), currently -27). Users pick a provider, either hosted (5apps) or self-hosted (armadietto, RSilo), and their apps keep their data there. Outside a browser there's no good way to back that data up, sync it, or edit it with local tools. An rclone backend would make it an ordinary remote, `mount` included.
>
> I couldn't find an earlier rclone issue or forum thread.

**How do you think rclone should be changed to solve that?**

> Add a `remotestorage` backend. I have one working out of tree: https://github.com/jonocodes/rclone-remotestorage. It's a normal `backend/remotestorage` package of about 1,300 lines, using `lib/rest`, `fs/fshttp`, `lib/pacer`, `lib/encoder` and `api/types.go`. While it is a POC for now, I build it into stock rclone v1.75.2, so upstreaming is mostly copying the directory.
>
> - **Supported:** the protocol is a stripped-down WebDAV (GET/HEAD/PUT/DELETE plus JSON folder listings). The backend does list, read with Range, write, streamed upload, delete and `mount`. It preserves Content-Type, which apps rely on. It uses `If-Match` on overwrite and delete so `copy`/`sync` don't clobber an app's newer write. A webdav-style `vendor` option handles armadietto's quirks.
> - **Not possible in the protocol:** hashes (ETags are opaque), settable modtimes, empty directories, server-side copy or move.
> - **Testing:** with the backend copied into rclone v1.75.2, `go run ./fstest/test_all -backends remotestorage` passes cleanly (backend, `fs/operations`, `fs/sync`, `cmd/bisync`, `vfs`). It runs against two self-starting servers in `fstest/testserver/init.d/`, both built on `run.bash`: armadietto in Docker (draft -10) and RSilo (draft -22). `cmd/gitannex` is skipped, as for the other self-starting servers. fstests also pass against a real 5apps account.
>
> **Questions before I open a PR:**
>
> 1. **Would you take it?** If so, I'll maintain it. I am active in the remoteStorage community.
> 2. **Auth.** Servers offer only the OAuth implicit grant, found via WebFinger, so the token arrives in the URL fragment, which `lib/oauthutil` doesn't handle. The backend runs its own loopback listener on 127.0.0.1:53682. Otherwise it follows oauthutil's conventions (`config_is_local`, `rclone authorize`, a pasted token, `config reconnect`). Is that OK in the backend, or would you prefer an implicit-grant mode in `oauthutil`?
> 3. **Integration testing.** Are the self-starting armadietto and RSilo servers enough for integration.rclone.org? RSilo runs on bun today, and I can publish it as a Docker image. If you need a hosted account, I can try to arrange a 5apps one.
> 4. **Mount renames.** The VFS won't rename without Move or Copy, and the protocol has neither. So the backend has a `Copy` that always returns `fs.ErrorCantCopy`, as iclouddrive does, to get rclone's download-and-re-upload fallback. Is that acceptable, or would you rather the VFS fall back by itself? ([#7834](https://github.com/rclone/rclone/issues/7834) is the same problem for linkbox.)
