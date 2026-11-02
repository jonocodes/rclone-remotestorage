// Command rclone is stock rclone with the remotestorage backend compiled in.
package main

import (
	"os"
	"path/filepath"

	_ "github.com/jonocodes/rclone-remotestorage/backend/remotestorage"
	_ "github.com/rclone/rclone/backend/all" // import all backends
	"github.com/rclone/rclone/cmd"
	_ "github.com/rclone/rclone/cmd/all" // import all commands
)

func main() {
	// This build is for trying the backend out, so its remotes live in
	// rclone.conf next to the binary rather than in the user's global config.
	// RCLONE_CONFIG or --config still override it.
	if os.Getenv("RCLONE_CONFIG") == "" {
		if exe, err := os.Executable(); err == nil {
			_ = os.Setenv("RCLONE_CONFIG", filepath.Join(filepath.Dir(exe), "rclone.conf"))
		}
	}
	cmd.Main()
}
