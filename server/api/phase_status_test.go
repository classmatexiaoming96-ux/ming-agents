package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetPhaseStatusReturnsRunPhaseStatus(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	runID := "run-api"
	statusPath := filepath.Join(".workflow", "runs", runID, "phase_status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"run_id":"run-api","phase":"completed"}`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := NewServer(nil, nil, nil, nil, WithFeishuNotifier(nil))
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/phase-status", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"phase":"completed"`) {
		t.Fatalf("body = %s, want phase status", body)
	}
}

func TestGetPhaseStatusReturnsRunPhaseStatusUnderAPIPrefix(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	runID := "run-api-prefix"
	statusPath := filepath.Join(".workflow", "runs", runID, "phase_status.json")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(statusPath, []byte(`{"run_id":"run-api-prefix","phase":"approval"}`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := NewServer(nil, nil, nil, nil, WithFeishuNotifier(nil))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/phase-status", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"phase":"approval"`) {
		t.Fatalf("body = %s, want phase status", body)
	}
}

func TestGetPhaseStatusReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	srv := NewServer(nil, nil, nil, nil, WithFeishuNotifier(nil))
	req := httptest.NewRequest(http.MethodGet, "/runs/missing/phase-status", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
