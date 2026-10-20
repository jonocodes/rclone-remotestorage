// Test remoteStorage filesystem interface
package remotestorage_test

import (
	"testing"

	"github.com/jonocodes/rclone-remotestorage/backend/remotestorage"
	"github.com/rclone/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestRemoteStorage:",
		NilObject:  (*remotestorage.Object)(nil),
	})
}
