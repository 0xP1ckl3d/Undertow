package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// prepareClientOutputDirectory gives newly created directories to the user
// who launched sudo. It also repairs old root-owned directories under the
// default outputs tree, without changing unrelated existing directories.
func prepareClientOutputDirectory(directory string) error {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	var missing []string
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("output parent is not a directory: %s", current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		if parent := filepath.Dir(current); parent == current {
			return fmt.Errorf("cannot find output parent for %s", abs)
		}
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := setLocalOutputOwner(missing[i]); err != nil {
			return err
		}
	}
	root, err := filepath.Abs("outputs")
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("output directory is not a directory: %s", current)
		}
		if err := setLocalOutputOwner(current); err != nil {
			return err
		}
		if current == root {
			break
		}
	}
	return nil
}
