# Proposal: a remoteStorage backend in rclone

This is a draft of the issue that proposes the backend to rclone, before any PR is opened. Posting it is a public action, so the project owner files it. When it's filed, record the link and date here and in the README.

Test numbers were re-run 2026-11-02 (`just test-integration`: armadietto and RSilo each 86 passed, 0 failed). The 5apps run is from 2026-10-19 (86 passed, 0 failed), before the mount fixes, so re-run it with `just test-remote` before filing.

## Where to file

As a **GitHub issue** on `rclone/rclone`, using the Feature request form. CONTRIBUTING.md says "If it is a big feature, then make an issue first so it can be discussed", and earlier new-backend proposals were filed this way and given the `new backend` label (e.g. [#9661](https://github.com/rclone/rclone/issues/9661), [#8456](https://github.com/rclone/rclone/issues/8456)). The form also has an optional forum-post field. A forum thread first is encouraged but not required. ncw reviews new backends himself and is busy (see the kDrive PR, [#8941](https://github.com/rclone/rclone/pull/8941)), so the issue is short and ends with concrete questions.

The form's checkboxes ask you to confirm you've searched the forum and issues. As of 2026-11-02, `gh search issues remotestorage --repo rclone/rclone` finds only an unrelated issue (#9308).

## Decide before filing

- [x] **Test account.** The draft offers to *try* to arrange a dedicated 5apps account for integration.rclone.org, without promising one (decided 2026-11-02). Update it if you secure one.
- [ ] **RSilo as a Docker image.** The draft offers to package RSilo as an image for rclone's integration server, which may not have bun. Drop that if you won't do it.
- [ ] **Maintainer.** The draft says you'll maintain the backend. Keep that only if you mean it, since rclone weighs it heavily for niche backends.
- [ ] **Use in practice.** You may want some real-world mileage first (mount and sync against an account with live app data). The draft doesn't claim any beyond the test runs.
- [ ] **AI assistance.** rclone allows AI-assisted contributions, but you must understand every line and be able to answer review questions without the tool (CONTRIBUTING "AI-assisted contributions"). That applies to the PR, not this issue. Also trim comments to rclone's style before the PR.
- [ ] Paste the real `rclone version` output from the build you test with.

## Draft

Fields follow the Feature request form.

**Title:** New backend proposal: remoteStorage (remotestorage.io)

**rclone version:** *(output of `./rclone-remotestorage version`)*

**What problem are you trying to solve?**

> [remoteStorage](https://remotestorage.io) is an open protocol for per-user storage that web apps write to, specified as an IETF Internet-Draft ([draft-dejong-remotestorage](https://datatracker.ietf.org/doc/draft-dejong-remotestorage/), currently -27). Users choose a provider and apps store their data there. There's a hosted provider (5apps) and several self-hostable servers (armadietto, php-remote-storage, mysteryshack, RSilo).
>
> There's no good way to get at that data outside a browser. You can't back it up with ordinary tools, sync it with another remote, or mount it. The existing CLI tools are Node scripts for one-off backups. rclone would make a remoteStorage account an ordinary remote: `copy`/`sync` for backup, and `mount` to edit app data with local tools.
>
> I couldn't find an earlier rclone issue or forum thread about it. There is a wish for it on the remoteStorage forum ([community #967](https://community.remotestorage.io/t/bind-remotestorage-server-backed-by-git/967)).

**How do you think rclone should be changed to solve that?**

> Add a `remotestorage` backend. I have one working out of tree: https://github.com/jonocodes/rclone-remotestorage. It's a normal rclone backend package that I build into stock rclone v1.75.2, so upstreaming is mostly copying `backend/remotestorage/` across.
>
> **What it does.** The protocol is a stripped-down WebDAV: GET/HEAD/PUT/DELETE on documents, plus JSON folder listings. The backend is about 1,200 lines of Go, login included. It uses `lib/rest`, `fs/fshttp`, `lib/pacer` and `lib/encoder`, with its API types in `api/types.go`.
>
> - list, read (with Range, and a fallback when a server ignores it), write, streamed upload, delete, `mount`
> - a `Copy` that always returns `fs.ErrorCantCopy`, so `mount` can rename (see question 4)
> - preserves Content-Type, which remoteStorage apps depend on
> - optional `If-Match` on overwrite and delete (on by default), so a document an app changed meanwhile fails with a conflict instead of being silently overwritten
> - a `vendor` option, like webdav's, for one server's quirks (armadietto: percent-encoded listing names and HEAD without Content-Length, reported as [armadietto#173](https://github.com/remotestorage/armadietto/issues/173) and [#174](https://github.com/remotestorage/armadietto/issues/174))
>
> Protocol limits it can't work around: no hashes (ETags are opaque), modtimes can't be set (Last-Modified is shown where the server sends it), no empty directories, and no copy or move on the server.
>
> **Testing.** The fstests integration suite passes (86 passed, 0 failed) against:
>
> - armadietto in Docker (an older server, draft -10)
> - RSilo in offline mode (a modern server, draft -22: Last-Modified, Range)
> - a real 5apps account (manual run)
>
> Both local servers already have `fstest/testserver/init.d/` scripts, so they start themselves during tests.
>
> **Questions before I open a PR:**
>
> 1. **Is this something you'd take?** If so, I'm happy to maintain it.
> 2. **Auth.** Every remoteStorage server only offers the OAuth implicit grant, with the server located by WebFinger. The token comes back in the URL fragment, and `lib/oauthutil` only handles `?code=`. So the backend runs its own loopback listener on 127.0.0.1:53682. It serves a small page that posts the fragment back, then checks `state`. Otherwise it follows oauthutil's conventions: `config_is_local`, `rclone authorize` for headless machines, a pasted token as a fallback, and `rclone config reconnect`. Is keeping this in the backend OK, or would you prefer a generic implicit-grant mode in `oauthutil`? (There are no refresh tokens in this flow. When a token expires, the user reconnects.)
> 3. **Integration testing.** Would the self-starting armadietto (Docker) and RSilo servers be enough for integration.rclone.org, or do you need a hosted account? RSilo currently runs with bun from a git checkout; I can provide it as a Docker image instead. If a hosted account is needed, I can try to arrange a dedicated 5apps test account, though I can't promise one yet.
> 4. **Renames in a mount.** The VFS refuses to rename unless the backend has Move or Copy, and the protocol has neither, so every rename failed, including editors that save to a temp file and rename it over the original. The backend now has a `Copy` that always returns `fs.ErrorCantCopy`, as iclouddrive does, so rclone falls back to download and re-upload. Is that acceptable, or would you rather the VFS fall back by itself for backends with neither? ([#7834](https://github.com/rclone/rclone/issues/7834) is the same problem for linkbox.)
> 5. **Change detection in the VFS cache.** With `--vfs-cache-mode full`, the cache spots a changed file by `fs.Fingerprint`: size, plus modtime and hash where supported. This backend has neither, so an app's edit that keeps the size the same goes unnoticed, and I recommend `--vfs-cache-mode writes` instead. Reporting Last-Modified as a read-only modtime would fix the cache, but it would make `sync` re-upload every same-size file. Every document has an ETag that changes whenever it does. Would you consider letting a backend add something like that to the fingerprint (e.g. an optional interface)?
