// Command rclone is stock rclone with the remotestorage backend compiled in.
package main

import (
	_ "github.com/jonocodes/rclone-remotestorage/backend/remotestorage"
	_ "github.com/rclone/rclone/backend/all" // import all backends
	"github.com/rclone/rclone/cmd"
	_ "github.com/rclone/rclone/cmd/all" // import all commands
)

func main() {
	cmd.Main()
}
