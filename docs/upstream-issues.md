# Upstream issues to file

These are server bugs this backend works around. Each one has a draft issue ready to file. **Nothing here has been filed yet:** filing is a public action, so confirm with the project owner first. When an issue is filed, record its link and date here. When it is fixed upstream, note the version and whether the workaround can go.

## armadietto

Checked against `remotestorage/armadietto` at `3251697` (2026-10-14), using the classic/monolithic server, Docker image `remotestorage/armadietto-monolithic:latest`. Searching the repo's issues on 2026-10-19 found nothing existing on either topic.

The modular server (`lib/routes/S3_store_router.js`) doesn't use `decodeURI` for listings and is probably not affected by issue 1. That is untested.

### 1. Folder listing keys are percent-encoded (spec says item names)

**Status:** not filed. **Workaround:** `vendor = armadietto` (`armadiettoName` in `backend/remotestorage/remotestorage.go`).

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

**Status:** not filed. **Workaround:** with `vendor = armadietto`, metadata comes from the parent listing instead of HEAD.

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
