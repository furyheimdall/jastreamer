//go:build !windows

package security

import (
	"errors"
	"os"
	"syscall"
)

func validatePrivateKeyOwnership(_ string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("inspect external TLS private key owner")
	}
	return externalPrivateKeyPermissionPolicy("unix", info.Mode(), stat.Uid == uint32(os.Geteuid()), false)
}
