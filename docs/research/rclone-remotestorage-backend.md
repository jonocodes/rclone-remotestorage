# Could rclone get a generic remoteStorage backend that works with any compliant server?

Researched 2026-10-18 against:

- rclone v1.76.0 (`0c9826d`)
- `draft-dejong-remotestorage-27` (published 12 June 2026, expires 18 December 2026; earlier drafts -00 to -26 were used for history)
- `remotestorage/remotestorage.js` 2.0.0-beta.10 (`d899b5a`)
- `raucao/rs-backup` v2.0.0 (`7cdbb10`, 2026-10-06)
- `DougReeder/adaptive-backup` 0.9.0 (`b31117e`)
- Server sources:
  - armadietto (`3251697`)
  - php-remote-storage (`eb6cecc`)
  - mysteryshack (`7413ecc`)
  - liquor-cabinet (`5efab8b`, which runs 5apps Storage)
  - RSilo (`b6b6958`)

**Verdict: feasible, and small.** remoteStorage is a stripped-down WebDAV: GET/HEAD/PUT/DELETE on documents and JSON folder listings. It maps onto rclone's required `Fs`/`Object` interfaces with nothing left over. A first backend is about 800–1,200 lines of Go on `lib/rest` plus `lib/pacer`, comparable to a slimmed-down `backend/webdav`. The work splits roughly 1 week for data-plane code and fstests, plus 1–2 weeks for auth.

The data plane is easy. The hard parts are elsewhere:

1. **Authentication from a CLI.** Every server in the wild uses only the implicit grant, which puts the token in a URL fragment. rclone's `oauthutil` only handles `?code=`.
2. **No usable checksums or settable modtimes.** Sync falls back to comparing sizes only.
3. **Old spec versions.** Several live servers advertise drafts from before Last-Modified was added, and they disagree on path-character rules.

None of these blocks the backend. They do shape the design, and they make an upstream merge depend on solving test-account hosting.

---

## 1. rclone backend anatomy

### Required

A backend registers with `fs.Register(&fs.RegInfo{…})`, as `backend/webdav/webdav.go` does at L70. It must implement these interfaces:

| Interface | Methods | Source |
|---|---|---|
| `fs.Info` | `Name, Root, String, Precision, Hashes, Features` | [`fs/types.go` L62-L80](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/types.go#L62-L80) |
| `fs.Fs` | `List, NewObject, Put, Mkdir, Rmdir` | [`fs/types.go` L17-L58](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/types.go#L17-L58) |
| `fs.Object` | `SetModTime, Open, Update, Remove`, plus `ObjectInfo` (`Hash, Storable`) and `DirEntry` (`Fs, String, Remote, ModTime, Size`) | [`fs/types.go` L83-L132](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/types.go#L83-L132) |

The contracts that matter here:

> List … This should return ErrDirNotFound if the directory isn't found.

> Rmdir removes the directory (container, bucket) if empty. Return an error if it doesn't exist or isn't empty

> ModTime returns the modification date of the file. It should return a best guess if one isn't available

### Optional

Optional features are declared in `fs.Features`, a set of boolean flags plus function fields ([`fs/features.go` L13-L43](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/features.go#L13-L43)). The ones relevant here:

| Optional interface / flag | remoteStorage fit |
|---|---|
| `CanHaveEmptyDirectories` | **false**. Folders exist only while non-empty (see §2). |
| `ReadMimeType` / `WriteMimeType` (`fs.MimeTyper`) | **true / true**. Content-Type is first-class in the protocol, and apps depend on it. |
| `CaseInsensitive` | false. Paths are opaque strings. |
| `PutStreamer` ([L628-L636](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/features.go#L628-L636)) | **Yes, but optional.** The spec says servers "MUST support chunked transfer coding on PUT requests" (§4). Some servers buffer anyway: RSilo buffers to enforce quota. |
| `Purger` ([L505-L515](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/features.go#L505-L515)) | No. There is no recursive delete, and rclone's default walk-and-delete is equivalent. |
| `Copier` / `Mover` / `DirMover` (L516-L556) | **No.** The protocol has no COPY or MOVE. rclone falls back to download+upload (and delete for moves). |
| `ListRer` ([L666-L684](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/features.go#L666-L684)) | **No.** The docs say "Don't implement this unless you have a more efficient way of listing recursively", and the protocol has no recursive listing. |
| `ListPer` (L687-L702) | Possible but pointless. Each folder listing is a single response. |
| `Abouter` ([L718-L722](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/features.go#L718-L722)) | **No standard way.** The spec has no quota endpoint, only `507` on over-quota writes (§5). |
| `ChangeNotifier` ([L570](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/features.go#L570)) | **Good fit, phase 2.** The spec itself suggests polling the root folder ETag, because every write propagates up the tree (§13, *Distributed versioning*). Polling one GET per interval is cheap, and changed subtrees can be narrowed down from there. This would make `rclone mount` with `--poll-interval` pick up edits made by apps. |
| `PublicLinker` | Possible. A document under `/public/<module>/…` is readable without a token (§9), so a "public link" would really be a copy into `/public/`. Not worth doing in v1. |
| `OpenChunkWriter` / multipart | No. The spec forbids `Content-Range` on PUT, and armadietto, php-remote-storage and RSilo all reject it with 400. |

### Modtime and hash declarations

- `Precision()` returns a duration. `fs.ModTimeNotSupported` is defined as `100 * 365 * 24 * time.Hour` ([`fs/fs.go` L15-L17](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/fs.go#L15-L17)), and `backend/webdav` starts with `precision: fs.ModTimeNotSupported` (webdav.go L542).
- `Hashes()` returns a `hash.Set`. It is `hash.Set(hash.None)` when no hash is available. Backends can register custom hash types with `hash.RegisterHash`, but an opaque ETag is not a content hash, so registering one would not help.
- With `ModTimeNotSupported`, rclone's equality check stops at size ([`fs/operations/operations.go` L288-L295](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/operations/operations.go#L288-L295)):

  > if modifyWindow == fs.ModTimeNotSupported { fs.DebugfCtx(ctx, src, "Sizes identical") … return true }

- If a backend declares a finite precision but `SetModTime` returns `fs.ErrorCantSetModTime`, rclone treats every file as different and re-uploads it on every run ([L330-L336](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fs/operations/operations.go#L330-L336)):

  > "src and dst identical but can't set mod time without re-uploading"

  So **declaring 1-second precision because the listing carries Last-Modified would be a bug.** Use `ModTimeNotSupported`, and return Last-Modified from `ModTime()` as the "best guess".

### Conventions from CONTRIBUTING.md

From "Writing a new backend" ([L585-L692](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/CONTRIBUTING.md#L585-L692)):

- Copy a similar backend. Box is suggested for directory-based backends; webdav is the closest match here.
- Use `lib/rest` and `fs/fshttp`, which give `--dump bodies`, `--tpslimit` and `--user-agent` for free.
- Use `lib/encoder` for names.
- Put API types in `api/types.go`.
- Do not split the backend into `fs.go`/`object.go`.

Docs come from `docs/data/backends/<name>.yaml` (created with `bin/manage_backends.py create`) and `docs/content/<name>.md`. The latter has an autogenerated options block refreshed by `bin/make_backend_docs.py <name>` ([L694-L723](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/CONTRIBUTING.md#L694-L723)).

Plumbing to copy from webdav:

- **Retries.** `retryErrorCodes` covers 423, 425, 429, 500, 502, 503, 504 and 509, checked through `fserrors.ShouldRetryHTTP` and driven by `pacer` ([webdav.go L285-L313](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/backend/webdav/webdav.go#L285-L313)).
- **Static tokens.** webdav has `bearer_token` and `bearer_token_command` options ([L115-L121](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/backend/webdav/webdav.go#L115-L121)).
- **Retry-After.** `fserrors.NewErrorRetryAfter` honours `Retry-After`, as swift does. armadietto sends `429` with `Retry-After` ([`rateLimiterMiddleware.js` L93-L94](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/middleware/rateLimiterMiddleware.js#L93-L94)).

## 2. Protocol → rclone mapping

These are spec facts from draft-27 ([§3 Storage model](https://datatracker.ietf.org/doc/html/draft-dejong-remotestorage-27#section-3), [§4 Requests](https://datatracker.ietf.org/doc/html/draft-dejong-remotestorage-27#section-4), [§5 Response codes](https://datatracker.ietf.org/doc/html/draft-dejong-remotestorage-27#section-5), [§6 Versioning](https://datatracker.ietf.org/doc/html/draft-dejong-remotestorage-27#section-6)).

Folder listing format (§4):

> A successful GET request to a folder MUST be responded to with a JSON-LD document (content type 'application/ld+json'), containing as its 'items' field a map in which contained documents appear as entries <item_name> which map to a document description, and contained non-empty folders appear as entries <item_name> '/' which map to a folder description.

> A document description is a map containing one string-valued 'ETag' field, one string-valued 'Content-Type', one integer-valued 'Content-Length' field, and one string-valued 'Last-Modified' field.

Implicit folders (§4):

> GET requests to empty folders SHOULD be responded to with a folder description with no items … an empty folder MUST NOT be listed as an item in its parent folder.

> PUT and DELETE requests only need to be made to documents, and never to folders. A document PUT will make all ancestor folders along its path become non-empty; deleting the last document from a subtree will make that whole subtree become empty.

| rclone operation | remoteStorage request | Notes |
|---|---|---|
| `List(dir)` | `GET <root>/<dir>/` | Keys ending in `/` become `fs.Dir`, which has an ETag but no size or time. Other keys become `Object`s with size, Content-Type, ETag and Last-Modified. An empty `items` map is **indistinguishable from a missing folder**, so return `fs.ErrorDirNotFound` when it is empty, as bucket backends do. fstests accepts either outcome when `CanHaveEmptyDirectories` is false ([`fstests.go` L688-L700](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fstest/fstests/fstests.go#L688-L700)). Also accept pre-`-02` flat listings (`{name: etag}` without `items`), as remotestorage.js does ([`wireclient.ts` L345-L358](https://github.com/remotestorage/remotestorage.js/blob/d899b5aee41849fc94c45470cfad08a7363780b6/src/wireclient.ts#L345-L358)). |
| `NewObject(remote)` | `HEAD <root>/<remote>` | HEAD is a SHOULD (§4). If a server returns 405 or omits `Content-Length` on HEAD, fall back to listing the parent folder. A 404 becomes `fs.ErrorObjectNotFound`. HEAD on `x` when `x/` is a folder gives 404, which is the allowed answer. |
| `Open(options)` | `GET <root>/<remote>` with `Range:` | Range support is a MAY, advertised through the `http://tools.ietf.org/html/rfc7233` WebFinger property and `Accept-Ranges` (§4). If neither is present, emulate ranges client-side by discarding bytes, using `fs.OpenOption` handling as webdav/http do. 5apps advertises `"GET"`, armadietto leaves it commented out ([`routes/webfinger.js` L46](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/routes/webfinger.js#L46)), and php-remote-storage only enables it in production mode ([`WebfingerModule.php` L54](https://git.sr.ht/~fkooman/php-remote-storage/tree/eb6ceccd7efebd1fcfb909b829bdf70d47acf7c4/item/src/WebfingerModule.php#L54)). |
| `Put(in, src)` | `PUT <root>/<remote>` with `Content-Type` | Ancestors are created implicitly. Content-Type comes from `fs.MimeType(ctx, src)`, falling back to `application/octet-stream`; servers "MAY refuse" a PUT without one (§4). Optionally send `If-None-Match: *` (§6, 412 if the document exists). Keep this off by default, because rclone's `Put` may legitimately overwrite. Record the returned strong ETag. |
| `Update(in, src)` | `PUT` with `If-Match: "<etag from list/HEAD>"` | **Optimistic concurrency for free**: if an app changed the document since rclone listed it, the server returns 412 (§6). This should be a `conditional_writes` option, on by default. On 412, return a non-retryable error rather than clobbering the app's write. |
| `Remove()` | `DELETE` (optionally with `If-Match`) | Empty ancestors disappear automatically. |
| `Mkdir(dir)` | no-op | Folders cannot exist empty. Return nil, as with S3 without directory markers. |
| `Rmdir(dir)` | `GET <dir>/` to check emptiness, then no-op | Return an error if the folder has items. If it is empty, it "doesn't exist" in remoteStorage terms, but S3-like backends return nil here and fstests tolerates that. |
| `SetModTime` | — | `fs.ErrorCantSetModTime`. The protocol has no way to write Last-Modified. |
| `Hash` | — | `hash.None`. See §4 for why the ETag cannot stand in. |
| `ModTime` | `Last-Modified` from the listing or HEAD | Last-Modified was added in **-11 (June 2018)** and is absent from drafts -00 to -10. Many deployed servers advertise -05 or -10 and omit it (see §4). Fall back to the HTTP `Last-Modified` header, then to the zero time. Precision stays `ModTimeNotSupported`. |
| `About` | — | Not implemented. Map `507` to a clear "over quota" `fserrors.NoRetryError` and `413` to "file too large". |
| Empty dirs | — | `CanHaveEmptyDirectories: false`. `rclone sync --create-empty-src-dirs` silently can't create them. |
| Case | — | Case-sensitive. Names are opaque. |

**Path encoding.** The spec says (§4):

> Item names MAY contain all characters, before URI_ENCODE, except '/' and the null character '\0' and MUST NOT have zero length. Item names MUST NOT be equal to '.' or to '..'.

That is exactly rclone's `encoder.Base` (`EncodeZero | EncodeSlash | EncodeDot`, [`lib/encoder/standard.go` L15-L19](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/lib/encoder/standard.go#L15-L19)). Add `EncodeInvalidUtf8`, because listing keys travel as JSON strings.

On the wire, encode very aggressively. armadietto's classic server validates the *raw* path against `/^\/([a-z0-9%.\-_]+\/?)*$/i` ([`lib/stores/core.js` L8](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/stores/core.js#L8)) and returns 400 otherwise. Any character outside `[A-Za-z0-9.\-_]` must therefore be percent-encoded, including `~`, which RFC 3986 says not to encode. That makes rclone's `rest.URLPathEscapeAll`, which keeps `~` ([`lib/rest/url.go` L49-L71](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/lib/rest/url.go#L49-L71)), insufficient against that server, and the same applies to `encodeURIComponent`, used by rs-backup.

Running `rclone test info` against each server (CONTRIBUTING L609-L615) is the right tool to settle this. Expect fstests ignores like webdav's `FsEncoding/punctuation` ([`test_all/config.yaml` L499-L507](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fstest/test_all/config.yaml#L499-L507)).

**Name clashes.** A document `x` and a folder `x/` cannot coexist in one parent, and the server answers 409 (§4–5). Most rclone backends assume this anyway.

**Scope and root.** A token is valid for `*:rw` or `<module>:rw`. The module scope covers `/<module>/` and `/public/<module>/` (§9). With a module token, `GET /` returns 403, so the backend must refuse to list above the granted scope. The backend root should be the module, e.g. `rs:documents/…`, or the root when the scope is `*`.

## 3. Auth and discovery for a CLI

### Discovery

Discovery is `user@host` → `GET https://host/.well-known/webfinger?resource=acct:user@host`. Then pick the link with `rel` equal to `http://tools.ietf.org/id/draft-dejong-remotestorage` and read these properties (§10):

- `href` → storage root
- `http://remotestorage.io/spec/version` → API version
- `http://tools.ietf.org/html/rfc6749#section-4.2` → implicit-grant auth dialog
- `…rfc6750#section-2.3` → token in query string allowed
- `…rfc7233` → Range support
- **New in -26 (Dec 2025), [§10.1 Optional PKCE support](https://datatracker.ietf.org/doc/html/draft-dejong-remotestorage-27#section-10.1):** `…rfc6749#section-3.1` (authorization endpoint), `…rfc6749#section-3.2` (token endpoint) and `…rfc7636` (`"S256"`):

  > Clients SHOULD use PKCE when these properties are present; otherwise they SHOULD use the implicit grant as described above.

  > Note: PKCE will be required in protocol versions >= 2.0.

Real clients are more tolerant than the spec, and a generic backend must be too:

- remotestorage.js and rs-backup accept rels `remotestorage`, `remoteStorage` and the draft URI (via webfinger.js normalisation).
- They also accept a legacy `auth-endpoint` property, and take the version from the link's `type` when the property is missing:
  - [`discover.ts` L64-L68](https://github.com/remotestorage/remotestorage.js/blob/d899b5aee41849fc94c45470cfad08a7363780b6/src/discover.ts#L64-L68)
  - [`rs-backup/discovery.js`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/discovery.js)
  - [`rs-backup/AGENTS.md`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/AGENTS.md) ("Domain knowledge")
- php-remote-storage publishes two links, a `-05` one and a legacy `-03` `rel: remotestorage` ([`WebfingerModule.php` L45-L70](https://git.sr.ht/~fkooman/php-remote-storage/tree/eb6ceccd7efebd1fcfb909b829bdf70d47acf7c4/item/src/WebfingerModule.php#L45-L70)).
- armadietto and RSilo also emit a pre-standard `rel: remoteStorage, api: simple, auth, template` link.

Since discovery only runs during `rclone config`, the backend should store the results (`storage_root`, `auth_url`, `token_url`, `spec_version`, `range_get`). Normal operation then does not depend on WebFinger, and `storage_root` + `token` can also be entered by hand.

### Getting a token: what servers actually offer

| Server | Grant types | Evidence |
|---|---|---|
| armadietto (both servers) | implicit only | `if (req.data.response_type !== 'token') … 'unsupported_response_type'` ([`routes/oauth.js` L161-L163](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/routes/oauth.js#L161-L163)) |
| php-remote-storage | implicit only. Tokens expire after 90 days, and `expires_in` is sent as the string `'P90D'` instead of seconds (RFC 6749 expects seconds). | [`OAuthModule.php` L28, L86-L91, L155-L157](https://git.sr.ht/~fkooman/php-remote-storage/tree/eb6ceccd7efebd1fcfb909b829bdf70d47acf7c4/item/src/OAuth/OAuthModule.php#L28) |
| 5apps | implicit (live WebFinger checked 2026-10-18: `draft-dejong-remotestorage-13`, only the `section-4.2` auth property) | `https://5apps.com/.well-known/webfinger?resource=acct:raucao@5apps.com` |
| RSilo (example) | implicit plus code with required S256 PKCE and refresh rotation, but it advertises `-22` and **does not publish the §10.1 properties** | `src/routes/webfinger.ts` L79-L86, `src/protocol/constants.ts` L2 in this repo |
| elee1766/remotestorage (Go library) | targets -27 including §10.1 | [README](https://github.com/elee1766/remotestorage/blob/3b73f099cadc96c98348bd92d0cffd5ae31e596a/README.md) |

So **for the foreseeable future, a generic client must support the implicit grant**. Auth-code+PKCE is an upgrade path, not a baseline.

### Client identity

The spec says the server "MUST ignore the value of the client_id parameter in favor of relying on the origin of the redirect_uri" (§10). In practice servers require `client_id` to *equal* that origin. 5apps rejects anything else with `error=invalid_client` ([community thread #646](https://community.remotestorage.io/t/getting-invalid-client-trying-to-connect-to-5apps/646)), and [spec issue #183](https://github.com/remotestorage/spec/issues/183) proposes formalising this.

A CLI therefore sends `client_id=http://127.0.0.1:53682` with `redirect_uri=http://127.0.0.1:53682/`. The consent screen will then show "127.0.0.1:53682" as the app. That is accurate but not reassuring.

Whether every server accepts an `http://` loopback redirect is untested. The spec does not restrict schemes ([issue #175](https://github.com/remotestorage/spec/issues/175)), and one CLI-ish client author reported "I solved this by redirecting to `http://localhost:nnnnn/` instead" in the same thread.

### What rclone's oauthutil does today

- It binds `127.0.0.1:53682` and uses `RedirectURL = "http://127.0.0.1:53682/"` ([`oauthutil.go` L49-L62](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/lib/oauthutil/oauthutil.go#L49-L62)).
- The local handler reads only `req.Form.Get("code")` and checks `state` ([L1052-L1112](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/lib/oauthutil/oauthutil.go#L1052-L1112)). The fragment never reaches the server, so **implicit grant is impossible with the stock flow**.
- `configExchange` calls `oauth2Conf.Exchange(ctx, code)` with no options ([L1016-L1027](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/lib/oauthutil/oauthutil.go#L1016-L1027)). `Options.OAuth2Opts` can add `oauth2.S256ChallengeOption` to the auth URL, but there is no hook to pass `oauth2.VerifierOption` to the exchange. **PKCE needs a small oauthutil patch.** No backend in the tree uses PKCE today.
- `access_type=offline` is appended unless `NoOffline` is set (L896-L902). Set `NoOffline: true`.
- The `rclone authorize` remote-machine flow (`*oauth-remote`) and static tokens (`token` JSON without a `refresh_token` or expiry) work unchanged with `oauthutil.NewClient`.

### Recommended config flow

This is a custom `Config` state machine of the kind `backend/seafile` and `backend/pcloud` use.

1. Ask for the user address and run WebFinger. Store `storage_root` and the advertised capabilities.
2. Ask for the scope. The default is `*:rw`, with a per-module `<module>:rw` option (see below).
3. Choose the grant:
   - **a. PKCE**, if the `rfc6749#section-3.1`, `section-3.2` and `rfc7636` properties are present. Reuse oauthutil with the small patch for the code verifier. You get a refresh token for free.
   - **b. Implicit**, otherwise. Run a tiny custom loopback server on 127.0.0.1:53682. `GET /` serves a static page with about 10 lines of JS. The page reads `location.hash` (`access_token`, `state`, `error`) and POSTs it to `/token` on the same loopback server, which checks `state` and stores the token.

     This is exactly what remoteStorage's own CLI tools already do with a hosted page: [`adaptive-backup/authPage/hashToToken.js`](https://github.com/DougReeder/adaptive-backup/blob/b31117e3eba653ec385f7fc1c9989f9e099995a2/authPage/hashToToken.js) parses `location.hash`, and rs-backup's `https://rs-backup.5apps.com/` page displays the token for pasting. Doing it on loopback removes the manual paste.

     This could live in the backend or as a small generic `oauthutil` "fragment mode", which other implicit-only services might also use.
   - **c. Paste a token.** Always offered, and required for headless boxes and servers with no auth dialog. It mirrors webdav's `bearer_token`. `bearer_token_command` is worth copying too, for users who mint tokens elsewhere.
4. Save the token through `oauthutil.PutToken`, so expiry and refresh behave uniformly across all three paths.

### Scope choice

`*:rw` is what a backup or sync tool wants and is rs-backup's default (`authScope = category ? category+':rw' : '*:rw'`, [`backup.js` L36](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/backup.js#L36)). It is also the scope users should be most wary of granting.

Offer `module:rw`, and `module:r` for read-only mirrors, and pin the rclone root to `/<module>/`. Note that a module scope also implies `/public/<module>/` (§9). rs-backup exposes that as `--include-public`.

## 4. Gaps and gotchas

1. **ETag is opaque, not a hash.** The spec only says the ETag is a strong validator, e.g. "for instance a hash of its contents" (§4). Implementations vary:

   | Server | ETag value |
   |---|---|
   | armadietto classic | `Date.now()` millisecond timestamp ([`file_tree.js` L276-L285](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/stores/file_tree.js#L276-L285)) |
   | php-remote-storage | `<seq>:<random hex>` ([`MetadataStorage.php` L64-L72](https://git.sr.ht/~fkooman/php-remote-storage/tree/eb6ceccd7efebd1fcfb909b829bdf70d47acf7c4/item/src/MetadataStorage.php#L64-L72)) |
   | liquor-cabinet (5apps) | the S3 ETag ([`s3.rb` L28-L40](https://github.com/5apps/liquor-cabinet/blob/5efab8b73347013c45f7a78aa7ba0f14e8372093/lib/remote_storage/s3.rb#L28-L40)) |
   | armadietto modular | the S3 ETag |
   | RSilo | the R2/S3 ETag |

   S3 ETags are an MD5 only for single-part uploads. adaptive-backup still *assumes* MD5, defaulting `--etag-algorithm` to `md5` ([`restore.js` L34](https://github.com/DougReeder/adaptive-backup/blob/b31117e3eba653ec385f7fc1c9989f9e099995a2/restore.js#L34)). A generic backend must not.

   Possible future option: a `hash_from_etag = md5` opt-in, validated per server, would let `--checksum` work on S3-backed servers. Users can also wrap the remote in rclone's `hasher` backend.

2. **No settable modtime, and Last-Modified is version-dependent.** Last-Modified first appeared in -11; diffing the -00 to -27 texts on datatracker shows it absent before -11. Servers still on older drafts omit it:
   - armadietto classic writes only `ETag/Content-Type/Content-Length` (file_tree.js L280-L284). Its legacy controller advertises `draft-dejong-remotestorage-10` ([`controllers/web_finger.js` L80](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/controllers/web_finger.js#L80)).
   - php-remote-storage (`-05`) lists `Content-Length` but no Last-Modified ([`RemoteStorage.php` L86-L104](https://git.sr.ht/~fkooman/php-remote-storage/tree/eb6ceccd7efebd1fcfb909b829bdf70d47acf7c4/item/src/RemoteStorage.php#L86-L104)).
   - mysteryshack (`-05`) lists `Content-Length` and `ETag` only ([`models.rs` L505-L508](https://github.com/untitaker/mysteryshack/blob/7413ecc205568ac3ad0696411f6b9a55526546c6/src/mysteryshack/models.rs#L505-L508)).
   - liquor-cabinet ([`rest_provider.rb` L468-L506](https://github.com/5apps/liquor-cabinet/blob/5efab8b73347013c45f7a78aa7ba0f14e8372093/lib/remote_storage/rest_provider.rb#L468-L506)), armadietto modular and RSilo do include it.

   Either way it is the server's upload time. Sync therefore compares sizes only. Document `--update --use-server-modtime` for local→remote pushes ([`docs.md` L3105-L3119](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/docs/content/docs.md#L3105-L3119)), and `--checksum` via `hasher`, or `--ignore-times`, for correctness.

3. **No server-side copy or move.** `rclone move` within one remote means download, upload and delete. rs-backup's [`docs/copying-a-folder.md`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/docs/copying-a-folder.md) documents the same limitation. rclone would actually be a strict improvement there, since `rclone moveto` does the round-trip for you.
4. **Rate limits.** armadietto enforces about 315 authorised requests per second per instance and answers 429 with `Retry-After`. Others are unknown. Use pacer defaults, honour `Retry-After`, and keep `--checkers`/`--transfers` modest by default. The folder-per-request listing model makes deep trees chatty.
5. **Spec-version spread.**
   - Live servers advertise -05 (php-remote-storage, mysteryshack), -10 or -22 (armadietto, in its two server variants, [`routes/webfinger.js` L6](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/routes/webfinger.js#L6)), -13 (5apps) and -22 (RSilo).
   - Only -26 and later define PKCE.
   - remotestorage.js still special-cases -00, -01, -02 and the 2012 API ([`wireclient.ts` L61-L64](https://github.com/remotestorage/remotestorage.js/blob/d899b5aee41849fc94c45470cfad08a7363780b6/src/wireclient.ts#L61-L64)).
   - Decision: support `-02` and later only (the `items` + `@context` listing). Anything older is from 2013 and before.
6. **Maintenance of reference servers.** mysteryshack's last commit was 2018-03-18. php-remote-storage's last commit was 2024-02-06, its last release 1.0.5 (2016-11-10), and its GitHub home is gone (it now lives at `git.sr.ht/~fkooman`, per [remotestorage.io/servers](https://remotestorage.io/servers)). armadietto still says "Please do not consider `armadietto` production ready" ([`docker/README.md`](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/docker/README.md)).
7. **Token expiry without refresh.** With the implicit grant, an expired token (90 days on php-remote-storage) means re-running `rclone config reconnect`. On 401, the backend should say exactly that instead of retrying.
8. **Content-Type matters to apps.** remoteStorage apps read JSON documents by Content-Type. The 2015 `remotestorage/fuse` client's README warns it "destroys MIME types". rclone must set `WriteMimeType` and pass the source MIME type through. rclone's MIME type detection is extension-based, so verify that `.json` maps to `application/json`, and consider a per-remote override.
9. **CORS, Origin and browser assumptions.** CORS is irrelevant to a Go client, but some servers key behaviour on `Origin`. rs-backup started sending `Origin: https://rs-backup.5apps.com` "matching redirect URL" in v1.10.0 (PR #22, [`backup.js` L67-L73](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/backup.js#L67-L73)). Plan an `Origin` header equal to the client_id origin, behind an option.
10. **Upload size limits.** `413` is in the spec. RSilo caps uploads with `MAX_OBJECT_SIZE_BYTES` and buffers them. There is no way to discover the limit up front.

## 5. rs-backup in depth (and adaptive-backup)

`raucao/rs-backup` is the reference non-browser remoteStorage client. It is written by Sebastian Kippe, who co-authors draft-27.

| | |
|---|---|
| Licence | MIT ([`package.json`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/package.json)) |
| Runtime | Node ≥ 20.16, ESM. Dependencies: `commander`, `webfinger.js` v3, `open`, `picocolors`. |
| Maintenance | First commit 2015-12-12. Releases v1.8.0 (2021-09), v1.9.x (2023-04), v1.10.0 (2024-07), **v2.0.0 (2026-10-06)**, which modernised it, added a `node:test` suite and fixed `#` in folder names (#9/#25). Bursty but alive. |
| Repository location | The canonical repo is `github.com/raucao/rs-backup`, not under the `remotestorage` org. |

### Discovery and auth

- **Discovery.** WebFinger via webfinger.js with `tls_only: false, allow_private_addresses: true`, so LAN and localhost servers work. It takes the first remoteStorage link and reads `rfc6749#section-4.2` or the legacy `auth-endpoint` ([`discovery.js`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/discovery.js)).
- **Auth.** Implicit grant with `client_id: 'rs-backup.5apps.com'`, `redirect_uri: 'https://rs-backup.5apps.com/'` and `response_type: 'token'`. It opens the browser, then **asks the user to paste the token** (`rl.question('Authorization token: ')`, [`backup.js` L190-L199](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/backup.js#L190-L199)). There is no local listener.
- **The redirect page.** `https://rs-backup.5apps.com/` is a static hosted page that shows "Copy this token and paste it in your terminal". adaptive-backup ships the equivalent page source ([`authPage/hashToToken.js`](https://github.com/DougReeder/adaptive-backup/blob/b31117e3eba653ec385f7fc1c9989f9e099995a2/authPage/hashToToken.js)).
- **Scopes.** `*:rw`, or `<category>:rw` with `-c` (L36).
- **Unattended runs.** `-u user@host -t TOKEN` skips the prompts.
- **Reuse for rclone.** The *hosted-page + paste* pattern ports directly as a fallback, if rclone or a project site hosted such a page. Its benefit is a stable HTTPS `client_id` origin that consent screens display nicely, instead of `127.0.0.1:53682`. Its cost is a hosted point of failure that sees tokens, which is the objection raised in [spec issue #174](https://github.com/remotestorage/spec/issues/174). The loopback + JS page variant in §3 keeps everything local. Offer loopback by default and paste as the fallback.

### Traversal and transfer

- **Recursion.** Recursive `GET` of folders. Each folder's raw listing is saved to `<dir>/000_folder-description.json`, and children are fetched with `Promise.all` ([`backup.js` L112-L137](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/backup.js#L112-L137)).
- **Rate limiting.** A promise chain *serialises request starts* with a fixed gap: 20 ms for backup, 40 ms for restore ([`rate-limited.js`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/rate-limited.js)). AGENTS.md warns that wrapping the walk itself deadlocks.
- **ETags on backup.** No conditional requests. 304 is treated as success, but `If-None-Match` is never sent.
- **Retries.** Backup only, up to 3 tries 1 s apart, and only on network errors that match a regex (`ETIMEDOUT|socket hang up|…|ECONNRESET|ENOTFOUND`, L38-L40). There is **no handling of 429, 503 or Retry-After** and no retries on restore.
- **Restore.** Walks the saved `000_folder-description.json` files and PUTs each document with the saved `Content-Type` and `If-None-Match: "<saved ETag>"` ([`restore.js` L61-L69](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/restore.js#L61-L69)). The intent appears to be skipping documents whose version is unchanged; that is an inference. On a *different* server the old ETag never matches, so everything is written.
- **Metadata preserved.** Content-Type is preserved (the restore source of truth). ETags are kept only in the description files. Last-Modified cannot be restored, because the protocol does not allow it.
- **Binary vs JSON.** No distinction. Bodies are streamed byte-for-byte (`Readable.fromWeb` → file), and Content-Type comes from the listing, not from sniffing.
- **Encoding.** `encodeURIComponent(path).replace(/%2F/g,'/')` ([`encode-path.js`](https://github.com/raucao/rs-backup/blob/7cdbb10f5d0b85eb52da1c37a68b3159cf6b7e23/encode-path.js)). Past bugs: UTF-8 names (#11), `#` in names (#13, #9/#25).

### Limitations

- A backup **wipes the target directory first** (`fs.rmSync(backupDir, {recursive:true})`), so it is not incremental.
- Restore never deletes.
- No progress display for big files (issue #4, open since 2015).
- No parallelism control beyond the start gap.
- Whole-tree only. There is no filtering beyond one category.

### adaptive-backup

[`DougReeder/adaptive-backup`](https://github.com/DougReeder/adaptive-backup) is an MIT-licensed re-architecture whose backups are compatible with rs-backup. It adds:

- bounded concurrency
- **429/503 handling with Retry-After and adaptive back-off** ([`src/backupClass.js` L136-L170](https://github.com/DougReeder/adaptive-backup/blob/b31117e3eba653ec385f7fc1c9989f9e099995a2/src/backupClass.js#L136-L170))
- treating 412 on restore as "correct version already there" ([`src/restoreClass.js` L160-L215](https://github.com/DougReeder/adaptive-backup/blob/b31117e3eba653ec385f7fc1c9989f9e099995a2/src/restoreClass.js#L160-L215))

Its MD5-equals-ETag assumption is the server-specific shortcut noted in §4.1.

### What rclone would add

- Incremental sync and bisync instead of wipe-and-refetch.
- `mount`/`serve` (WebDAV, SFTP, S3) on top of a remoteStorage account.
- `crypt`. Encrypted data is unreadable to rS apps, so it suits archive-only use.
- Filters, `--max-age`, `check`, `ncdu` and `size`.
- Copying between remoteStorage and any of rclone's other 70+ backends: `ls backend` in v1.76.0 lists 71 directories.
- Mature pacer, retries and bandwidth limiting.
- The `rclone authorize` remote-setup flow.

### What rs-backup gets right, to copy

- Preserve **Content-Type** on every write.
- Accept the legacy WebFinger rels and properties, and allow `http://` and private addresses for self-hosted servers. Make this explicit in rclone, because Go's HTTP client has no "private address" guard by default.
- Make "token + user address on the command line" a first-class unattended path.
- Wrap only requests, never the walk, in the limiter. This maps to rclone's pacer.
- A useful extra (an idea): an rclone `backend` command that dumps folder descriptions in rs-backup's `000_folder-description.json` layout would make rclone backups restorable with rs-restore.

## 6. Testing plan

### How rclone runs it

- A backend gets `backend/remotestorage/remotestorage_test.go` calling `fstests.Run(t, &fstests.Opt{RemoteName: "TestRemoteStorage:", NilObject: (*Object)(nil)})`. The `Opt` fields are at [`fstests.go` L329-L346](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fstest/fstests/fstests.go#L329-L346), and webdav's test file is the template.
- The full matrix is `go run ./fstest/test_all -backends remotestorage`, after adding an entry to `fstest/test_all/config.yaml`. Merging requires a clean run *and* a test account for integration.rclone.org (CONTRIBUTING L666-L692).
- **Self-starting servers.** For each remote name, `fstest/testserver/init.d/<RemoteName>` is a script with `start/stop/status`. `start` prints `key=value` config lines plus `_connect=host:port`, and test_all runs it automatically ([`init.d/README.md`](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fstest/testserver/init.d/README.md)). [`TestWebdavNextcloud`](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/fstest/testserver/init.d/TestWebdavNextcloud) is a 25-line Docker example. Ports are allocated in `PORTS.md`.

### Server matrix

| Remote name | Server | Why |
|---|---|---|
| `TestRemoteStorageArmadietto:` | `remotestorage/armadietto-monolithic` Docker image, HTTP on :8000, `allow_signup: true` in its stock conf ([`docker/conf-monolithic/conf.json`](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/docker/conf-monolithic/conf.json)) | Oldest-style server: no Last-Modified, strict path regex, timestamp ETags. Token minting can be scripted with curl, because both forms are plain POSTs: `POST /signup` (`username,email,password`) and `POST /oauth` (`username,password,client_id,redirect_uri,response_type=token,scope=*:rw,allow`) → 302 `Location: …#access_token=…` ([`lib/armadietto.js` L188-L196](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/armadietto.js#L188-L196), [`controllers/oauth.js` L26-L45](https://github.com/remotestorage/armadietto/blob/3251697530f7110fa15129229911f2307687010f/lib/controllers/oauth.js#L26-L45)). The init script prints `storage_root=…` and `token={"access_token":"…"}`. |
| `TestRemoteStorageModern:` | A -22/-27 server with Last-Modified, Range and ideally PKCE. Candidates: armadietto modular (needs HTTPS and S3, so heavier), RSilo's offline dev server, or a 50-line Go harness around `elee1766/remotestorage`'s `rsserver.Handler` + `rstest.MemoryStorage` (targets -27 with ranges and conditional requests). | Covers the "new" code paths. |
| `TestRemoteStoragePHP:` (optional) | php-remote-storage via [`libresh/compose-remotestorage`](https://github.com/libresh/compose-remotestorage) | -05 legacy rel, `'P90D'` expiry quirk. |
| Manual, not CI | 5apps account | The only big hosted provider. Use it to check real-world limits and the client_id/redirect rules. |

### Repeatability notes

- Pin image digests.
- Mint a fresh user per run.
- Expect per-backend ignores. Start from webdav's `FsMkdir/FsEncoding/punctuation` and `invalid_UTF-8`.
- Run `rclone test info` per server and commit the CSVs, so quirks are documented, not folklore.
- Add an in-process unit test against a Go `httptest` fake (or the elee1766 `rstest` memory server) for discovery parsing, legacy rels, flat listings, 412/409/507 mapping and fragment capture.
- Gotcha: the elee1766 module declares `go 1.27.2` in its go.mod, while rclone is on `go 1.26.0` ([`go.mod` L3](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/go.mod#L3)). As a dependency it is blocked for now, though it remains usable as an out-of-tree test server binary.
- Also worth running the remoteStorage project's own [`api-test-suite`](https://github.com/remotestorage/api-test-suite) against each server, to separate server bugs from backend bugs.

## 7. Upstream vs fork vs plugin

- **Upstream.** This is the best outcome for users. Blockers:
  - a sponsor-provided **test account** the rclone maintainers can keep, otherwise "a backend that we cannot test is likely to break and may be removed" (CONTRIBUTING L687-L691). A small hosted armadietto/RSilo instance or a 5apps account would do, or the Docker init.d server alone might be accepted.
  - the oauthutil changes (fragment capture and the PKCE verifier) need to be argued generically, not as remoteStorage hacks.
  - a niche protocol needs a committed maintainer.
- **Go plugin.** rclone supports out-of-tree backends as `librcloneplugin_backend_<name>.so` loaded from `$RCLONE_PLUGIN_PATH` ([CONTRIBUTING L728-L760](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/CONTRIBUTING.md#L728-L760)). Limits:
  - Linux and macOS only ([`lib/plugin/plugin.go` L1](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/lib/plugin/plugin.go#L1)).
  - It must be built against the *exact* rclone source and Go version.
  - Go's `plugin` package needs cgo, while `bin/cross-compile.go` builds with `CGO_ENABLED=0` unless `-cgo` is passed ([L310-L315](https://github.com/rclone/rclone/blob/0c9826d3440def5cc10c163ca8ccf46dce6c0b1e/bin/cross-compile.go#L310-L315)). In practice a plugin therefore needs a self-built rclone, which removes most of its advantage over a fork.
- **Fork or custom build.** Add `backend/remotestorage` plus one import line in `backend/all/all.go` to a tagged rclone and publish binaries, e.g. a flake output. This is cheap to rebase because backends are isolated. It is the realistic path while the backend matures.
- **Suggested sequence.**
  1. Build in a fork with paste-token auth plus the armadietto init.d server, and get fstests green.
  2. Add WebFinger and loopback implicit capture.
  3. Open an rclone issue or forum thread proposing the backend and the oauthutil fragment and PKCE hooks.
  4. Submit a PR with test_all results.

### Prior art

As of 2026-10-18:

- **rclone.** There is no remoteStorage backend, issue or forum thread. `gh search issues remotestorage --repo rclone/rclone` finds only an unrelated issue, a forum.rclone.org search finds nothing relevant, and the source tree has no matches apart from a variable name in `cmd/gitannex`. The only mention is a remoteStorage-forum wish: "It would be amazing to plug this into rclone" ([community #967](https://community.remotestorage.io/t/bind-remotestorage-server-backed-by-git/967)).
- **Other non-browser clients.**

  | Client | Language | Notes |
  |---|---|---|
  | rs-backup | Node | see §5 |
  | adaptive-backup | Node | see §5 |
  | [`remotestorage/fuse`](https://github.com/remotestorage/fuse) | C | 2015, abandoned. Same Mkdir/Rmdir no-op semantics as proposed here. |
  | [`elee1766/remotestorage`](https://github.com/elee1766/remotestorage) | Go | Client and server library with -27 PKCE helpers and a conformance suite. Created 2025-11, 3 stars, no licence file. |
  | [`shun2580/rstools`](https://github.com/shun2580/rstools) | Go | CLI targeting -22. Uses dynamic client registration, which is non-standard for rS. Single-day project, no licence. |

## 8. Open questions

1. ~~Do 5apps, armadietto and php-remote-storage accept `redirect_uri=http://127.0.0.1:53682/` with `client_id=http://127.0.0.1:53682`?~~ **Answered by [the loopback spike](../spikes/loopback-implicit-grant.md) (2026-10-19):** yes for armadietto (tested end to end), and for php-remote-storage, mysteryshack and RSilo (per source). 5apps also accepts it (manual run with a real account, 2026-10-19), so no rclone-hosted HTTPS page is needed.
2. Would rclone maintainers accept a generic "implicit/fragment" mode and a PKCE verifier hook in `oauthutil`, or must the backend own its own listener?
3. Exactly which characters fail on each server? Run `rclone test info` against all of them, then decide the default encoder and the escaping function (aggressive `%`-encoding including `~`).
4. Should `Update` use `If-Match` by default? This is safer alongside live apps, but a 412 mid-sync needs a clear message and probably `--remotestorage-conditional-writes=false` as an escape hatch.
5. Is an opt-in `hash_from_etag=md5` worth shipping for S3-backed servers (5apps, armadietto modular, RSilo), given multipart-upload ETags break it?
6. How should `/public/` work with a module scope? Options: expose it as a sibling remote path, a `public` flag, or ignore it.
7. Will the remoteStorage spec move toward Client ID Metadata Documents ([spec #202](https://github.com/remotestorage/spec/issues/202)) or token exchange ([spec #201](https://github.com/remotestorage/spec/issues/201))? Either would change how a CLI identifies itself, so watch before freezing the config UX.
8. Who hosts the long-lived test account that upstream requires?
