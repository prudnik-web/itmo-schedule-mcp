package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invoke(t *testing.T, args ...string) error {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"itmo-calendar-sync"}, args...)
	defer func() { os.Args = original }()
	return run()
}

func TestCLIHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"setup", "--help"}, {"auth", "--help"}} {
		if err := invoke(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := invoke(t, "not-a-command"); err == nil {
		t.Fatal("unknown command accepted")
	}
	if err := invoke(t, "auth", "--auth-port", "-1"); err == nil {
		t.Fatal("invalid auth port accepted")
	}
	if err := invoke(t, "sync", "--migrate-primary"); err == nil {
		t.Fatal("cleanup must be explicitly configured through setup")
	}
}

func TestStatusDoesNotInitializeProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if err := invoke(t, "status", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("status created profile state")
	}
}

func TestSetupRejectsNoninteractiveCredentials(t *testing.T) {
	original := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer r.Close()
	os.Stdin = r
	defer func() { os.Stdin = original }()
	err = invoke(t, "setup", "--dir", filepath.Join(t.TempDir(), "profile"), "--no-auth")
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("expected terminal requirement: %v", err)
	}
}
