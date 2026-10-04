package pivot

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStructuredFileListingPagesWithoutReadingContents(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 205; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%03d", i)), []byte("private data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := listFiles([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != dir || len(first.Entries) != 200 || !first.HasMore || first.Entries[0].Name == "" {
		t.Fatalf("first page: %+v", first)
	}
	next, err := listFiles([]string{dir, "200"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Offset != 200 || len(next.Entries) != 5 || next.HasMore {
		t.Fatalf("second page: %+v", next)
	}
	if _, err := listFiles([]string{dir, "-1"}); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := listFiles([]string{filepath.Join(dir, "item-001")}); err == nil {
		t.Fatal("file accepted as a directory")
	}
}
