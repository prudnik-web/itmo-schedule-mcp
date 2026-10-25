//go:build windows

package platform

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDirectoryDACLIsProtected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL still inherits permissions", err)
	}
	sid, err := currentUserID()
	if err != nil {
		t.Fatal(err)
	}
	sddl := sd.String()
	if strings.Count(sddl, "(A;") != 2 || !strings.Contains(sddl, ";;;"+sid+")") || !strings.Contains(sddl, ";;;SY)") {
		t.Fatalf("unexpected state permissions: %s", sddl)
	}
}
