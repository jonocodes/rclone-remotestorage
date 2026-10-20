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

### Backslash in an S3 key is listed as "/"

**Status:** not filed. **Seen with:** `chrislusf/seaweedfs:4.48` (RSilo's CI S3 server), running RSilo with S3 storage.

An object stored under `probe/back\slash.txt` (sent as `%5C`) can be read back with HEAD under that exact key, but ListObjectsV2 reports it as `probe/back/slash.txt`. R2 and AWS S3 keep `\` literally. The effect is that rclone's `FsEncoding/punctuation` case fails when RSilo runs on SeaweedFS, but not on its local store. There is no workaround in RSilo or in the backend, since it's a server listing bug.
