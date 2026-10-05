//go:build linux || windows

package main

import (
	"path/filepath"
	"testing"
)

func TestArchivedVisibilityIsLocalAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui.db")
	store, err := openClientGUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SavePreferences(guiPreferences{OperatorID: "operator-one", DisplayName: "Operator One"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetArchivedVisibility(true); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePreferences(guiPreferences{OperatorID: "operator-two", DisplayName: "Operator Two"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openClientGUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	prefs, err := reopened.Preferences()
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.ShowArchived || prefs.OperatorID != "operator-two" {
		t.Fatalf("preferences after restart: %+v", prefs)
	}
}
