//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tasks

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readTaskOutputsFile binds validation to the opened descriptor. O_NOFOLLOW
// blocks path substitution with a symlink and O_NONBLOCK makes a FIFO harmless.
func readTaskOutputsFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("outputs file is not a regular file")
	}
	return io.ReadAll(io.LimitReader(file, maxPublicOutputDocumentBytes+1))
}
