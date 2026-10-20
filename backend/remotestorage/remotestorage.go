// Package remotestorage provides an interface to servers speaking the
// remoteStorage protocol (draft-dejong-remotestorage), e.g. 5apps,
// armadietto, php-remote-storage, mysteryshack or RSilo.
//
// The protocol is a stripped-down WebDAV: documents are read and written with
// GET/HEAD/PUT/DELETE, and a GET on a path ending in "/" returns a JSON-LD
// listing. Folders exist only while they contain documents, there is no
// copy/move, no checksum and no way to set a modification time.
package remotestorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonocodes/rclone-remotestorage/backend/remotestorage/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/fs/hash"
	fslog "github.com/rclone/rclone/fs/log"
	"github.com/rclone/rclone/lib/encoder"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/readers"
	"github.com/rclone/rclone/lib/rest"
)

const (
	minSleep      = 10 * time.Millisecond
	maxSleep      = 2 * time.Second
	decayConstant = 2
)

// Register with Fs
func init() {
	// Out of tree there is no embedded docs/data/backends/remotestorage.yaml,
	// so fs.Register logs "no overview data found" on every run. Mute it.
	// Drop this once the backend lives in rclone itself.
	fs.SetLogger(slog.NewTextHandler(io.Discard, nil))
	defer fs.SetLogger(fslog.Handler)
	fs.Register(&fs.RegInfo{
		Name:        "remotestorage",
		Description: "remoteStorage (remotestorage.io)",
		NewFs:       NewFs,
		Config:      Config,
		Options: []fs.Option{{
			Name: "user_address",
			Help: `Your remoteStorage address, like alice@5apps.com.

rclone looks the account up with WebFinger and logs in with your web
browser. Leave blank to enter the storage root and a token by hand.`,
			Sensitive: true,
		}, {
			Name: "scope",
			Help: `OAuth scope to ask for when logging in.

"*:rw" gives access to the whole storage. A category scope such as
"documents:rw" limits rclone to documents/ (and public/documents/).`,
			Default:  "*:rw",
			Advanced: true,
		}, {
			Name: "storage_root",
			Help: `Storage root URL of the account.

Found with WebFinger when logging in. This is the "href" of the
remoteStorage link in the server's WebFinger response, e.g.
https://storage.5apps.com/alice or https://example.com/storage/alice.`,
			Advanced: true,
		}, {
			Name:     "auth_url",
			Help:     "OAuth authorization endpoint, found with WebFinger when logging in.",
			Advanced: true,
		}, {
			Name: "token",
			Help: `OAuth bearer token for the account.

Set by logging in. Its scope decides what the remote can reach.`,
			Advanced:  true,
			Sensitive: true,
		}, {
			Name: "conditional_writes",
			Help: `Use If-Match on overwrite and delete.

remoteStorage apps may change documents at any time. With this set, rclone
only overwrites or deletes a document if it is unchanged since rclone last
read its metadata; otherwise the operation fails with a conflict instead of
losing the app's write.`,
			Default:  true,
			Advanced: true,
		}, {
			Name: "vendor",
			Help: `Server software, to work around its known quirks.`,
			Examples: []fs.OptionExample{{
				Value: "other",
				Help:  "Spec-compliant server (5apps, php-remote-storage, mysteryshack, RSilo, …)",
			}, {
				Value: "armadietto",
				Help:  "armadietto: folder listing keys come back percent-encoded",
			}},
			Default:  "other",
			Advanced: true,
		}, {
			Name:     config.ConfigEncoding,
			Help:     config.ConfigEncodingHelp,
			Advanced: true,
			// Item names may contain anything except "/" and NUL and must not
			// be "." or ".." (draft-dejong-remotestorage §4). Listing keys are
			// JSON strings, so invalid UTF-8 must be encoded too.
			Default: encoder.Base | encoder.EncodeInvalidUtf8,
		}},
	})
}

// Options defines the configuration for this backend
type Options struct {
	UserAddress       string               `config:"user_address"`
	Scope             string               `config:"scope"`
	StorageRoot       string               `config:"storage_root"`
	AuthURL           string               `config:"auth_url"`
	Token             string               `config:"token"`
	ConditionalWrites bool                 `config:"conditional_writes"`
	Vendor            string               `config:"vendor"`
	Enc               encoder.MultiEncoder `config:"encoding"`
}

// Fs represents a remote remoteStorage account
type Fs struct {
	name     string
	root     string // path under the storage root, no leading or trailing /
	opt      Options
	features *fs.Features
	endpoint string // storage root URL ending in /
	srv      *rest.Client
	pacer    *fs.Pacer
}

// Object describes a remoteStorage document
type Object struct {
	fs          *Fs
	remote      string
	hasMetaData bool
	size        int64
	modTime     time.Time
	etag        string // unquoted
	mimeType    string
}

// ------------------------------------------------------------
// Folders which exist without documents
//
// remoteStorage folders exist only while they contain documents, but rclone
// expects Mkdir'd folders to be removable with Rmdir and nonexistent ones to
// report an error. Remember the folders this process has made or seen, keyed
// by full URL so every Fs on the same account agrees.

var knownDirs = struct {
	sync.Mutex
	m map[string]struct{}
}{m: map[string]struct{}{}}

func (f *Fs) markDir(dir string, withParents bool) {
	knownDirs.Lock()
	defer knownDirs.Unlock()
	for {
		knownDirs.m[f.dirURL(dir)] = struct{}{}
		if !withParents || dir == "" {
			return
		}
		dir = parentDir(dir)
	}
}

func (f *Fs) forgetDir(dir string) (known bool) {
	knownDirs.Lock()
	defer knownDirs.Unlock()
	key := f.dirURL(dir)
	_, known = knownDirs.m[key]
	delete(knownDirs.m, key)
	return known
}

func (f *Fs) isKnownDir(dir string) bool {
	knownDirs.Lock()
	defer knownDirs.Unlock()
	_, known := knownDirs.m[f.dirURL(dir)]
	return known
}

func parentDir(dir string) string {
	p := path.Dir(dir)
	if p == "." || p == "/" {
		return ""
	}
	return p
}

// ------------------------------------------------------------

// Name of the remote (as passed into NewFs)
func (f *Fs) Name() string { return f.name }

// Root of the remote (as passed into NewFs)
func (f *Fs) Root() string { return f.root }

// String converts this Fs to a string
func (f *Fs) String() string { return fmt.Sprintf("remoteStorage root '%s'", f.root) }

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features { return f.features }

// Precision of the remote: modification times can't be set
func (f *Fs) Precision() time.Duration { return fs.ModTimeNotSupported }

// Hashes returns the supported hash types: ETags are opaque, not checksums
func (f *Fs) Hashes() hash.Set { return hash.Set(hash.None) }

// escapePath percent-encodes everything except unreserved characters and /.
//
// This is stricter than RFC 3986 (it encodes "~" too) because armadietto
// validates raw request paths against /^\/([a-z0-9%.\-_]+\/?)*$/i.
func escapePath(p string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// lenientUnescape decodes valid %XX sequences for which keep returns true
// (all of them if keep is nil) and leaves any other % as is
func lenientUnescape(s string, keep func(byte) bool) string {
	unhex := func(c byte) (byte, bool) {
		switch {
		case '0' <= c && c <= '9':
			return c - '0', true
		case 'a' <= c && c <= 'f':
			return c - 'a' + 10, true
		case 'A' <= c && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if c := hi<<4 | lo; ok1 && ok2 && (keep == nil || keep(c)) {
				b.WriteByte(c)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// decodeURIReserved reports whether JS decodeURI leaves c percent-encoded
func decodeURIReserved(c byte) bool { return strings.IndexByte(";/?:@&=+$,#", c) >= 0 }

// armadiettoName recovers the item name from an armadietto listing key.
// Folder keys are exactly what the client sent, so decode fully. Document
// keys went through decodeURI, so the only escapes left that it made are
// reserved characters; any other %XX is literal text in the name.
func armadiettoName(key string) string {
	if strings.HasSuffix(key, "/") {
		return lenientUnescape(key, nil)
	}
	return lenientUnescape(key, decodeURIReserved)
}

// filePath returns the escaped path of remote relative to the storage root
func (f *Fs) filePath(remote string) string {
	return escapePath(f.opt.Enc.FromStandardPath(path.Join(f.root, remote)))
}

// dirPath returns the escaped folder path of dir relative to the storage root,
// ending in / unless it is the storage root itself
func (f *Fs) dirPath(dir string) string {
	p := f.filePath(dir)
	if p != "" {
		p += "/"
	}
	return p
}

func (f *Fs) dirURL(dir string) string { return f.endpoint + f.dirPath(dir) }

// retryErrorCodes is a slice of error codes that we will retry
var retryErrorCodes = []int{
	429, // Too Many Requests
	500, // Internal Server Error
	502, // Bad Gateway
	503, // Service Unavailable
	504, // Gateway Timeout
	509, // Bandwidth Limit Exceeded
}

// shouldRetry returns a boolean as to whether this resp and err deserve to be
// retried. It returns the err as a convenience.
func (f *Fs) shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if fserrors.ContextError(ctx, &err) {
		return false, err
	}
	if resp != nil && (resp.StatusCode == 429 || resp.StatusCode == 503) {
		if secs, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && secs > 0 {
			return true, fserrors.NewErrorRetryAfter(time.Duration(secs) * time.Second)
		}
	}
	return fserrors.ShouldRetry(err) || fserrors.ShouldRetryHTTP(resp, retryErrorCodes), err
}

// errorHandler turns non-2xx responses into errors, explaining the ones a
// user can act on and marking those that retrying can't fix
func errorHandler(resp *http.Response) error {
	body, _ := rest.ReadBody(resp)
	e := &api.Error{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Message:    string(body),
	}
	if resp.Request != nil {
		e.Method = resp.Request.Method
		e.Path = resp.Request.URL.Path
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fserrors.NoRetryError(fmt.Errorf("token rejected or expired, get a new one with \"rclone config reconnect\" or update the remote's token: %w", e))
	case http.StatusForbidden:
		return fserrors.NoRetryError(fmt.Errorf("path is outside the token's scope: %w", e))
	case http.StatusPreconditionFailed:
		return fserrors.NoRetryError(fmt.Errorf("document was changed on the server since it was read (conflict); re-run to pick up the new version, or set conditional_writes = false to overwrite: %w", e))
	case http.StatusRequestEntityTooLarge:
		return fserrors.NoRetryError(fmt.Errorf("document too large for this server: %w", e))
	case http.StatusInsufficientStorage:
		return fserrors.NoRetryError(fmt.Errorf("storage quota exceeded: %w", e))
	}
	return e
}

func statusCode(err error) int {
	var e *api.Error
	if errors.As(err, &e) {
		return e.StatusCode
	}
	return 0
}

// NewFs constructs an Fs from the path, container:path
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	opt := new(Options)
	if err := configstruct.Set(m, opt); err != nil {
		return nil, err
	}
	if opt.StorageRoot == "" || opt.Token == "" {
		return nil, errors.New("remote is not logged in (no storage_root or token): run \"rclone config reconnect " + name + ":\"")
	}
	u, err := url.Parse(opt.StorageRoot)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("storage_root %q must be an http(s) URL", opt.StorageRoot)
	}
	endpoint := strings.TrimSuffix(u.String(), "/") + "/"

	rootIsDir := strings.HasSuffix(root, "/")
	root = strings.Trim(root, "/")

	f := &Fs{
		name:     name,
		root:     root,
		opt:      *opt,
		endpoint: endpoint,
		srv:      rest.NewClient(fshttp.NewClient(ctx)).SetRoot(endpoint).SetErrorHandler(errorHandler),
		pacer:    fs.NewPacer(ctx, pacer.NewDefault(pacer.MinSleep(minSleep), pacer.MaxSleep(maxSleep), pacer.DecayConstant(decayConstant))),
	}
	f.srv.SetHeader("Authorization", "Bearer "+opt.Token)
	f.features = (&fs.Features{
		CanHaveEmptyDirectories: false,
		ReadMimeType:            true,
		WriteMimeType:           true,
	}).Fill(ctx, f)

	if root != "" && !rootIsDir {
		// Check to see if the root is actually an existing document
		remote := path.Base(root)
		f.root = parentDir(root)
		_, err := f.NewObject(ctx, remote)
		if err != nil {
			if errors.Is(err, fs.ErrorObjectNotFound) || errors.Is(err, fs.ErrorIsDir) {
				f.root = root
				return f, nil
			}
			return nil, err
		}
		return f, fs.ErrorIsFile
	}
	return f, nil
}

// readDir returns the listing of dir; a missing or empty folder is
// fs.ErrorDirNotFound because the protocol can't tell them apart
func (f *Fs) readDir(ctx context.Context, dir string) (*api.Listing, error) {
	opts := rest.Opts{
		Method:       "GET",
		Path:         f.dirPath(dir),
		ExtraHeaders: map[string]string{"Accept": "application/ld+json, application/json"},
	}
	var listing api.Listing
	var resp *http.Response
	err := f.pacer.Call(func() (bool, error) {
		var err error
		resp, err = f.srv.CallJSON(ctx, &opts, nil, &listing)
		return f.shouldRetry(ctx, resp, err)
	})
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, fs.ErrorDirNotFound
		}
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
			return nil, fmt.Errorf("folder listing of %q is not a draft-02+ remoteStorage listing: %w", dir, err)
		}
		return nil, err
	}
	if len(listing.Items) == 0 {
		return nil, fs.ErrorDirNotFound
	}
	if f.opt.Vendor == "armadietto" {
		// armadietto lists folder names as sent (percent-encoded) and
		// document names through JS decodeURI, which leaves reserved
		// characters (;/?:@&=+$,#) encoded. See armadiettoName.
		items := make(map[string]api.Item, len(listing.Items))
		for key, item := range listing.Items {
			items[armadiettoName(key)] = item
		}
		listing.Items = items
	}
	return &listing, nil
}

// List the objects and directories in dir into entries
func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	listing, err := f.readDir(ctx, dir)
	if errors.Is(err, fs.ErrorDirNotFound) && (f.isKnownDir(dir) || f.dirPath(dir) == "") {
		// the storage root always exists
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.markDir(dir, true)
	for key, item := range listing.Items {
		name, isDir := strings.CutSuffix(key, "/")
		if name == "" {
			continue
		}
		remote := path.Join(dir, f.opt.Enc.ToStandardName(name))
		if isDir {
			f.markDir(remote, false)
			entries = append(entries, fs.NewDir(remote, time.Time{}).SetID(item.ETag))
			continue
		}
		o := &Object{fs: f, remote: remote}
		o.setFromItem(&item)
		entries = append(entries, o)
	}
	return entries, nil
}

// NewObject finds the Object at remote. If it can't be found it returns the
// error fs.ErrorObjectNotFound.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	o := &Object{fs: f, remote: remote}
	if err := o.readMetaData(ctx); err != nil {
		return nil, err
	}
	return o, nil
}

// Put the object. Ancestor folders are created implicitly by the server.
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	o := &Object{fs: f, remote: src.Remote()}
	return o, o.upload(ctx, in, src, options...)
}

// PutStream uploads an object of indeterminate size (chunked transfer
// encoding, which servers MUST support per §4)
func (f *Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.Put(ctx, in, src, options...)
}

// Mkdir records dir as existing: folders can't exist empty on the server and
// appear once a document is written below them
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	f.markDir(dir, true)
	return nil
}

// Rmdir removes an empty directory. It is an error if it contains documents,
// or if it neither exists on the server nor was made/seen by this process.
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	_, err := f.readDir(ctx, dir)
	if err == nil {
		return fs.ErrorDirectoryNotEmpty
	}
	if !errors.Is(err, fs.ErrorDirNotFound) {
		return err
	}
	if !f.forgetDir(dir) {
		return fs.ErrorDirNotFound
	}
	return nil
}

// ------------------------------------------------------------

// Fs returns the parent Fs
func (o *Object) Fs() fs.Info { return o.fs }

// String returns a description of the Object
func (o *Object) String() string {
	if o == nil {
		return "<nil>"
	}
	return o.remote
}

// Remote returns the remote path
func (o *Object) Remote() string { return o.remote }

// Hash is unsupported: the protocol has no checksums
func (o *Object) Hash(ctx context.Context, t hash.Type) (string, error) {
	return "", hash.ErrUnsupported
}

// Size returns the size of an object in bytes
func (o *Object) Size() int64 { return o.size }

// ModTime returns the server's Last-Modified (its upload time) when it sends
// one, which drafts before -11 don't; otherwise --default-time
func (o *Object) ModTime(ctx context.Context) time.Time {
	if o.modTime.IsZero() {
		return time.Time(fs.GetConfig(ctx).DefaultTime)
	}
	return o.modTime
}

// SetModTime is not possible in the protocol
func (o *Object) SetModTime(ctx context.Context, modTime time.Time) error {
	return fs.ErrorCantSetModTime
}

// Storable returns whether this object is storable
func (o *Object) Storable() bool { return true }

// MimeType returns the document's Content-Type
func (o *Object) MimeType(ctx context.Context) string { return o.mimeType }

// ID returns the document's ETag
func (o *Object) ID() string { return o.etag }

func unquoteETag(etag string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(etag), "W/"), `"`)
}

func (o *Object) setFromItem(item *api.Item) {
	o.hasMetaData = true
	o.etag = unquoteETag(item.ETag)
	o.mimeType = item.ContentType
	o.size = -1
	if item.ContentLength != nil {
		o.size = *item.ContentLength
	}
	o.modTime = time.Time{}
	if t, err := http.ParseTime(item.LastModified); err == nil {
		o.modTime = t
	}
}

func (o *Object) setFromHeader(h http.Header, contentLength int64) {
	o.hasMetaData = true
	o.etag = unquoteETag(h.Get("ETag"))
	o.mimeType = h.Get("Content-Type")
	o.size = contentLength
	o.modTime = time.Time{}
	if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
		o.modTime = t
	}
}

// readMetaData fetches the document's metadata with HEAD, falling back to
// the parent listing for servers without HEAD (it is only a SHOULD)
func (o *Object) readMetaData(ctx context.Context) error {
	if o.hasMetaData {
		return nil
	}
	if o.fs.opt.Vendor == "armadietto" {
		// its HEAD responses carry no Content-Length
		return o.readMetaDataFromParent(ctx)
	}
	opts := rest.Opts{Method: "HEAD", Path: o.fs.filePath(o.remote), NoResponse: true}
	var resp *http.Response
	err := o.fs.pacer.Call(func() (bool, error) {
		var err error
		resp, err = o.fs.srv.Call(ctx, &opts)
		return o.fs.shouldRetry(ctx, resp, err)
	})
	switch code := statusCode(err); {
	case err == nil && resp.ContentLength >= 0:
		o.setFromHeader(resp.Header, resp.ContentLength)
		return nil
	case code == http.StatusNotFound || code == http.StatusConflict:
		return fs.ErrorObjectNotFound
	case err != nil && code != http.StatusMethodNotAllowed && code != http.StatusNotImplemented:
		return err
	}
	return o.readMetaDataFromParent(ctx)
}

func (o *Object) readMetaDataFromParent(ctx context.Context) error {
	dir, leaf := parentDir(o.remote), path.Base(o.remote)
	listing, err := o.fs.readDir(ctx, dir)
	if errors.Is(err, fs.ErrorDirNotFound) {
		return fs.ErrorObjectNotFound
	}
	if err != nil {
		return err
	}
	name := o.fs.opt.Enc.FromStandardName(leaf)
	if item, ok := listing.Items[name]; ok {
		o.setFromItem(&item)
		return nil
	}
	if _, ok := listing.Items[name+"/"]; ok {
		return fs.ErrorIsDir
	}
	return fs.ErrorObjectNotFound
}

// Open an object for read. Range support is only a MAY, so a server which
// ignores the Range header has the unwanted bytes discarded here.
func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	fs.FixRangeOption(options, o.size)
	var offset, limit int64 = 0, -1
	for _, option := range options {
		switch x := option.(type) {
		case *fs.RangeOption:
			offset, limit = x.Decode(o.size)
		case *fs.SeekOption:
			offset, limit = x.Offset, -1
		default:
			if option.Mandatory() {
				fs.Logf(o, "Unsupported mandatory option: %v", option)
			}
		}
	}
	opts := rest.Opts{Method: "GET", Path: o.fs.filePath(o.remote), Options: options}
	var resp *http.Response
	err := o.fs.pacer.Call(func() (bool, error) {
		var err error
		resp, err = o.fs.srv.Call(ctx, &opts)
		return o.fs.shouldRetry(ctx, resp, err)
	})
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, fs.ErrorObjectNotFound
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusPartialContent || (offset == 0 && limit < 0) {
		return resp.Body, nil
	}
	// Server ignored the Range: skip to offset and stop after limit
	if _, err := io.CopyN(io.Discard, resp.Body, offset); err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("skipping to offset %d: %w", offset, err)
	}
	if limit >= 0 {
		return readers.NewLimitedReadCloser(resp.Body, limit), nil
	}
	return resp.Body, nil
}

// Update the object with the contents of the io.Reader
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	return o.upload(ctx, in, src, options...)
}

// conditionalHeaders returns If-Match for the last known ETag, if enabled
func (o *Object) conditionalHeaders() map[string]string {
	if !o.fs.opt.ConditionalWrites || o.etag == "" {
		return nil
	}
	return map[string]string{"If-Match": `"` + o.etag + `"`}
}

func (o *Object) upload(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	contentType := fs.MimeType(ctx, src)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	opts := rest.Opts{
		Method:       "PUT",
		Path:         o.fs.filePath(o.remote),
		Body:         in,
		ContentType:  contentType,
		ExtraHeaders: o.conditionalHeaders(),
		Options:      options,
		NoResponse:   true,
	}
	if size := src.Size(); size >= 0 {
		opts.ContentLength = &size
	}
	var resp *http.Response
	err := o.fs.pacer.CallNoRetry(func() (bool, error) {
		var err error
		resp, err = o.fs.srv.Call(ctx, &opts)
		return o.fs.shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return err
	}
	// Re-read so size, ETag and Last-Modified are the server's
	o.hasMetaData = false
	return o.readMetaData(ctx)
}

// Remove an object
func (o *Object) Remove(ctx context.Context) error {
	opts := rest.Opts{
		Method:       "DELETE",
		Path:         o.fs.filePath(o.remote),
		ExtraHeaders: o.conditionalHeaders(),
		NoResponse:   true,
	}
	err := o.fs.pacer.Call(func() (bool, error) {
		resp, err := o.fs.srv.Call(ctx, &opts)
		return o.fs.shouldRetry(ctx, resp, err)
	})
	if statusCode(err) == http.StatusNotFound {
		return fs.ErrorObjectNotFound
	}
	return err
}

// Check the interfaces are satisfied
var (
	_ fs.Fs          = (*Fs)(nil)
	_ fs.PutStreamer = (*Fs)(nil)
	_ fs.Object      = (*Object)(nil)
	_ fs.MimeTyper   = (*Object)(nil)
	_ fs.IDer        = (*Object)(nil)
)
