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
		io.WriteString(w, `{"@context":"http://remotestorage.io/spec/folder-description","items":{
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
		io.WriteString(w, `{"@context":"http://remotestorage.io/spec/folder-description","items":{}}`)
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

func TestOpenEmulatesIgnoredRange(t *testing.T) {
	f := newTestFs(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		io.WriteString(w, "0123456789") // ignores Range
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
