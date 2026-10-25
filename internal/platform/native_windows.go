//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

func currentUserID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func windowsPowerShell() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	// A user-chosen state directory may contain percent sequences. System
	// directories with percent characters cannot safely be passed to Task Scheduler.
	if strings.Contains(dir, "%") {
		return "", errors.New("Windows system directory contains unsupported percent characters")
	}
	path := filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(path); err != nil {
		return "", errors.New("built-in Windows PowerShell is required for scheduling")
	}
	return path, nil
}

// Windows ignores POSIX mode bits. A protected inheritable DACL explicitly
// grants access only to the current user and SYSTEM, or setup fails closed.
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
	sid, err := currentUserID()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.New("cannot create private Windows directory permissions")
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("cannot protect state directory with a private Windows DACL: %w", err)
	}
	return nil
}

func Lock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	overlapped := &windows.Overlapped{}
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped); _ = file.Close() })
	}, nil
}

func openBrowser(ctx context.Context, link string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
