package remotestorage

// Out of tree there is no embedded docs/data/backends/remotestorage.yaml, so
// fs.Register logs "no overview data found" on every run. Mute rclone's
// logger while this package initialises: package variables are set before
// any init() runs, and this file's init() runs after remotestorage.go's
// because Go runs them in file name order. test/upstream-sync.sh leaves this
// file out.

import (
	"io"
	"log/slog"

	"github.com/rclone/rclone/fs"
	fslog "github.com/rclone/rclone/fs/log"
)

var _ = func() bool {
	fs.SetLogger(slog.NewTextHandler(io.Discard, nil))
	return true
}()

func init() { fs.SetLogger(fslog.Handler) }
