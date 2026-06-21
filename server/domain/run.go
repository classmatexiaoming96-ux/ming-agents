package domain

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RunStatus represents the status of a Run.
type RunStatus string

const (
	RunStatusPending    RunStatus = "pending"
	RunStatusRunning    RunStatus = "running"
	RunStatusPaused     RunStatus = "paused"
	RunStatusCompleted  RunStatus = "completed"
	RunStatusFailed     RunStatus = "failed"
	RunStatusCancelled  RunStatus = "cancelled"
)

// Run is the top-level execution unit.
type Run struct {
	ID                   uuid.UUID       `json:"id"`
	Name                 string         `json:"name"`
	WDLVersion           string         `json:"wdl_version"`
	Status               RunStatus      `json:"status"`
	WDLSource            sql.NullString `json:"-"`
	WDLSourceStr         string         `json:"wdl_source,omitempty"`
	MaxParallel          int            `json:"max_parallel"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	EndedAt              sql.NullTime   `json:"-"`
	EndedAtStr           *string        `json:"ended_at,omitempty"`
	ErrorMsg             sql.NullString `json:"-"`
	ErrorMsgStr          *string        `json:"error_msg,omitempty"`
	Version              int           `json:"version"`
	TemplateName         string         `json:"template_name,omitempty"` // Epic 2.14: template this run was created from
	CriticallyNodesJSON  sql.NullString `json:"-"` // Epic 2.14: serialized CriticallyNodes
	CriticallyNodesStr   string        `json:"critically_nodes,omitempty"` // Epic 2.14: for JSON serialization
}

// MarshalJSON custom marshaler to surface null strings.
func (r Run) MarshalJSON() ([]byte, error) {
	type Alias Run
	aux := struct {
		Alias
		WDLSourceStr string `json:"wdl_source,omitempty"`
		EndedAtStr   string `json:"ended_at,omitempty"`
		ErrorMsgStr  string `json:"error_msg,omitempty"`
	}{
		Alias:        Alias(r),
		WDLSourceStr: r.WDLSource.String,
	}
	if r.EndedAt.Valid {
		aux.EndedAtStr = r.EndedAt.Time.Format(time.RFC3339)
	}
	if r.ErrorMsg.Valid {
		aux.ErrorMsgStr = r.ErrorMsg.String
	}
	return json.Marshal(aux)
}

// CanStart returns true if the run can transition to running.
func (r Run) CanStart() bool { return r.Status == RunStatusPending }

// CanComplete returns true if the run can complete.
func (r Run) CanComplete() bool { return r.Status == RunStatusRunning }