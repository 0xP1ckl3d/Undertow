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

func TestDeleteAgentRecordsClearsOnlySelectedLocalHistory(t *testing.T) {
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "gui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"archive-a", "archive-b"} {
		if _, err := store.db.Exec(`INSERT INTO console_entries(agent_id,source,kind,text) VALUES(?,?,?,?)`, id, "operator", "output", "retained"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO topology_layout(id,x,y) VALUES(?,?,?)`, "agent:"+id, 1, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteAgentRecords([]string{"archive-a"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   string
		want int
	}{{"archive-a", 0}, {"archive-b", 1}} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM console_entries WHERE agent_id=?`, test.id).Scan(&count); err != nil || count != test.want {
			t.Fatalf("console %s: %d %v", test.id, count, err)
		}
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM topology_layout WHERE id=?`, "agent:"+test.id).Scan(&count); err != nil || count != test.want {
			t.Fatalf("layout %s: %d %v", test.id, count, err)
		}
	}
}
