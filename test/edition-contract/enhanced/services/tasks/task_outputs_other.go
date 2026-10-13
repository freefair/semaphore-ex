//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package tasks

import (
	"fmt"
	"io"
	"os"
)

// Platforms without O_NOFOLLOW validate both path and descriptor identity.
// The capture directory is private, so an untrusted task cannot race its path.
func readTaskOutputsFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("outputs file is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("outputs file changed during collection")
	}
	return io.ReadAll(io.LimitReader(file, maxPublicOutputDocumentBytes+1))
}
