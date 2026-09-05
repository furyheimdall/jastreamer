//go:build windows

package security

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateKeyOwnedByServer_acceptsProcessAndAdministratorsContext(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	matches, err := windowsPrivateKeyOwnedByServer(user.User.Sid)
	if err != nil || !matches {
		t.Fatalf("current user SID rejected: %v %v", matches, err)
	}

	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	matches, err = windowsPrivateKeyOwnedByServer(world)
	if err != nil || matches {
		t.Fatalf("world SID accepted: %v %v", matches, err)
	}

	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	member, err := token.IsMember(admins)
	if err != nil {
		t.Fatal(err)
	}
	matches, err = windowsPrivateKeyOwnedByServer(admins)
	if err != nil {
		t.Fatal(err)
	}
	if matches != member {
		t.Fatalf("administrators owner match = %v, membership = %v", matches, member)
	}
}

func TestValidatePrivateKeyOwnership_acceptsProcessCreatedKey(t *testing.T) {
	directory := externalIdentityTestDirectory(t)
	path := filepath.Join(directory, "tls-key.pem")
	if err := os.WriteFile(path, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateKeyOwnership(path, info); err != nil {
		t.Fatal(err)
	}
}
