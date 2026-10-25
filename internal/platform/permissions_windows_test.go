//go:build windows

package platform

import (
	"path/filepath"
	"testing"
	"unsafe"

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
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 {
		t.Fatalf("expected exactly two explicit access entries: %s (%v)", sd.String(), err)
	}
	// SDDL renders some users as aliases (for example LA for the local
	// administrator), so compare binary SIDs rather than their display text.
	want := []*windows.SID{user.User.Sid, system}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			t.Fatalf("unexpected access or inheritance flags: %+v", ace.Header)
		}
		// SidStart is the first DWORD of the variable-length SID in this ACE.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		matched := false
		for j, expected := range want {
			if expected != nil && sid.Equals(expected) {
				want[j] = nil
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("unexpected or duplicate trustee: %s", sid.String())
		}
	}
}
