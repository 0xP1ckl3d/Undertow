//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestSudoClientOutputOwnership(t *testing.T) {
	if os.Getenv("UNDERTOW_TEST_SUDO_OUTPUT") != "1" || os.Geteuid() != 0 || os.Getenv("SUDO_UID") == "" {
		t.Skip("run as sudo with UNDERTOW_TEST_SUDO_OUTPUT=1")
	}
	uid, err := strconv.Atoi(os.Getenv("SUDO_UID"))
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	directory := filepath.Join("outputs", "screenshots")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	} // prior root-owned output
	if err := prepareClientOutputDirectory(directory); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "capture.png")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setLocalOutputOwner(file); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"outputs", directory, file} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		owner := info.Sys().(*syscall.Stat_t)
		if owner.Uid != uint32(uid) || owner.Gid != uint32(gid) {
			t.Fatalf("%s owner=%d:%d want=%d:%d", path, owner.Uid, owner.Gid, uid, gid)
		}
	}
}
