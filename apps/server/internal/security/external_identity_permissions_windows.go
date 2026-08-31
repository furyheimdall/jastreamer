//go:build windows

package security

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func validatePrivateKeyOwnership(path string, info os.FileInfo) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return errors.New("inspect external TLS private key ACL")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return errors.New("inspect external TLS private key owner")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("inspect Server account identity")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("external TLS private key requires an explicit ACL")
	}
	broad := make([]*windows.SID, 0, 3)
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinWorldSid, windows.WinAuthenticatedUserSid, windows.WinBuiltinUsersSid} {
		sid, sidErr := windows.CreateWellKnownSid(kind)
		if sidErr != nil {
			return errors.New("inspect Windows broad-access identities")
		}
		broad = append(broad, sid)
	}
	broadRead := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return errors.New("inspect external TLS private key ACL entry")
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask&(windows.GENERIC_READ|windows.FILE_GENERIC_READ|windows.FILE_READ_DATA) == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		for _, candidate := range broad {
			if sid.Equals(candidate) {
				broadRead = true
			}
		}
	}
	return externalPrivateKeyPermissionPolicy("windows", info.Mode(), owner != nil && owner.Equals(user.User.Sid), broadRead)
}
