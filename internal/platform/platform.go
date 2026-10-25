// Package platform provides private local state and per-user scheduling.
package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrLocked = errors.New("another instance is already running")

func DefaultDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home == "" {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "itmo-calendar-sync")
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if !filepath.IsAbs(base) {
			if home == "" {
				return ""
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "itmo-calendar-sync")
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if !filepath.IsAbs(base) {
			if home == "" {
				return ""
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "itmo-calendar-sync")
	}
}

func OpenBrowser(ctx context.Context, link string) error {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("browser URL must use HTTP or HTTPS")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return openBrowser(ctx, link)
}

func absolutePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	for _, r := range path {
		if r < 32 || r == 127 {
			return "", errors.New("paths cannot contain control characters")
		}
	}
	return filepath.Abs(path)
}

func profileName(dir string) string {
	path := filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	hash := sha256.Sum256([]byte(path))
	return "itmo-calendar-sync-" + hex.EncodeToString(hash[:6])
}

func escapedXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".itmo-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func installExecutable(executable, dir string) (string, error) {
	source, err := absolutePath(executable)
	if err != nil {
		return "", err
	}
	binDir := filepath.Join(dir, "bin")
	if err = EnsurePrivateDir(binDir); err != nil {
		return "", err
	}
	name := "itmo-calendar-sync"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	destination := filepath.Join(binDir, name)
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("executable must be a regular file")
	}
	if existing, e := os.Stat(destination); e == nil && os.SameFile(info, existing) {
		return destination, nil
	}
	out, err := os.CreateTemp(binDir, ".itmo-binary-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	if err = out.Chmod(0700); err == nil {
		_, err = io.Copy(out, in)
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(out.Name(), destination); err != nil {
		return "", fmt.Errorf("cannot replace installed executable; stop any running sync and retry: %w", err)
	}
	return destination, nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 1500 {
			message = message[:1500]
		}
		return fmt.Errorf("%s failed: %w (%s)", name, err, message)
	}
	return nil
}

func saveSchedule(dir, scheduler, name, executable string, paths []string) error {
	data, err := json.MarshalIndent(map[string]any{"scheduler": scheduler, "name": name, "executable": executable, "configuration_paths": paths}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "schedule.json"), append(data, '\n'), 0600)
}

func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
