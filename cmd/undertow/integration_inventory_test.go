package main

import (
	"context"
	"encoding/json"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

// These transport tests model agents on separate hosts. A synthetic inventory
// keeps the test independent of interfaces on the machine running the suite.
func sendIsolatedTestInventory(ctx context.Context, streamMux *mux.Mux) error {
	data, err := json.Marshal(map[string]any{
		"hostname":     "test-agent",
		"capabilities": pivot.DefaultCapabilities().Report(),
	})
	if err != nil {
		return err
	}
	return streamMux.SendControl(ctx, data)
}
