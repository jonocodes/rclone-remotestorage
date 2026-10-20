package remotestorage

// Interactive configuration: WebFinger discovery, then a browser login
// (implicit grant on a loopback listener) or, on a machine without a
// browser, "rclone authorize" elsewhere / a pasted token.
//
// rclone's lib/oauthutil only handles the authorization code flow, so this
// mirrors its conventions (config_is_local, config_refresh_token,
// config_token, "rclone authorize") instead of using it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/lib/oauthutil"
)

// openURL opens a URL in the user's browser (overridden in tests)
var openURL = func(u string) error { return oauthutil.OpenURL(u) }

// Config runs the configuration state machine
func Config(ctx context.Context, name string, m configmap.Mapper, in fs.ConfigIn) (*fs.ConfigOut, error) {
	authorizing, _ := m.Get(config.ConfigAuthorize) // running "rclone authorize"
	address, _ := m.Get("user_address")
	scope, _ := m.Get("scope")
	if scope == "" {
		scope = "*:rw"
	}

	switch in.State {
	case "":
		token, _ := m.Get("token")
		switch {
		case authorizing != "":
			return fs.ConfigGoto("address")
		case token != "" && address == "":
			// configured by hand (storage_root + token): nothing to do
			return nil, nil
		case token != "":
			return fs.ConfigConfirm("replace", true, "config_refresh_token", "Token already configured - replace it?")
		}
		return fs.ConfigGoto("address")

	case "replace":
		if in.Result == "false" {
			return nil, nil
		}
		return fs.ConfigGoto("address")

	case "address":
		if address == "" {
			if authorizing != "" {
				return fs.ConfigInput("address-result", "user_address", "Your remoteStorage address, like alice@5apps.com")
			}
			return fs.ConfigGoto("manual")
		}
		d, err := discover(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("couldn't find remoteStorage for %q: %w", address, err)
		}
		fs.Logf(nil, "Found remoteStorage for %s: %s (%s)", address, d.StorageRoot, d.Version)
		m.Set("storage_root", d.StorageRoot)
		m.Set("auth_url", d.AuthURL)
		if authorizing != "" {
			return fs.ConfigGoto("login")
		}
		return fs.ConfigConfirm("islocal", true, "config_is_local", "Use web browser to automatically authenticate rclone with remote?\n * Say Y if the machine running rclone has a web browser you can use\n * Say N if running rclone on a (remote) machine without web browser access\nIf not sure try Y. If Y failed, try N.\n")

	case "address-result":
		m.Set("user_address", strings.TrimSpace(in.Result))
		return fs.ConfigGoto("address")

	case "islocal":
		if in.Result == "true" {
			return fs.ConfigGoto("login")
		}
		blob, err := configmap.Simple{"user_address": address, "scope": scope}.Encode()
		if err != nil {
			return nil, err
		}
		return fs.ConfigInput("authorize-result", "config_token", fmt.Sprintf(`For this to work, you will need rclone with the remotestorage backend on a
machine that has a web browser available. Execute the following there:

	rclone authorize "remotestorage" %q

Then paste the result. Alternatively paste a bearer token for %s.
`, blob, address))

	case "authorize-result":
		result := strings.TrimSpace(in.Result)
		outM := configmap.Simple{}
		if err := outM.Decode(result); err == nil && outM["token"] != "" {
			for k, v := range outM {
				m.Set(k, v)
			}
		} else {
			m.Set("token", result)
		}
		return nil, nil

	case "login":
		authURL, _ := m.Get("auth_url")
		noBrowser, _ := m.Get(config.ConfigAuthNoBrowser)
		token, err := login(ctx, authURL, scope, noBrowser == "")
		if err != nil {
			return nil, err
		}
		m.Set("token", token)
		return nil, nil

	case "manual":
		if root, _ := m.Get("storage_root"); root == "" {
			return fs.ConfigInput("manual-root", "storage_root", "Storage root URL, e.g. https://example.com/storage/alice")
		}
		return fs.ConfigGoto("manual-token")

	case "manual-root":
		m.Set("storage_root", strings.TrimSpace(in.Result))
		return fs.ConfigGoto("manual-token")

	case "manual-token":
		return fs.ConfigInput("manual-token-result", "token", "OAuth bearer token")

	case "manual-token-result":
		m.Set("token", strings.TrimSpace(in.Result))
		return nil, nil
	}
	return nil, fmt.Errorf("unknown state %q", in.State)
}
