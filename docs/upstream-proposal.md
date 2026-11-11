# Proposal: a remoteStorage backend in rclone

**Filed 2026-11-02 as [rclone/rclone#10236](https://github.com/rclone/rclone/issues/10236).** The draft below matches what was posted. Before filing, this was a draft of the issue that proposes the backend to rclone, before any PR is opened.

Test numbers were re-run 2026-11-02 (`just test-integration`: armadietto and RSilo each 86 passed, 0 failed; `just test-all`: all suites passed first try). The 5apps fstests run was repeated 2026-11-02 after all the fixes and passed in 76s (no per-test count; the 2026-10-19 run was 86 passed, 0 failed). A manual `nfsmount` check on macOS against 5apps passed the same day.

## After filing: wait for agreement on the issue, then open the PR

As of 2026-11-10 the issue has no maintainer reply. **Don't open the PR until there is one.** rclone's pull request template (added by ncw 2026-06-18) says: "Anything beyond a trivial fix … should be discussed and agreed in an issue BEFORE you open a pull request. Pull requests for larger changes that have not been discussed in an issue first may be closed." Its checklist asks you to tick "This change is trivial **OR** it has been discussed and agreed in the linked issue."

Older history points the other way, which is why this doc briefly said to open the PR without waiting (2026-11-09). Of the 12 backends merged 2024-2026 that we checked, none waited for a yes on an issue, and ncw rarely commented on proposal issues (none on Filen #6728, Internxt #7610 or Cloudinary #7989). But all except dosya's PR (April 2026) predate the template rule. The one proposal since then, Quark Drive (#9661), kept its PR (#9662) as a draft pending agreement and has had no maintainer reply in 3.5 months. Measured from PR opening, ncw's first reply came the same or next day for about half of the 12 and after 1.5 to 6.5 months for the rest; the median time to merge was about 5 months.

So instead of a PR, we commented on #10236 (2026-11-10, below) linking the pushed branch and its passing `test_all` run, so there is concrete code to agree to. A forum.rclone.org post pointing at the issue is the other channel the issue form suggests.

The template also requires, for a new backend, "A test account so the maintainers can run the integration tests daily". Our position (decided 2026-11-10, stated in the comment): none is needed, because the self-starting Docker servers cover it the way they do for sftp, webdav and smb. That replaced the original question 3 and the offer to try for a 5apps account.

The fork's commits were squashed into one, `4fd3ce0dc` (`remotestorage: add remoteStorage backend - fixes #10236`), on 2026-11-10, as CONTRIBUTING's "Squashing your commits" describes. Same content as the two commits the comment was posted on. Rebase it on master before opening the PR.

### Comment on #10236

Posted 2026-11-10 as [#issuecomment-6787340801](https://github.com/rclone/rclone/issues/10236#issuecomment-6787340801), on fork commits `08e0083d2` and `bc42913b0`:

> I've pushed the implementation so there's concrete code to look at: https://github.com/rclone/rclone/compare/master...jonocodes:rclone:remotestorage (on current master, including tests and docs).
>
> `go run ./fstest/test_all -backends remotestorage` passes on master against both self-starting servers: backend, fs/operations, fs/sync, cmd/bisync and vfs, all first try. `make quicktest` and golangci-lint (linux/windows/darwin/freebsd/openbsd) are clean.
>
> **Test account:** not needed. remoteStorage is an open protocol with self-hostable servers, so the backend is tested the same way as sftp, webdav and smb: `fstest/testserver/init.d/` starts two servers in Docker. `TestRemoteStorage` runs armadietto, the reference server (an older spec revision, draft -10), and `TestRemoteStorageRSilo` runs RSilo (a modern one, draft -22). Between them they cover both the old and new protocol behaviour. Each script creates a user and gets an access token by itself, so the integration server can run both without any credentials or external service.
>
> Following the PR template, I haven't opened a PR yet. The remaining questions from above, in short:
>
> 1. Would you take it? I'll maintain it.
> 2. Implicit-grant login in the backend, or an implicit-grant mode in `lib/oauthutil`?
> 3. Is the decline-only `Copy` for mount renames OK?
>
> Happy to change the approach on any of these.

- **Fork:** [jonocodes/rclone](https://github.com/jonocodes/rclone), branch `remotestorage` off master, checked out at `../rclone`. `just upstream-sync` regenerates everything the PR adds from this repo (see the README's "How it is built"); `just upstream-test-all` runs `test_all` there for the result the PR must quote.
- **Ports:** 28642 (armadietto) and 28643 (RSilo), the next free ones in rclone's 286xx range.
- **RSilo:** runs from `ghcr.io/jonocodes/rsilo:0.3.0`, so the integration server needs only Docker, not bun.
- **`versionIntroduced`:** v1.77 (master is v1.77.0-DEV; kDrive and dosya took v1.76).

## Where to file

As a **GitHub issue** on `rclone/rclone`, using the Feature request form. CONTRIBUTING.md says "If it is a big feature, then make an issue first so it can be discussed", and earlier new-backend proposals were filed this way (e.g. [#9661](https://github.com/rclone/rclone/issues/9661), [#8456](https://github.com/rclone/rclone/issues/8456); a collaborator labeled #8456 `new backend` the same day, while #9661 never got that label). The form also has an optional forum-post field. A forum thread first is encouraged but not required. ncw reviews new backends himself and is busy (see the kDrive PR, [#8941](https://github.com/rclone/rclone/pull/8941)), so the issue is short and ends with concrete questions.

The form's checkboxes ask you to confirm you've searched the forum and issues. As of 2026-11-02, `gh search issues remotestorage --repo rclone/rclone` finds only an unrelated issue (#9308).

## Decide before filing

- [x] **Test account.** The draft offers to *try* to arrange a dedicated 5apps account for integration.rclone.org, without promising one (decided 2026-11-02). Update it if you secure one. Superseded 2026-11-10: no test account needed (see "After filing").
- [x] **RSilo as a Docker image.** The draft offers to package RSilo as an image for rclone's integration server, which may not have bun (decided 2026-11-02).
- [x] **Maintainer.** Yes: the draft says you'll maintain the backend (decided 2026-11-02).
- [ ] **Use in practice.** You may want some real-world mileage first (mount and sync against an account with live app data). The draft doesn't claim any beyond the test runs.
- [ ] **AI assistance.** rclone allows AI-assisted contributions, but you must understand every line and be able to answer review questions without the tool (CONTRIBUTING "AI-assisted contributions"). That applies to the PR, not this issue. The two comments that only made sense in this repo are gone (2026-11-09): the logger hack moved to `remotestorage_outoftree.go`, which the sync leaves out, and the spike-doc pointer in `auth.go` was dropped.
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
