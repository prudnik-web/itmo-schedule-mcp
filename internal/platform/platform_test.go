package platform

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestPrivateStateAndLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := EnsurePrivateDir(dir); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private mode: %v %v", info, err)
		}
	}
	path := filepath.Join(dir, "sync.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := Lock(path); !errors.Is(err, ErrLocked) {
		if duplicate != nil {
			duplicate()
		}
		t.Fatalf("second lock succeeded: %v", err)
	}
	unlock()
	unlock()
	unlock, err = Lock(path)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	unlock()
}

func TestInstallExecutableUsesPrivateStableCopy(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "download")
	if err := os.WriteFile(source, []byte("example executable"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "state")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	installed, err := installExecutable(source, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(installed, filepath.Join(dir, "bin")) {
		t.Fatal("binary depends on download location")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(installed)
	if err != nil || string(data) != "example executable" {
		t.Fatal("installed copy missing", err)
	}
	if _, err := installExecutable(installed, dir); err != nil {
		t.Fatal("same-file install should be safe", err)
	}
}

func xmlValues(t *testing.T, data, element string) []string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(data))
	var values []string
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := token.(xml.StartElement); ok && e.Name.Local == element {
			var v string
			if err := d.DecodeElement(&v, &e); err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
	}
	return values
}

func TestLaunchdPreservesPathCharacters(t *testing.T) {
	dir := `/home/a user/quotes" & % $ stuff`
	bin := dir + `/bin/app`
	x := launchdConfig("itmo-calendar-sync-test", bin, dir)
	stringsInXML := xmlValues(t, x, "string")
	if len(stringsInXML) != 8 || stringsInXML[1] != bin || stringsInXML[4] != dir {
		t.Fatalf("paths changed in XML: %#v", stringsInXML)
	}
	if !strings.Contains(x, "<key>StartCalendarInterval</key>") || !strings.Contains(x, "<integer>7</integer>") {
		t.Fatal("expected calendar trigger for wake catch-up")
	}
}

func TestSystemdPreservesSpecialCharactersWithoutShell(t *testing.T) {
	dir := `/home/a user/quotes" & % ${DATA} \ stuff`
	service := systemdService(dir+"/bin/app", dir)
	if !strings.Contains(service, `ExecStart=":/home/`) || !strings.Contains(service, `${DATA}`) || strings.Contains(service, `$${DATA}`) {
		t.Fatal("environment expansion must be disabled with the colon executable prefix")
	}
	if !strings.Contains(service, `quotes\" & %% ${DATA} \\ stuff`) {
		t.Fatal("unit quoting is incorrect", service)
	}
	if strings.Contains(service, "sh -c") {
		t.Fatal("scheduler must launch the executable directly")
	}
	if !strings.Contains(systemdTimer("profile"), "Persistent=true") {
		t.Fatal("Linux timer must catch up after downtime")
	}
}

func TestWindowsXMLAndArgumentQuoting(t *testing.T) {
	dir := `C:\Users\A B\%value% $name & stuff\`
	bin := dir + "bin\\app.exe"
	launcher := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	x := windowsTaskConfig(launcher, bin, dir, "S-1-5-21-1", time.Date(2026, 1, 1, 10, 7, 0, 0, time.UTC))
	commands := xmlValues(t, x, "Command")
	args := xmlValues(t, x, "Arguments")
	if len(commands) != 1 || commands[0] != launcher || len(args) != 1 || strings.Contains(args[0], "%") {
		t.Fatalf("Task Scheduler paths changed: %#v %#v", commands, args)
	}
	encoded := strings.TrimPrefix(args[0], "-NoProfile -NonInteractive -EncodedCommand ")
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	chars := make([]uint16, len(data)/2)
	for i := range chars {
		chars[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	script := string(utf16.Decode(chars))
	for _, path := range []string{bin, dir} {
		if !strings.Contains(script, "FromBase64String('"+base64.StdEncoding.EncodeToString([]byte(path))+"')") {
			t.Fatal("path data changed or was interpolated into PowerShell code")
		}
	}
	if !strings.Contains(script, "& $p 'sync' '--dir' $d '--scheduled'") {
		t.Fatal("unexpected launcher command")
	}
	if got := xmlValues(t, x, "LogonType"); len(got) != 1 || got[0] != "InteractiveToken" {
		t.Fatal("task must use current interactive user without stored password")
	}
	if got := xmlValues(t, x, "Interval"); len(got) != 1 || got[0] != "PT1H" {
		t.Fatal("task is not hourly")
	}
}

func TestProfilesAndInvalidPaths(t *testing.T) {
	if profileName("/first") == profileName("/second") {
		t.Fatal("profiles collide")
	}
	for _, bad := range []string{"", "/tmp/a\nb", "/tmp/a\x00b"} {
		if _, err := absolutePath(bad); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
}
