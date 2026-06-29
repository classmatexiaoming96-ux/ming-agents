package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetEvaluationReturnsEvaluationJSON(t *testing.T) {
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

	runID := "run-eval-api"
	evalPath := filepath.Join(".workflow", "runs", runID, "evaluation.json")
	if err := os.MkdirAll(filepath.Dir(evalPath), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(evalPath, []byte(`{"run_id":"run-eval-api","passed":true,"failure_class":"none"}`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := NewServer(nil, nil, nil, nil, WithFeishuNotifier(nil))
	req := httptest.NewRequest(http.MethodGet, "/runs/"+runID+"/evaluation", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"failure_class":"none"`) {
		t.Fatalf("body = %s, want evaluation json", body)
	}
}

func TestGetEvaluationReturnsEvaluationJSONUnderAPIPrefix(t *testing.T) {
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

	runID := "run-eval-api-prefix"
	evalPath := filepath.Join(".workflow", "runs", runID, "evaluation.json")
	if err := os.MkdirAll(filepath.Dir(evalPath), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(evalPath, []byte(`{"run_id":"run-eval-api-prefix","passed":false,"failure_class":"product_defect"}`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := NewServer(nil, nil, nil, nil, WithFeishuNotifier(nil))
	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/evaluation", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"failure_class":"product_defect"`) {
		t.Fatalf("body = %s, want evaluation json", body)
	}
}

func TestGetEvaluationReturnsNotFound(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/runs/missing/evaluation", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
