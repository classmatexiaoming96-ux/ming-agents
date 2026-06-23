package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
	_ "modernc.org/sqlite"
)

func TestListRunsReturnsSummaryEnvelopeAndFiltersStatus(t *testing.T) {
	db := newAPITestDB(t)
	st := store.NewStore(db)

	running := &domain.Run{Name: "active", Status: domain.RunStatusRunning, MaxParallel: 2}
	if err := st.CreateRun(running); err != nil {
		t.Fatalf("create running run: %v", err)
	}
	if err := st.CreateStep(&domain.Step{RunID: running.ID, Name: "draft", StepType: domain.StepTypeTask, Status: domain.StepStatusCompleted}); err != nil {
		t.Fatalf("create completed step: %v", err)
	}
	if err := st.CreateStep(&domain.Step{RunID: running.ID, Name: "code_review", StepType: domain.StepTypeTask, Status: domain.StepStatusWaitingUserInput}); err != nil {
		t.Fatalf("create waiting step: %v", err)
	}

	completed := &domain.Run{Name: "done", Status: domain.RunStatusCompleted, MaxParallel: 1}
	if err := st.CreateRun(completed); err != nil {
		t.Fatalf("create completed run: %v", err)
	}

	srv := NewServer(st, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/runs?status=running", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Runs []struct {
			RunID       string `json:"run_id"`
			Status      string `json:"status"`
			CurrentNode string `json:"current_node"`
			CreatedAt   string `json:"created_at"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Runs) != 1 {
		t.Fatalf("runs length = %d, want 1; body: %+v", len(body.Runs), body)
	}
	if body.Runs[0].RunID != running.ID.String() {
		t.Fatalf("run_id = %s, want %s", body.Runs[0].RunID, running.ID)
	}
	if body.Runs[0].Status != string(domain.RunStatusRunning) {
		t.Fatalf("status = %s, want running", body.Runs[0].Status)
	}
	if body.Runs[0].CurrentNode != "code_review" {
		t.Fatalf("current_node = %q, want code_review", body.Runs[0].CurrentNode)
	}
	if body.Runs[0].CreatedAt == "" {
		t.Fatal("created_at is empty")
	}
}

func newAPITestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE runs (
			id TEXT PRIMARY KEY,
			name TEXT,
			wdl_version TEXT,
			status TEXT,
			wdl_src TEXT,
			max_parallel INTEGER,
			created_at TIMESTAMP,
			updated_at TIMESTAMP,
			ended_at TIMESTAMP,
			error_msg TEXT,
			version INTEGER
		)`,
		`CREATE TABLE steps (
			id TEXT PRIMARY KEY,
			run_id TEXT,
			name TEXT,
			step_type TEXT,
			status TEXT,
			iteration INTEGER,
			attempt INTEGER,
			when_cond TEXT,
			inputs_json TEXT,
			outputs_json TEXT,
			skip_reason TEXT,
			created_at TIMESTAMP,
			updated_at TIMESTAMP
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
	return db
}
