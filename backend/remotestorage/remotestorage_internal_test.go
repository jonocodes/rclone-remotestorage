package remotestorage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/operations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEscapePath(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"a/b.txt":          "a/b.txt",
		"a b?/c d@+~%.txt": "a%20b%3F/c%20d%40%2B%7E%25.txt",
		"Hello, 世界":        "Hello%2C%20%E4%B8%96%E7%95%8C",
		"x-y_z.0":          "x-y_z.0",
	} {
		assert.Equal(t, want, escapePath(in), in)
	}
}

func TestUnquoteETag(t *testing.T) {
	assert.Equal(t, "abc", unquoteETag(`"abc"`))
	assert.Equal(t, "abc", unquoteETag(`W/"abc"`))
	assert.Equal(t, "123", unquoteETag("123"))
}

// newTestFs returns an Fs talking to handler as its storage root
func newTestFs(t *testing.T, handler http.HandlerFunc, extra configmap.Simple) *Fs {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	m := configmap.Simple{}
	ri, err := fs.Find("remotestorage")
	require.NoError(t, err)
	for _, o := range ri.Options {
		if o.Default != nil {
			m[o.Name] = fmt.Sprint(o.Default)
		}
	}
	m["storage_root"] = srv.URL + "/storage/alice"
	m["token"] = "tok"
	for k, v := range extra {
		m[k] = v
	}
	f, err := NewFs(context.Background(), "test", "", m)
	require.NoError(t, err)
	return f.(*Fs)
}

func TestListArmadiettoKeys(t *testing.T) {
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		assert.Equal(t, "/storage/alice/t/", r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/ld+json")
		// folder key as sent, document key through decodeURI
		_, _ = io.WriteString(w, `{"@context":"http://remotestorage.io/spec/folder-description","items":{
			"a%20b%3F/":{"ETag":"1"},
			"c d%40%2B.txt":{"ETag":"2","Content-Type":"text/plain","Content-Length":5,"Last-Modified":"Tue, 20 Oct 2026 00:52:09 GMT"}}}`)
	}, configmap.Simple{"vendor": "armadietto"})

	entries, err := f.List(context.Background(), "t")
	require.NoError(t, err)
	got := map[string]fs.DirEntry{}
	for _, e := range entries {
		got[e.Remote()] = e
	}
	require.Contains(t, got, "t/a b?")
	require.Contains(t, got, "t/c d@+.txt")
	o := got["t/c d@+.txt"].(*Object)
	assert.Equal(t, int64(5), o.Size())
	assert.Equal(t, "text/plain", o.MimeType(context.Background()))
	assert.Equal(t, "2", o.ID())
	assert.Equal(t, 2026, o.ModTime(context.Background()).Year())
}

func TestListEmptyIsDirNotFound(t *testing.T) {
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"@context":"http://remotestorage.io/spec/folder-description","items":{}}`)
	}, nil)
	_, err := f.List(context.Background(), "nope")
	assert.ErrorIs(t, err, fs.ErrorDirNotFound)
	// the storage root always exists
	entries, err := f.List(context.Background(), "")
	assert.NoError(t, err)
	assert.Empty(t, entries)
	// Mkdir'd folders exist until removed
	require.NoError(t, f.Mkdir(context.Background(), "made/sub"))
	_, err = f.List(context.Background(), "made")
	assert.NoError(t, err)
	assert.NoError(t, f.Rmdir(context.Background(), "made/sub"))
	assert.NoError(t, f.Rmdir(context.Background(), "made"))
	assert.ErrorIs(t, f.Rmdir(context.Background(), "made"), fs.ErrorDirNotFound)
}

// A token's scope names categories, so the storage root and categories exist
// even when empty; any deeper root must be found like any other folder
func TestListEmptyRoot(t *testing.T) {
	for root, exists := range map[string]bool{
		"":                 true,
		"documents":        true,
		"public/documents": true,
		"documents/sub":    false,
		"a/b/notfound":     false,
	} {
		f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"@context":"http://remotestorage.io/spec/folder-description","items":{}}`)
		}, configmap.Simple{})
		g, err := NewFs(context.Background(), "test", root, configmap.Simple{"storage_root": f.opt.StorageRoot, "token": "tok"})
		require.NoError(t, err, root)
		_, err = g.List(context.Background(), "")
		if exists {
			assert.NoError(t, err, root)
		} else {
			assert.ErrorIs(t, err, fs.ErrorDirNotFound, root)
		}
	}
}

func TestOpenEmulatesIgnoredRange(t *testing.T) {
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = io.WriteString(w, "0123456789") // ignores Range
	}, nil)
	o := &Object{fs: f, remote: "doc", hasMetaData: true, size: 10}
	for _, tc := range []struct {
		opt  fs.OpenOption
		want string
	}{
		{&fs.RangeOption{Start: 2, End: 4}, "234"},
		{&fs.RangeOption{Start: -1, End: 3}, "789"},
		{&fs.SeekOption{Offset: 7}, "789"},
	} {
		rc, err := o.Open(context.Background(), tc.opt)
		require.NoError(t, err)
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.Equal(t, tc.want, string(b), tc.opt.String())
	}
}

func TestConditionalWriteConflict(t *testing.T) {
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, `"old"`, r.Header.Get("If-Match"))
		w.WriteHeader(http.StatusPreconditionFailed)
	}, nil)
	o := &Object{fs: f, remote: "doc", hasMetaData: true, size: 1, etag: "old"}
	src := &fakeInfo{remote: "doc", size: 3}
	err := o.Update(context.Background(), strings.NewReader("new"), src)
	require.Error(t, err)
	assert.True(t, fserrors.IsNoRetryError(err), "412 must not be retried")
	assert.Contains(t, err.Error(), "conflict")
	assert.Equal(t, http.StatusPreconditionFailed, statusCode(err))
}

func TestRemoveRetryAfterDelete(t *testing.T) {
	deletes := 0
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		deletes++
		if deletes == 1 {
			// deleted, but the response was lost
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}, nil)
	o := &Object{fs: f, remote: "doc", hasMetaData: true, size: 1, etag: "old"}
	require.NoError(t, o.Remove(context.Background()))
	assert.Equal(t, 2, deletes)

	// without a retry a 404 still means the object wasn't there
	deletes = 1
	assert.ErrorIs(t, o.Remove(context.Background()), fs.ErrorObjectNotFound)
}

func TestConditionalWritesOff(t *testing.T) {
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			assert.Empty(t, r.Header.Get("If-Match"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusCreated)
		case http.MethodHead:
			w.Header().Set("Content-Length", "3")
			w.Header().Set("ETag", `"new"`)
		}
	}, configmap.Simple{"conditional_writes": "false"})
	o := &Object{fs: f, remote: "doc.json", hasMetaData: true, size: 1, etag: "old"}
	err := o.Update(context.Background(), strings.NewReader("new"), &fakeInfo{remote: "doc.json", size: 3})
	require.NoError(t, err)
	assert.Equal(t, "new", o.etag)
	assert.Equal(t, int64(3), o.size)
}

// Overwriting through a mount uploads from the VFS cache, whose files have no
// type of their own. App documents usually have no extension, so a guess from
// the name would turn application/json into application/octet-stream.
func TestUpdateKeepsContentType(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  fs.ObjectInfo
		want string
	}{
		{"guess from name", &fakeInfo{remote: "note", size: 3}, "application/json; charset=UTF-8"},
		{"source has a type", &fakeMimeInfo{fakeInfo{remote: "note", size: 3}, "text/plain"}, "text/plain"},
		// an editor's temp file renamed over the document got a guessed type
		{"source type unknown", &fakeMimeInfo{fakeInfo{remote: "note", size: 3}, "application/octet-stream"}, "application/json; charset=UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPut:
					assert.Equal(t, tc.want, r.Header.Get("Content-Type"))
					w.WriteHeader(http.StatusCreated)
				case http.MethodHead:
					w.Header().Set("Content-Length", "3")
					w.Header().Set("Content-Type", tc.want)
				}
			}, nil)
			o := &Object{fs: f, remote: "note", hasMetaData: true, size: 1, etag: "old", mimeType: "application/json; charset=UTF-8"}
			require.NoError(t, o.Update(context.Background(), strings.NewReader("new"), tc.src))
		})
	}
}

// memDoc is a document held by memServer
type memDoc struct{ body, contentType, etag string }

// memServer is an in-memory storage root honouring If-Match on PUT and DELETE
func memServer(t *testing.T, docs map[string]*memDoc) http.HandlerFunc {
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/storage/alice/")
		d := docs[p]
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if d == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", d.contentType)
			w.Header().Set("Content-Length", fmt.Sprint(len(d.body)))
			w.Header().Set("ETag", `"`+d.etag+`"`)
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, d.body)
			}
		case http.MethodPut, http.MethodDelete:
			if m := r.Header.Get("If-Match"); m != "" && (d == nil || m != `"`+d.etag+`"`) {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			if r.Method == http.MethodDelete {
				delete(docs, p)
				return
			}
			body, _ := io.ReadAll(r.Body)
			n++
			docs[p] = &memDoc{string(body), r.Header.Get("Content-Type"), fmt.Sprint("v", n)}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}
}

// The protocol has no copy, but rclone's VFS can't rename without one, so
// Copy exists only to decline: rclone then copies by download and upload,
// which keeps the source's type and overwrites with If-Match
func TestMoveByDownloadUpload(t *testing.T) {
	ctx := context.Background()
	docs := map[string]*memDoc{
		"d/src":      {`{"n":1}`, "application/json", "s1"},
		"d/src2":     {`{"n":2}`, "application/json", "s2"},
		"d/existing": {"old", "text/plain", "e1"},
	}
	f := newTestFs(t, memServer(t, docs), nil)
	src, err := f.NewObject(ctx, "d/src")
	require.NoError(t, err)
	_, err = f.Copy(ctx, src, "d/x")
	assert.ErrorIs(t, err, fs.ErrorCantCopy)

	_, err = operations.Move(ctx, f, nil, "d/new", src)
	require.NoError(t, err)
	assert.NotContains(t, docs, "d/src")
	require.Contains(t, docs, "d/new")
	assert.Equal(t, `{"n":1}`, docs["d/new"].body)
	assert.Equal(t, "application/json", docs["d/new"].contentType)

	src2, err := f.NewObject(ctx, "d/src2")
	require.NoError(t, err)
	existing, err := f.NewObject(ctx, "d/existing")
	require.NoError(t, err)
	_, err = operations.Move(ctx, f, existing, "d/existing", src2)
	require.NoError(t, err)
	assert.Equal(t, `{"n":2}`, docs["d/existing"].body)
	assert.Equal(t, "application/json", docs["d/existing"].contentType)
}

// The VFS may upload a temp file renamed over a document as a new file (Put),
// and the name alone says nothing about an extensionless app document's type
func TestPutKeepsExistingContentType(t *testing.T) {
	ctx := context.Background()
	docs := map[string]*memDoc{"d/note": {`{"n":1}`, "application/json", "e1"}}
	f := newTestFs(t, memServer(t, docs), nil)
	for remote, want := range map[string]string{
		"d/note":     "application/json",
		"d/fresh":    "application/octet-stream",
		"d/text.txt": "text/plain; charset=utf-8",
	} {
		_, err := f.Put(ctx, strings.NewReader("new"), &fakeInfo{remote: remote, size: 3})
		require.NoError(t, err, remote)
		assert.Equal(t, want, docs[remote].contentType, remote)
	}
}

// fakeMimeInfo is a fakeInfo whose source knows its type
type fakeMimeInfo struct {
	fakeInfo
	mimeType string
}

func (i *fakeMimeInfo) MimeType(context.Context) string { return i.mimeType }

// fakeInfo is a minimal fs.ObjectInfo for uploads
type fakeInfo struct {
	fs.ObjectInfo
	remote string
	size   int64
}

func (i *fakeInfo) Remote() string                                  { return i.remote }
func (i *fakeInfo) Size() int64                                     { return i.size }
func (i *fakeInfo) Fs() fs.Info                                     { return nil }
func (i *fakeInfo) String() string                                  { return i.remote }
func (i *fakeInfo) Hash(context.Context, hash.Type) (string, error) { return "", nil }

func TestLenientUnescape(t *testing.T) {
	for in, want := range map[string]string{
		"a%20b%3F":     "a b?",
		"100%":         "100%",
		"%%26":         "%&",
		"%zz%2B":       "%zz+",
		"%E4%B8%96":    "世",
		"plain-name.x": "plain-name.x",
	} {
		assert.Equal(t, want, lenientUnescape(in, nil), in)
	}
}

func TestArmadiettoName(t *testing.T) {
	// key as armadietto lists it for a name we sent via escapePath
	for name, key := range map[string]string{
		"test%46.txt":  "test%46.txt", // decodeURI("test%2546.txt")
		"a b@+&.txt":   "a b%40%2B%26.txt",
		"100%":         "100%",
		"%&":           "%%26",
		"dir%46 x/":    "dir%2546%20x/", // folders are listed as sent
		"Hello, 世界/":   "Hello%2C%20%E4%B8%96%E7%95%8C/",
		"Hello, 世界.md": "Hello%2C 世界.md",
	} {
		assert.Equal(t, name, armadiettoName(key), key)
	}
}

// A category-scoped token (scope "rclone-test:rw") makes 5apps answer 401 to
// the root-is-a-file probe HEAD /rclone-test; the remote must still open.
func TestNewFsScopedRootProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/storage/alice/rclone-test/":
			_, _ = io.WriteString(w, `{"@context":"http://remotestorage.io/spec/folder-description","items":{"a.txt":{"ETag":"1","Content-Type":"text/plain","Content-Length":1}}}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(srv.Close)
	m := configmap.Simple{"storage_root": srv.URL + "/storage/alice", "token": "tok"}
	f, err := NewFs(context.Background(), "test", "rclone-test", m)
	require.NoError(t, err)
	assert.Equal(t, "rclone-test", f.Root())
	entries, err := f.List(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}
