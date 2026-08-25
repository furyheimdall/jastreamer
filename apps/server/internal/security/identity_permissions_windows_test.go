//go:build windows

package security_test

import (
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/security"
	"golang.org/x/sys/windows"
)

func assertPrivateDirectoryACL(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("read directory ACL: %v", err)
	}
	sddl := descriptor.String()
	if !strings.HasPrefix(sddl, "D:P") {
		t.Fatalf("directory ACL inherits permissions: %s", sddl)
	}
	for _, publicSID := range []string{";;;WD)", ";;;BU)", ";;;AU)"} {
		if strings.Contains(sddl, publicSID) {
			t.Fatalf("directory ACL grants public access: %s", sddl)
		}
	}
	for _, privateSID := range []string{";;;SY)", ";;;BA)", ";;;OW)"} {
		if !strings.Contains(sddl, privateSID) {
			t.Fatalf("directory ACL omits %s: %s", privateSID, sddl)
		}
	}
}

func TestIdentity_uses_private_Windows_ACL(t *testing.T) {
	directory := t.TempDir()
	if _, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}}); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	assertPrivateDirectoryACL(t, directory)
}
