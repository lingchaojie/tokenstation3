//go:build aix || linux || darwin || dragonfly || freebsd || netbsd || openbsd || solaris

package spool

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openBatchDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, classifyBatchOpenError("open batch directory", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openBatchDirectoryAt(directory *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, classifyBatchOpenError("openat batch directory", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func openBatchRegularAt(directory *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, classifyBatchOpenError("openat batch metadata", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func classifyBatchOpenError(op, path string, err error) error {
	if errors.Is(err, unix.ELOOP) {
		return ErrSpoolCorrupt
	}
	return &os.PathError{Op: op, Path: path, Err: err}
}
