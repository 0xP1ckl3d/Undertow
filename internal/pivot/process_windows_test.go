//go:build windows

package pivot

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"
)

func TestWindowsProcessInventoryContainsCurrentProcess(t *testing.T) {
	output, err := windowsProcessInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(output)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 || len(rows[0]) != 4 || rows[0][1] != "PID" {
		t.Fatalf("unexpected process inventory: %q", output)
	}
}
