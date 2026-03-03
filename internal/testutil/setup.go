// Package testutil provides shared helpers for Go unit tests.
// It is not exported outside the module.
package testutil

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"evidra/internal/api"
	ce "evidra/internal/cloudevents"
	"evidra/internal/export"
	"evidra/internal/store"
)

// NewTestServer returns a configured in-memory HTTP handler, the backing
// repository, and a cleanup function that must be called at end of test.
func NewTestServer(t *testing.T) (http.Handler, store.Repository, func()) {
	t.Helper()
	repo := store.NewMemoryRepository()
	exporter := export.NewFilesystemExporter(t.TempDir())
	srv := api.NewServer(repo, exporter)
	return srv.Routes(), repo, func() {}
}

// MakeEvent creates a minimal StoredEvent suitable for seeding test repositories.
// The caller may override any field after construction.
func MakeEvent(id, subject string, overrides ...map[string]interface{}) ce.StoredEvent {
	ext := map[string]interface{}{
		"cluster":   "eu-1",
		"namespace": "prod-eu",
		"initiator": "jane.doe",
	}
	for _, o := range overrides {
		for k, v := range o {
			ext[k] = v
		}
	}
	return ce.StoredEvent{
		ID:         id,
		Source:     "git",
		Type:       "pull_request_merged",
		Time:       time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC),
		Subject:    subject,
		Extensions: ext,
		Data:       json.RawMessage(`{"repo":"org/payments"}`),
	}
}
