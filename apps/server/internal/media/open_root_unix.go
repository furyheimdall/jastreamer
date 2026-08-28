//go:build unix

package media

import (
	"os"
	"syscall"
)

func openRootReadOnly(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
