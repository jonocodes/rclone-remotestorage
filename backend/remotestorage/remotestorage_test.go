// Test remoteStorage filesystem interface
package remotestorage_test

import (
	"testing"

	"github.com/jonocodes/rclone-remotestorage/backend/remotestorage"
	"github.com/rclone/rclone/fstest"
	"github.com/rclone/rclone/fstest/fstests"
)

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestRemoteStorage:",
		NilObject:  (*remotestorage.Object)(nil),
	})
}

// TestIntegrationRSilo runs the integration tests against RSilo's offline
// server (fstest/testserver/init.d/TestRemoteStorageRSilo)
func TestIntegrationRSilo(t *testing.T) {
	if *fstest.RemoteName != "" {
		t.Skip("skipping as -remote is set")
	}
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestRemoteStorageRSilo:",
		NilObject:  (*remotestorage.Object)(nil),
	})
}
