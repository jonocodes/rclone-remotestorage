// Command loopback is a throwaway spike answering open question 1 of
// docs/research/rclone-remotestorage-backend.md: can a CLI obtain a
// remoteStorage token via the implicit grant by redirecting to a loopback
// listener, given the token arrives in the URL fragment (which never reaches
// the server)?
//
// Flow: WebFinger discovery -> listen on 127.0.0.1:PORT -> print the auth URL
// -> the browser lands on http://127.0.0.1:PORT/#access_token=... -> a tiny
// page reads location.hash and POSTs it back -> verify the token with a GET on
// the storage root.
//
// Machine-readable lines on stdout are prefixed "AUTH_URL " and "RESULT ".
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	relDraft    = "http://tools.ietf.org/id/draft-dejong-remotestorage"
	propVersion = "http://remotestorage.io/spec/version"
	propAuth    = "http://tools.ietf.org/html/rfc6749#section-4.2"
)

type link struct {
	Rel        string         `json:"rel"`
	Href       string         `json:"href"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}

type discovery struct {
	StorageRoot string `json:"storage_root"`
	AuthURL     string `json:"auth_url"`
	Version     string `json:"spec_version"`
}

func discover(ctx context.Context, address string, insecure bool) (*discovery, error) {
	at := strings.LastIndex(address, "@")
	if at < 1 {
		return nil, fmt.Errorf("address %q is not user@host", address)
	}
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	u := fmt.Sprintf("%s://%s/.well-known/webfinger?resource=%s",
		scheme, address[at+1:], url.QueryEscape("acct:"+address))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webfinger: %s", resp.Status)
	}
	var jrd struct {
		Links []link `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jrd); err != nil {
		return nil, fmt.Errorf("webfinger: %w", err)
	}
	for _, l := range jrd.Links {
		// Accept the draft URI plus the legacy rels real clients tolerate.
		if l.Rel != relDraft && !strings.EqualFold(l.Rel, "remotestorage") {
			continue
		}
		d := &discovery{StorageRoot: l.Href, Version: l.Type}
		if v, ok := l.Properties[propVersion].(string); ok {
			d.Version = v
		}
		for _, k := range []string{propAuth, "auth-endpoint"} {
			if v, ok := l.Properties[k].(string); ok && v != "" {
				d.AuthURL = v
				break
			}
		}
		if d.StorageRoot != "" && d.AuthURL != "" {
			return d, nil
		}
	}
	return nil, errors.New("webfinger: no usable remoteStorage link")
}

// page runs in the browser at the redirect URI. The fragment carries
// access_token/state (or error/error_description); POST it back verbatim.
const page = `<!doctype html><meta charset="utf-8"><title>rclone remoteStorage</title>
<p id="m">Completing authorization…</p>
<script>
const h = location.hash.slice(1);
fetch("/fragment", {method: "POST", headers: {"Content-Type": "application/x-www-form-urlencoded"}, body: h})
  .then(r => r.text()).then(t => document.getElementById("m").textContent = t)
  .catch(e => document.getElementById("m").textContent = "Failed: " + e);
</script>`

type grant struct {
	token string
	err   error
}

func main() {
	address := flag.String("address", "", "user@host to authorize")
	insecure := flag.Bool("insecure-http", false, "use http:// for WebFinger (local test servers)")
	port := flag.Int("port", 53682, "loopback port (rclone's oauthutil default)")
	scope := flag.String("scope", "*:rw", "OAuth scope")
	timeout := flag.Duration("timeout", 2*time.Minute, "how long to wait for the browser")
	flag.Parse()
	if err := run(*address, *insecure, *port, *scope, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(address string, insecure bool, port int, scope string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	d, err := discover(ctx, address, insecure)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "discovered: %+v\n", *d)

	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	redirectURI := origin + "/"
	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	got := make(chan grant, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, page)
	})
	mux.HandleFunc("POST /fragment", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		v, err := url.ParseQuery(string(body))
		var g grant
		switch {
		case err != nil:
			g.err = fmt.Errorf("bad fragment: %w", err)
		case v.Get("error") != "":
			g.err = fmt.Errorf("server returned %s: %s", v.Get("error"), v.Get("error_description"))
		case v.Get("state") != state:
			g.err = errors.New("state mismatch")
		case v.Get("access_token") == "":
			g.err = errors.New("no access_token in fragment")
		default:
			g.token = v.Get("access_token")
		}
		if g.err != nil {
			http.Error(w, g.err.Error(), http.StatusBadRequest)
		} else {
			io.WriteString(w, "Authorized. You can close this window and return to rclone.")
		}
		select {
		case got <- g:
		default:
		}
	})
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	q := url.Values{
		"client_id":     {origin},
		"redirect_uri":  {redirectURI},
		"response_type": {"token"},
		"scope":         {scope},
		"state":         {state},
	}
	sep := "?"
	if strings.Contains(d.AuthURL, "?") {
		sep = "&"
	}
	fmt.Println("AUTH_URL " + d.AuthURL + sep + q.Encode())

	var g grant
	select {
	case g = <-got:
	case <-ctx.Done():
		return errors.New("timed out waiting for the browser")
	}
	if g.err != nil {
		return g.err
	}

	// Prove the token works: list the storage root (needs *:r).
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(d.StorageRoot, "/")+"/", nil)
	req.Header.Set("Authorization", "Bearer "+g.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	listing, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("root listing with new token: %s %s", resp.Status, listing)
	}
	out, _ := json.Marshal(map[string]any{
		"storage_root": d.StorageRoot,
		"spec_version": d.Version,
		"token_len":    len(g.token),
		"root_status":  resp.StatusCode,
		"root_listing": json.RawMessage(listing),
	})
	fmt.Println("RESULT " + string(out))
	return nil
}
