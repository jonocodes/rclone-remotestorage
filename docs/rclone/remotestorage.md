---
title: "remoteStorage"
description: "Rclone docs for remoteStorage"
versionIntroduced: "v1.76"
---

# remoteStorage

[remoteStorage](https://remotestorage.io) is an open protocol for
per-user storage that web apps keep their data in. It is specified as
an IETF Internet-Draft,
[draft-dejong-remotestorage](https://datatracker.ietf.org/doc/draft-dejong-remotestorage/).
Accounts are offered by hosted providers such as [5apps](https://5apps.com/storage)
and by self-hosted servers such as
[armadietto](https://github.com/remotestorage/armadietto) and
[RSilo](https://github.com/jonocodes/RSilo).

Paths are specified as `remote:path`

Paths may be as deep as required, e.g. `remote:directory/subdirectory`.

The top level of an account holds one folder per category (`documents`,
`notes`, `contacts`, …), each shared by the apps which use that kind of
data, plus `public/` for documents anyone can read.

## Configuration

A remoteStorage account is identified by a user address like
`alice@5apps.com`. rclone looks the address up with WebFinger, then
logs in with your web browser.

Here is an example of making a remoteStorage configuration. First run

```console
rclone config
```

This will guide you through an interactive setup process:

```text
No remotes found, make a new one?
n) New remote
s) Set configuration password
q) Quit config
n/s/q> n

Enter name for new remote.
name> remote

Option Storage.
Type of storage to configure.
Choose a number from below, or type in your own value.
[snip]
XX / remoteStorage (remotestorage.io)
   \ (remotestorage)
[snip]
Storage> remotestorage

Option user_address.
Your remoteStorage address, like alice@5apps.com.
rclone looks the account up with WebFinger and logs in with your web
browser. Leave blank to enter the storage root and a token by hand.
Enter a value. Press Enter to leave empty.
user_address> alice@5apps.com

Edit advanced config?
y) Yes
n) No (default)
y/n> n

NOTICE: Found remoteStorage for alice@5apps.com: https://storage.5apps.com/alice (draft-dejong-remotestorage-13)
Use web browser to automatically authenticate rclone with remote?
 * Say Y if the machine running rclone has a web browser you can use
 * Say N if running rclone on a (remote) machine without web browser access
If not sure try Y. If Y failed, try N.

y) Yes (default)
n) No
y/n> y

NOTICE: If your browser doesn't open automatically go to the following link: https://5apps.com/rs/oauth/alice?...
NOTICE: Log in and authorize rclone for access
NOTICE: Waiting for the browser...

Configuration complete.
Options:
- type: remotestorage
- user_address: alice@5apps.com
- storage_root: https://storage.5apps.com/alice
- auth_url: https://5apps.com/rs/oauth/alice
- token: XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
Keep this "remote" remote?
y) Yes this is OK (default)
e) Edit this remote
d) Delete this remote
y/e/d> y
```

The server shows a consent page listing the access rclone asks for.
Once you allow it, the browser hands the token to rclone on a local
listener at `http://127.0.0.1:53682/`, which may require you to
unblock it if you are using a host firewall.

remoteStorage servers only offer the OAuth implicit grant, so tokens
can't be refreshed. When a token expires or is revoked, log in again
with `rclone config reconnect remote:`.

Once configured you can then use `rclone` like this (replace `remote`
with the name you gave your remote):

List the top level folders

```console
rclone lsd remote:
```

List all the files in the `documents` category

```console
rclone ls remote:documents
```

Copy a local directory to `documents/notes`

```console
rclone copy /home/source remote:documents/notes
```

### Configuring without a web browser

If the machine running rclone has no web browser, answer `n` to "Use
web browser…". rclone prints an `rclone authorize "remotestorage" "…"`
command to run on a machine which does have one; paste its output
back. See the [remote setup docs](/remote_setup/) for more on this.

You can also paste a bearer token at that prompt instead, if you
already have one for the account.

To configure a remote without WebFinger or a browser at all, leave
`user_address` empty and give the storage root and token directly:

```console
rclone config create remote remotestorage storage_root=https://example.com/storage/alice token=XXX
```

The storage root is the `href` of the remoteStorage link in the
server's WebFinger response for the account.

### Limiting access to one category

By default rclone asks for `*:rw`, read and write access to the whole
account. Set the advanced `scope` option to a category, e.g.
`documents:rw` or `documents:r`, to give rclone access to that folder
only (and its `public/` counterpart). This is a good idea when trying
rclone on an account which holds data your apps depend on.

With a category scope, use paths inside that category, e.g.
`remote:documents/notes`. Anything outside it fails with a "path is
outside the token's scope" error.

### Modification times and hashes

remoteStorage does not support setting modification times. If the
server sends a `Last-Modified` header rclone shows that, which is the
time the document was last uploaded, otherwise it shows
`--default-time`.

The protocol has no hashes either. Each document has an ETag, but it is
an opaque version string, not a checksum. `rclone sync` and `rclone
check` therefore compare sizes only, so a local change which keeps a
file's size is not copied.

When copying or syncing from local disk to a server which sends
`Last-Modified`, use [`--update`](/docs/#u-update) to fix this. rclone
then uploads every file changed locally since its last upload,
whatever its size.

### Content types

Web apps rely on the `Content-Type` stored with each document, so rclone
preserves it in both directions. When rclone uploads a file it uses the
source's MIME type if it has one, otherwise one guessed from the file
name. When overwriting a document, rclone keeps the document's existing
`Content-Type` unless the source has a more specific one than
`application/octet-stream`. This means editing an app's document
without a file extension, e.g. through `rclone mount`, does not change
its type.

### Conflicts with apps

Apps can change documents at any time. When rclone overwrites or
deletes a document it sends `If-Match` with the ETag it last saw, so
if an app has changed the document in the meantime the operation fails
with a conflict error instead of losing the app's change. Run the
command again to pick up the new version.

Set `conditional_writes = false` to overwrite regardless.

Uploading a file which doesn't exist yet is not protected: if an app
creates a document at the same path first, rclone replaces it.

### Empty directories

remoteStorage folders only exist while they contain documents, so
empty directories can't be stored. `rclone mkdir` succeeds, but the
directory only appears on the server once something is written inside
it.

### Server-side copy and move

The protocol has no copy or move, so rclone copies by downloading and
re-uploading, and moves by copying then deleting. This includes renames
in `rclone mount`.

### Mounting

The recommended options for `rclone mount` are

```console
rclone mount remote: /path/to/mountpoint --vfs-cache-mode writes --dir-cache-time 30s
```

Changes made by apps show up once the directory cache expires, so set
`--dir-cache-time` to how long you are prepared to wait.

Prefer `--vfs-cache-mode writes` over `full`. With `full`, rclone keeps
read data in its cache and decides whether a document changed by
comparing size, modification time and hash. remoteStorage only provides
the size, so if an app changes a document without changing its size
the mount keeps showing the old contents. With `writes`, files are read
from the server each time they are opened.

The [conflict protection](#conflicts-with-apps) only covers a short
window in a mount. If a write is refused with a conflict the mount
retries it, and once its directory cache expires the retry is made
against the app's new version and succeeds, overwriting it.

### Restricted filename characters

In addition to the [default restricted characters set](/overview/#restricted-characters)
the names `.` and `..` are replaced, as the protocol doesn't allow
them.

Invalid UTF-8 bytes will also be [replaced](/overview/#invalid-utf8),
as they can't be used in JSON strings.

<!-- autogenerated options start - DO NOT EDIT - instead edit fs.RegInfo in backend/remotestorage/remotestorage.go and run make backenddocs to verify --> <!-- markdownlint-disable-line line-length -->
### Standard options

Here are the Standard options specific to remotestorage (remoteStorage (remotestorage.io)).

#### --remotestorage-user-address

Your remoteStorage address, like alice@5apps.com.

rclone looks the account up with WebFinger and logs in with your web
browser. Leave blank to enter the storage root and a token by hand.

Properties:

- Config:      user_address
- Env Var:     RCLONE_REMOTESTORAGE_USER_ADDRESS
- Type:        string
- Required:    false

### Advanced options

Here are the Advanced options specific to remotestorage (remoteStorage (remotestorage.io)).

#### --remotestorage-scope

OAuth scope to ask for when logging in.

"*:rw" gives access to the whole storage. A category scope such as
"documents:rw" limits rclone to documents/ (and public/documents/).

Properties:

- Config:      scope
- Env Var:     RCLONE_REMOTESTORAGE_SCOPE
- Type:        string
- Default:     "*:rw"

#### --remotestorage-storage-root

Storage root URL of the account.

Found with WebFinger when logging in. This is the "href" of the
remoteStorage link in the server's WebFinger response, e.g.
https://storage.5apps.com/alice or https://example.com/storage/alice.

Properties:

- Config:      storage_root
- Env Var:     RCLONE_REMOTESTORAGE_STORAGE_ROOT
- Type:        string
- Required:    false

#### --remotestorage-auth-url

OAuth authorization endpoint, found with WebFinger when logging in.

Properties:

- Config:      auth_url
- Env Var:     RCLONE_REMOTESTORAGE_AUTH_URL
- Type:        string
- Required:    false

#### --remotestorage-token

OAuth bearer token for the account.

Set by logging in. Its scope decides what the remote can reach.

Properties:

- Config:      token
- Env Var:     RCLONE_REMOTESTORAGE_TOKEN
- Type:        string
- Required:    false

#### --remotestorage-conditional-writes

Use If-Match on overwrite and delete.

remoteStorage apps may change documents at any time. With this set, rclone
only overwrites or deletes a document if it is unchanged since rclone last
read its metadata; otherwise the operation fails with a conflict instead of
losing the app's write.

Properties:

- Config:      conditional_writes
- Env Var:     RCLONE_REMOTESTORAGE_CONDITIONAL_WRITES
- Type:        bool
- Default:     true

#### --remotestorage-vendor

Server software, to work around its known quirks.

Properties:

- Config:      vendor
- Env Var:     RCLONE_REMOTESTORAGE_VENDOR
- Type:        string
- Default:     "other"
- Examples:
  - "other"
    - Spec-compliant server (5apps, RSilo, …)
  - "armadietto"
    - armadietto: folder listing keys come back percent-encoded

#### --remotestorage-encoding

The encoding for the backend.

See the [encoding section in the overview](/overview/#encoding) for more info.

Properties:

- Config:      encoding
- Env Var:     RCLONE_REMOTESTORAGE_ENCODING
- Type:        Encoding
- Default:     Slash,InvalidUtf8,Dot

#### --remotestorage-description

Description of the remote.

Properties:

- Config:      description
- Env Var:     RCLONE_REMOTESTORAGE_DESCRIPTION
- Type:        string
- Required:    false

<!-- autogenerated options stop -->

## Limitations

Documents can't be larger than the server allows. A document which is
too large fails with a "document too large for this server" error, and
running out of quota with a "storage quota exceeded" error.

Some servers save documents on disk under their percent-encoded names,
so a name has to fit the filesystem's limit, often 255 bytes, after
encoding. Each character outside ASCII takes 6 to 12 bytes encoded, so
names in scripts such as Chinese or Burmese can be limited to around 28
characters. armadietto is affected.

## Providers

### 5apps

[5apps](https://5apps.com/storage) offers hosted remoteStorage accounts.
Use your 5apps user address, e.g. `alice@5apps.com`, as described in
[Configuration](#configuration).

### armadietto

[armadietto](https://github.com/remotestorage/armadietto) is a
self-hosted remoteStorage server in Node.js. Its folder listings return
document names percent-encoded, and its `HEAD` responses have no
`Content-Length`. Set the advanced `vendor` option to `armadietto` to
work around both. With it set, names which literally contain `%XX`
sequences can't be told apart from encoded ones.

### RSilo

[RSilo](https://github.com/jonocodes/RSilo) is a self-hosted
remoteStorage server which stores documents in S3-compatible storage
or on local disk. It needs no special options.

### Other servers

Other servers implementing draft-dejong-remotestorage should work with
the default options. rclone has been tested against servers speaking
versions 10 (armadietto), 13 (5apps) and 22 (RSilo) of the draft.
