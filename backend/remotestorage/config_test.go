package remotestorage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLinks(t *testing.T) {
	for name, tc := range map[string]struct {
		links   string
		root    string
		auth    string
		version string
	}{
		"draft rel and properties": {
			links:   `[{"rel":"http://tools.ietf.org/id/draft-dejong-remotestorage","href":"https://s.example/alice","properties":{"http://remotestorage.io/spec/version":"draft-dejong-remotestorage-13","http://tools.ietf.org/html/rfc6749#section-4.2":"https://example/rs/oauth/alice"}}]`,
			root:    "https://s.example/alice",
			auth:    "https://example/rs/oauth/alice",
			version: "draft-dejong-remotestorage-13",
		},
		"legacy rel, auth-endpoint, version from type": {
			links:   `[{"rel":"lrdd","href":"x"},{"rel":"remoteStorage","href":"https://s.example/bob","type":"draft-dejong-remotestorage-03","properties":{"auth-endpoint":"https://example/oauth/bob"}}]`,
			root:    "https://s.example/bob",
			auth:    "https://example/oauth/bob",
			version: "draft-dejong-remotestorage-03",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var links []jrdLink
			require.NoError(t, json.Unmarshal([]byte(tc.links), &links))
			d, err := parseLinks(links)
			require.NoError(t, err)
			assert.Equal(t, tc.root, d.StorageRoot)
			assert.Equal(t, tc.auth, d.AuthURL)
			assert.Equal(t, tc.version, d.Version)
		})
	}
	_, err := parseLinks([]jrdLink{{Rel: "remotestorage", Href: "https://s.example/x"}})
	assert.Error(t, err, "no auth endpoint")
}

func TestParseFragment(t *testing.T) {
	assert.Equal(t, "tok", parseFragment("access_token=tok&token_type=bearer&state=s1", "s1").token)
	assert.ErrorContains(t, parseFragment("access_token=tok&state=other", "s1").err, "wrong state")
	assert.ErrorContains(t, parseFragment("error=access_denied&error_description=nope&state=s1", "s1").err, "access_denied")
	assert.ErrorContains(t, parseFragment("state=s1", "s1").err, "no access_token")
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:18001": true, "localhost": true, "[::1]:80": true,
		"5apps.com": false, "10.0.0.1:80": false,
	} {
		assert.Equal(t, want, isLoopbackHost(host), host)
	}
}

// fakeProvider serves WebFinger and an OAuth endpoint which grants at once
// (as if the user approved), redirecting with the token in the fragment
func fakeProvider(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/webfinger":
			assert.Equal(t, "acct:alice@"+r.Host, r.URL.Query().Get("resource"))
			_, _ = io.WriteString(w, `{"links":[{"rel":"http://tools.ietf.org/id/draft-dejong-remotestorage","href":"`+srv.URL+`/storage/alice","properties":{"http://remotestorage.io/spec/version":"draft-dejong-remotestorage-22","http://tools.ietf.org/html/rfc6749#section-4.2":"`+srv.URL+`/oauth/alice"}}]}`)
		case "/oauth/alice":
			q := r.URL.Query()
			assert.Equal(t, "token", q.Get("response_type"))
			assert.Equal(t, "documents:rw", q.Get("scope"))
			cid, rdr := q.Get("client_id"), q.Get("redirect_uri")
			assert.Equal(t, cid+"/", rdr, "redirect_uri must be client_id origin plus /")
			http.Redirect(w, r, rdr+"#access_token=minted&token_type=bearer&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeBrowser does what a browser plus loginPage's JS do: follow the auth
// URL to the redirect, then POST the fragment back to the loopback origin
func fakeBrowser(t *testing.T) func(string) error {
	return func(authURL string) error {
		go func() {
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Get(authURL)
			if !assert.NoError(t, err) {
				return
			}
			_ = resp.Body.Close()
			loc, err := url.Parse(resp.Header.Get("Location"))
			if !assert.NoError(t, err) {
				return
			}
			page, err := http.Get(loc.Scheme + "://" + loc.Host + "/")
			if assert.NoError(t, err) {
				b, _ := io.ReadAll(page.Body)
				_ = page.Body.Close()
				assert.Contains(t, string(b), "location.hash")
			}
			resp, err = http.Post(loc.Scheme+"://"+loc.Host+"/fragment", "application/x-www-form-urlencoded", strings.NewReader(loc.Fragment))
			if assert.NoError(t, err) {
				_ = resp.Body.Close()
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			}
		}()
		return nil
	}
}

// runConfig drives Config like rclone's config UI, answering each question
// from answers (keyed by option name) or with its default
func runConfig(t *testing.T, m configmap.Simple, answers map[string]string) {
	t.Helper()
	in := fs.ConfigIn{}
	for range 20 {
		out, err := Config(context.Background(), "test", m, in)
		require.NoError(t, err)
		if out == nil {
			return
		}
		require.Empty(t, out.Error)
		in = fs.ConfigIn{State: out.State}
		if out.Option != nil {
			answer, ok := answers[out.Option.Name]
			if !ok {
				answer = fmt.Sprint(out.Option.Default)
			}
			in.Result = answer
		}
		if out.State == "" {
			return
		}
	}
	t.Fatal("config did not finish")
}

func TestConfigBrowserLogin(t *testing.T) {
	srv := fakeProvider(t)
	old := openURL
	openURL = fakeBrowser(t)
	t.Cleanup(func() { openURL = old })

	host := strings.TrimPrefix(srv.URL, "http://")
	m := configmap.Simple{"user_address": "alice@" + host, "scope": "documents:rw"}
	runConfig(t, m, nil) // config_is_local defaults to yes
	assert.Equal(t, srv.URL+"/storage/alice", m["storage_root"])
	assert.Equal(t, srv.URL+"/oauth/alice", m["auth_url"])
	assert.Equal(t, "minted", m["token"])

	// reconnect: declining to replace keeps the token
	m["token"] = "kept"
	runConfig(t, m, map[string]string{"config_refresh_token": "false"})
	assert.Equal(t, "kept", m["token"])
}

func TestConfigHeadless(t *testing.T) {
	srv := fakeProvider(t)
	host := strings.TrimPrefix(srv.URL, "http://")

	// the browser machine: rclone authorize "remotestorage" <blob>
	old := openURL
	openURL = fakeBrowser(t)
	t.Cleanup(func() { openURL = old })
	authM := configmap.Simple{config.ConfigAuthorize: "true", "user_address": "alice@" + host, "scope": "documents:rw"}
	runConfig(t, authM, nil)
	delete(authM, config.ConfigAuthorize)
	blob, err := authM.Encode()
	require.NoError(t, err)

	// the headless machine: say no to the browser, paste the blob
	m := configmap.Simple{"user_address": "alice@" + host, "scope": "documents:rw"}
	runConfig(t, m, map[string]string{"config_is_local": "false", "config_token": blob})
	assert.Equal(t, "minted", m["token"])
	assert.Equal(t, srv.URL+"/storage/alice", m["storage_root"])

	// ...or paste a bare token
	m = configmap.Simple{"user_address": "alice@" + host}
	runConfig(t, m, map[string]string{"config_is_local": "false", "config_token": " pasted\n"})
	assert.Equal(t, "pasted", m["token"])
}

func TestConfigManual(t *testing.T) {
	m := configmap.Simple{}
	runConfig(t, m, map[string]string{"storage_root": "https://example.com/storage/alice", "token": "tok"})
	assert.Equal(t, "https://example.com/storage/alice", m["storage_root"])
	assert.Equal(t, "tok", m["token"])
	// already configured by hand: nothing asked
	runConfig(t, m, map[string]string{})
	assert.Equal(t, "tok", m["token"])
}
