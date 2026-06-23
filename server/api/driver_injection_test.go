package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ming-agents/server/engine"
)

type recordingRunDriver struct {
	launched uuid.UUID
}

func (d *recordingRunDriver) Launch(runID uuid.UUID) error {
	d.launched = runID
	return nil
}

func (d *recordingRunDriver) PauseContext(context.Context, uuid.UUID) error {
	return nil
}

func (d *recordingRunDriver) ResumeRun(runID uuid.UUID) (*engine.RecoveryResult, error) {
	return &engine.RecoveryResult{}, nil
}

func TestServerUsesInjectedRunDriverForStart(t *testing.T) {
	driver := &recordingRunDriver{}
	srv := NewServer(nil, nil, nil, nil, WithRunDriver(driver))
	runID := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/runs/"+runID.String()+"/start", strings.NewReader(""))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if driver.launched != runID {
		t.Fatalf("launched run = %s, want %s", driver.launched, runID)
	}
}
