# Upstream issues to file

These are server bugs this backend works around. Each one has a draft issue ready to file. Filing is a public action, so confirm with the project owner first. When an issue is filed, record its link and date here. When it is fixed upstream, note the version and whether the workaround can go.

## armadietto

Checked against `remotestorage/armadietto` at `3251697` (2026-10-14), using the classic/monolithic server, Docker image `remotestorage/armadietto-monolithic:latest`.

### Prior reports (searched 2026-10-19)

Neither bug has been reported or discussed anywhere we could find:

- **armadietto issues and PRs** (open and closed): nothing for `decodeURI`, encoding, percent, special characters, listing names, HEAD or Content-Length. The nearest is [#151](https://github.com/remotestorage/armadietto/issues/151) (raucao, 2025-12), about the classic server's inconsistent spec-version claims. It led to [#155](https://github.com/remotestorage/armadietto/pull/155), which now advertises `-10`. That issue also calls the classic server "legacy" and says it should be "killed off as soon as possible", though self-hosters who don't want passkeys still rely on it. Expect a fix to be low priority.
- **remotestorage.js, spec and api-test-suite issues:** no armadietto-specific encoding reports. The spec has long-open threads on what `URI_ENCODE` means and whether listings show encoded or decoded names ([spec#103](https://github.com/remotestorage/spec/issues/103), [#119](https://github.com/remotestorage/spec/issues/119), [#162](https://github.com/remotestorage/spec/issues/162)). In #119, untitaker reads the spec as listings showing names *unencoded*, which matches the draft below.
- **community.remotestorage.io:** nothing. The only armadietto thread found ([t/759](https://community.remotestorage.io/t/759), If-None-Match/304) is unrelated.
- **Git history:** both bugs come from lesion's 2018 rewrite. `decodeURI` arrived in `09c32ed` ("cleaning, v10 fully compliant", 2018-05-14). The HEAD branch is from `af5e781` ("head same as get without content", the same day). Neither line has been touched since.
- **Modular server** (`lib/routes/S3_store_router.js`): doesn't use `decodeURI` for listings, and its GET path sets `Content-Length` from S3. Probably unaffected by both, but untested.

### It breaks remotestorage.js apps too

remotestorage.js uses listing keys as-is and builds URLs by running `encodeURIComponent` on each path segment (`cleanPath`, `src/util.ts` L59). Against armadietto, simulated with curl:

```
PUT t/my%20notes%3F/todo%20list.txt   → 201
GET t/                                 → items: {"my%20notes%3F/": …}
GET t/my%2520notes%253F/               → 200, items: {}   (rs.js re-encoding the listed key: the folder looks empty)
```

So any app that lets users pick names with spaces or punctuation sees those folders as empty on armadietto. That makes issue 1 a user-visible bug, not just a client nit, and is the strongest argument for the report. (This was simulated with curl from rs.js's `cleanPath`, not run with rs.js itself.)

### 1. Folder listing keys are percent-encoded (spec says item names)

**Status:** filed 2026-10-19 as [#173](https://github.com/remotestorage/armadietto/issues/173). **Workaround:** `vendor = armadietto` (`armadiettoName` in `backend/remotestorage/remotestorage.go`).

Draft:

> **Folder listings return percent-encoded item names**
>
> draft-dejong-remotestorage §4 says folder listings map *item names* to descriptions, and requests use the URI-encoded form of those names. armadietto (classic server) returns listing keys that are still partly or fully percent-encoded:
>
> ```sh
> curl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: text/plain' --data x \
>   "$ROOT/t/a%20b%3F/c%20d%40.txt"
> curl -H "Authorization: Bearer $T" "$ROOT/t/"          # items: {"a%20b%3F/": …}     expected "a b?/"
> curl -H "Authorization: Bearer $T" "$ROOT/t/a%20b%3F/" # items: {"c d%40.txt": …}    expected "c d@.txt"
> ```
>
> - **Folder** names are not decoded at all.
> - **Document** names go through `decodeURI` (`lib/stores/file_tree.js` L196, L235, L306), which deliberately leaves reserved characters (`; / ? : @ & = + $ , #`) encoded. It also turns `%25` into `%`, so the result is ambiguous: a name that literally contains `%40` can't be told from one containing `@`.
>
> `decodeURIComponent` on the path segment, applied to both folders and documents, would match the spec. Existing data stored under encoded names may need a migration, or a decode when listing.

### 2. HEAD responses omit Content-Length

**Status:** filed 2026-10-19 as [#174](https://github.com/remotestorage/armadietto/issues/174). **Workaround:** with `vendor = armadietto`, metadata comes from the parent listing instead of HEAD.

Draft:

> **HEAD on a document doesn't return Content-Length**
>
> `GET` returns `Content-Length`, but `HEAD` on the same document returns only `Content-Type` and `ETag`:
>
> ```sh
> curl -I -H "Authorization: Bearer $T" "$ROOT/t/x.txt"   # no Content-Length
> ```
>
> `lib/controllers/storage.js` L71 sets the header only when the request is not a HEAD: `if (item && item.value && !head) this._headers['Content-Length'] = item.value.length;`. The size is already known from the folder metadata (`item['Content-Length']`). HEAD is meant to return the same headers as GET (RFC 9110 §9.3.2), and clients use it to read a document's size without downloading it.

### Not bugs, for the record

- **No `Last-Modified`.** The classic server advertises draft `-10`, and Last-Modified only arrived in `-11`.
- **No Range support.** Range is a MAY in the spec.
- **Path regex `/^\/([a-z0-9%.\-_]+\/?)*$/i`** (`lib/stores/core.js` L8). Characters such as `~` or `!` must be percent-encoded, even though RFC 3986 allows them raw. This is strict but consistent. The backend encodes everything outside `[A-Za-z0-9._-]`.

## 5apps

### Out-of-scope paths answer 401, not 403

**Status:** not filed, and possibly not worth filing. **Workaround:** `NewFs` treats 401 or 403 from its root-is-a-file probe as "not a file", and the 401 error message mentions scope.

With a `rclone-test:rw` token, `HEAD /<user>/rclone-test` (a document outside the category) and `GET /<user>/documents/` both return 401. The spec (§5) lists 403 for "access to the requested path is not allowed with this token" and 401 for a missing or invalid token. 5apps' storage server isn't public, so the only place to report this would be 5apps support.

## RSilo

Running fstests against RSilo's offline server found problems with item names, missing Range support, and fidelity problems in the offline store. All were fixed in [jonocodes/RSilo#41](https://github.com/jonocodes/RSilo/pull/41), merged 2026-10-20, and the backend needs no workarounds for RSilo.

## SeaweedFS

### S3 keys: "\" is turned into "/" (and "//" collapsed), so distinct keys alias

**Status:** not filed. Verified 2026-10-20 against `chrislusf/seaweedfs:4.48` (`weed version`: `30GB 4.48 530be3e37 linux amd64`), using the aws CLI 2.34 and curl's SigV4 directly, with no RSilo code involved.

**What happens:** `NormalizeObjectKey` (`weed/s3api/s3_constants/header.go`, around L255 at 4.48) runs on every S3 object key. It replaces `\` with `/` "for cross-platform compatibility" and collapses duplicate slashes. As a result:
- `a\b` and `a/b` are the same object for PUT, GET, HEAD and DELETE.
- Listings show the slash form. With `delimiter=/`, the object shows up as a folder.

AWS S3 and R2 treat `\` and `//` as ordinary key characters. This is deliberate: it has applied to every key since [#7873](https://github.com/seaweedfs/seaweedfs/pull/7873) (2025-12-25). It is the S3-side counterpart of the filer fix in [#11243](https://github.com/seaweedfs/seaweedfs/issues/11243) / [#11246](https://github.com/seaweedfs/seaweedfs/pull/11246). No issue questions the AWS divergence (searched 2026-10-20).

**Not a security issue.** Their [SECURITY.md](https://github.com/seaweedfs/seaweedfs/blob/master/SECURITY.md) sends findings that cross no trust boundary to the normal tracker. I tested an identity limited to `Write:repro/allowed/*`:
- Permission checks use the normalized key, so writing `other\x.txt` gives 403.
- `IsValidObjectKey` rejects `..` after normalization, so `allowed/..\secret.txt` and similar give 400.
- Data outside the prefix was untouched.

**Effect here:** rclone's `FsEncoding/punctuation` case fails when RSilo stores files on SeaweedFS (RSilo's CI S3 server). On R2, which RSilo uses in production, it is not an issue (B2 is untested). There is no workaround in RSilo or the backend.

**Filing:** a public issue at github.com/seaweedfs/seaweedfs, using their bug template (`.github/ISSUE_TEMPLATE.md`: describe the bug, system setup with the exact `weed` command line, OS, `weed version`, expected behavior). Questions go to Discussions, but this is a behaviour report. Their good-example issue is #1005. Because the behaviour is intentional, the ask is an opt-out (e.g. a flag), not a revert.

Draft (their template):

> **S3 API: backslashes in object keys are converted to "/" (and "//" collapsed), so distinct AWS keys alias the same object**
>
> **Describe the bug**
> `NormalizeObjectKey` (`weed/s3api/s3_constants/header.go`) rewrites every S3 object key: `\` becomes `/` and duplicate slashes are collapsed. On AWS S3 both are ordinary key characters, so keys that are distinct there alias one object here:
>
> ```
> $ aws s3api put-object --bucket repro --key 'probe/back\slash.txt' --body f.txt     # "hello"
> $ aws s3api list-objects-v2 --bucket repro --prefix probe/ --query 'Contents[].Key'
> ["probe/back/slash.txt"]
> $ aws s3api list-objects-v2 --bucket repro --prefix probe/ --delimiter /
> CommonPrefixes: ["probe/back/"], Contents: none
> $ aws s3api put-object --bucket repro --key 'probe/back/slash.txt' --body g.txt     # "other"
> $ aws s3api get-object --bucket repro --key 'probe/back\slash.txt' out.txt && cat out.txt
> other
> $ aws s3api delete-object --bucket repro --key 'probe/back\slash.txt'               # removes probe/back/slash.txt
> ```
>
> (All commands use `--endpoint-url http://localhost:8333` and path-style addressing. A raw `%5C` sent with curl `--aws-sigv4` behaves the same.)
>
> Clients that sync arbitrary file names, such as rclone, or that store user-chosen names, such as remoteStorage servers, can't round-trip a name containing `\` or `//`. Writing one can silently overwrite a different object.
>
> **System Setup**
> - `docker run -p 8333:8333 -v $PWD:/cfg:ro chrislusf/seaweedfs:4.48 server -s3 -s3.config=/cfg/s3.json` (default filer, no `filer.toml`)
> - Linux 6.18 (NixOS), podman
> - `weed version`: `30GB 4.48 530be3e37 linux amd64`
>
> **Expected behavior**
> As on AWS S3: `probe/back\slash.txt` and `probe/back/slash.txt` are two objects, and keys are listed exactly as written.
>
> I understand the conversion is deliberate (Windows compatibility, #7873) and that `IsValidObjectKey` relies on it to block `..` traversal. Could there be an option to keep keys verbatim for AWS compatibility, e.g. a flag on `weed s3`/`weed server -s3`? If the filer can't store `\` in a name, then at least documenting the divergence would help.
>
> **Additional context**
> Related: #11243 / #11246 (the filer-path side). Found while testing a remoteStorage server's S3 backend with rclone's integration suite.
