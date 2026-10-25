//go:build darwin || linux

package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"
)

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private state path must be a real directory")
	}
	return os.Chmod(path, 0700)
}

func Lock(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }) }, nil
}

func currentUserID() (string, error) { return strconv.Itoa(os.Getuid()), nil }

func windowsPowerShell() (string, error) {
	return "", errors.New("Windows PowerShell is unavailable on this platform")
}

func openBrowser(ctx context.Context, link string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "/usr/bin/open"
	}
	return exec.CommandContext(ctx, command, link).Run()
}
