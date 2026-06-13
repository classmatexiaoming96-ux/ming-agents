package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
)

// newMemoryTestServer wires a minimal Server with only the memory routes and an
// isolated vault/FTS dir, enough to exercise the D1 HTTP surface end to end.
func newMemoryTestServer(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	prevVault := memory.VaultDir
	memory.VaultDir = dir
	t.Cleanup(func() { memory.VaultDir = prevVault })

	mux := http.NewServeMux()
	s := &Server{}
	s.registerMemoryRoutes(mux)
	return mux
}

func TestMemoryRoutesIngestRecallStats(t *testing.T) {
	h := newMemoryTestServer(t)

	// Ingest.
	body, _ := json.Marshal(ingestReq{
		Content: "decision: adopt pgx pooling because of 30000ms timeouts",
		Type:    "decision", Project: "demo", Tags: []string{"db"}, Source: "manual",
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/memory/ingest", strings.NewReader(string(body))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201: %s", rec.Code, rec.Body)
	}
	var ing memory.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &ing); err != nil {
		t.Fatalf("decode ingest: %v", err)
	}
	if !ing.Accepted || ing.ID == "" {
		t.Fatalf("unexpected ingest result: %+v", ing)
	}
	if filepath.Base(filepath.Dir(ing.Path)) != "demo" {
		t.Errorf("ingest path = %s, want under notes/demo", ing.Path)
	}

	// Recall by project.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/memory/recall?project=demo&limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("recall status = %d: %s", rec.Code, rec.Body)
	}
	var recall struct {
		Results []memory.Memory `json:"results"`
		Total   int             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &recall); err != nil {
		t.Fatalf("decode recall: %v", err)
	}
	if recall.Total != 1 || len(recall.Results) != 1 {
		t.Fatalf("recall returned %d results (total %d), want 1", len(recall.Results), recall.Total)
	}

	// Feedback bumps hit_count.
	fb, _ := json.Marshal(feedbackReq{ID: ing.ID, Used: true, Helpful: true})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/memory/feedback", strings.NewReader(string(fb))))
	if rec.Code != http.StatusOK {
		t.Fatalf("feedback status = %d: %s", rec.Code, rec.Body)
	}

	// Stats reflects one active memory.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/memory/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d", rec.Code)
	}
	var stats struct {
		Active int `json:"active"`
	}
	json.Unmarshal(rec.Body.Bytes(), &stats)
	if stats.Active != 1 {
		t.Errorf("stats active = %d, want 1", stats.Active)
	}
}

func TestMemoryIngestRequiresContent(t *testing.T) {
	h := newMemoryTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/memory/ingest", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty content status = %d, want 400", rec.Code)
	}
}
