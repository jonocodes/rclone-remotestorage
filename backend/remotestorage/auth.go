package remotestorage

// WebFinger discovery and the browser login (OAuth implicit grant captured on
// a loopback listener). See docs/spikes/loopback-implicit-grant.md.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fshttp"
)

const (
	relDraft = "http://tools.ietf.org/id/draft-dejong-remotestorage"
	// WebFinger link properties (draft-dejong-remotestorage §10)
	propVersion  = "http://remotestorage.io/spec/version"
	propImplicit = "http://tools.ietf.org/html/rfc6749#section-4.2"
	// loopbackPort is the port rclone's oauthutil uses for its redirect
	loopbackPort = 53682
	loginTimeout = 5 * time.Minute
)

// discovery is what WebFinger tells us about an account
type discovery struct {
	StorageRoot string
	AuthURL     string
	Version     string
}

type jrdLink struct {
	Rel        string         `json:"rel"`
	Href       string         `json:"href"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}

// splitAddress splits user@host, where host may carry a port
func splitAddress(address string) (user, host string, err error) {
	at := strings.LastIndex(address, "@")
	if at < 1 || at == len(address)-1 {
		return "", "", fmt.Errorf("%q is not a remoteStorage address like alice@example.com", address)
	}
	return address[:at], address[at+1:], nil
}

// isLoopbackHost reports whether host (maybe with port) is this machine,
// where plain-http WebFinger is acceptable (local test servers)
func isLoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// discover looks up address (user@host) with WebFinger
func discover(ctx context.Context, address string) (*discovery, error) {
	_, host, err := splitAddress(address)
	if err != nil {
		return nil, err
	}
	schemes := []string{"https"}
	if isLoopbackHost(host) {
		schemes = append(schemes, "http")
	}
	client := fshttp.NewClient(ctx)
	var lastErr error
	for _, scheme := range schemes {
		u := fmt.Sprintf("%s://%s/.well-known/webfinger?resource=%s", scheme, host, url.QueryEscape("acct:"+address))
		d, err := discoverURL(ctx, client, u)
		if err == nil {
			return d, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func discoverURL(ctx context.Context, client *http.Client, u string) (*discovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/jrd+json, application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webfinger: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webfinger %s: %s", u, resp.Status)
	}
	var jrd struct {
		Links []jrdLink `json:"links"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jrd); err != nil {
		return nil, fmt.Errorf("webfinger %s: %w", u, err)
	}
	return parseLinks(jrd.Links)
}

// parseLinks picks the remoteStorage link, accepting the legacy rels and
// properties real clients (remotestorage.js, rs-backup) tolerate
func parseLinks(links []jrdLink) (*discovery, error) {
	for _, l := range links {
		if l.Rel != relDraft && !strings.EqualFold(l.Rel, "remotestorage") {
			continue
		}
		d := &discovery{StorageRoot: l.Href, Version: l.Type}
		if v, ok := l.Properties[propVersion].(string); ok && v != "" {
			d.Version = v
		}
		for _, k := range []string{propImplicit, "auth-endpoint"} {
			if v, ok := l.Properties[k].(string); ok && v != "" {
				d.AuthURL = v
				break
			}
		}
		if d.StorageRoot != "" && d.AuthURL != "" {
			return d, nil
		}
	}
	return nil, errors.New("webfinger: no remoteStorage link with a storage root and an OAuth endpoint")
}

// loginPage runs in the browser at the redirect URI. The token arrives in
// the URL fragment, which never reaches a server, so post it back.
const loginPage = `<!doctype html><meta charset="utf-8"><title>rclone remoteStorage login</title>
<p id="m">Completing authorization…</p>
<script>
fetch("/fragment", {method: "POST", headers: {"Content-Type": "application/x-www-form-urlencoded"}, body: location.hash.slice(1)})
  .then(r => r.text()).then(t => document.getElementById("m").textContent = t)
  .catch(e => document.getElementById("m").textContent = "Failed: " + e);
</script>`

type grantResult struct {
	token string
	err   error
}

// loginRequest is a pending browser login
type loginRequest struct {
	// AuthURL is the URL the user must open
	AuthURL string
	result  chan grantResult
	srv     *http.Server
}

// startLogin starts a loopback listener and returns the authorization URL
// to open. client_id is the listener's origin and redirect_uri that origin
// plus "/" (same origin, no query: php-remote-storage and RSilo require it).
func startLogin(authURL, scope string) (*loginRequest, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", loopbackPort))
	if err != nil {
		// port taken (another login?): any free port works, the origin
		// just has to match client_id
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("failed to start login listener: %w", err)
		}
	}
	origin := "http://" + ln.Addr().String()
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		_ = ln.Close()
		return nil, err
	}
	state := hex.EncodeToString(stateBytes)

	lr := &loginRequest{result: make(chan grantResult, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, loginPage)
	})
	mux.HandleFunc("POST /fragment", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		g := parseFragment(string(body), state)
		if g.err != nil {
			http.Error(w, g.err.Error(), http.StatusBadRequest)
		} else {
			_, _ = io.WriteString(w, "Authorized. You can close this window and return to rclone.")
		}
		select {
		case lr.result <- g:
		default:
		}
	})
	lr.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = lr.srv.Serve(ln) }()

	q := url.Values{
		"client_id":     {origin},
		"redirect_uri":  {origin + "/"},
		"response_type": {"token"},
		"scope":         {scope},
		"state":         {state},
	}
	sep := "?"
	if strings.Contains(authURL, "?") {
		sep = "&"
	}
	lr.AuthURL = authURL + sep + q.Encode()
	return lr, nil
}

// parseFragment checks the redirect fragment posted back by loginPage
func parseFragment(fragment, wantState string) grantResult {
	v, err := url.ParseQuery(fragment)
	switch {
	case err != nil:
		return grantResult{err: fmt.Errorf("bad authorization response: %w", err)}
	case v.Get("error") != "":
		return grantResult{err: fmt.Errorf("server refused authorization: %s %s", v.Get("error"), v.Get("error_description"))}
	case v.Get("state") != wantState:
		return grantResult{err: errors.New("authorization response has the wrong state")}
	case v.Get("access_token") == "":
		return grantResult{err: errors.New("authorization response has no access_token")}
	}
	return grantResult{token: v.Get("access_token")}
}

// Wait for the browser to deliver the token, then stop the listener
func (lr *loginRequest) Wait(ctx context.Context) (string, error) {
	defer func() { _ = lr.srv.Close() }()
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	select {
	case g := <-lr.result:
		return g.token, g.err
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for the browser login: %w", ctx.Err())
	}
}

// login runs the whole browser flow and returns the token
func login(ctx context.Context, authURL, scope string, openBrowser bool) (string, error) {
	lr, err := startLogin(authURL, scope)
	if err != nil {
		return "", err
	}
	if openBrowser {
		fs.Logf(nil, "If your browser doesn't open automatically go to the following link: %s", lr.AuthURL)
		if err := openURL(lr.AuthURL); err != nil {
			fs.Logf(nil, "Failed to open browser automatically (%v) - please go to the link above", err)
		}
	} else {
		fs.Logf(nil, "Please go to the following link: %s", lr.AuthURL)
	}
	fs.Logf(nil, "Log in and authorize rclone for access")
	fs.Logf(nil, "Waiting for the browser...")
	return lr.Wait(ctx)
}
