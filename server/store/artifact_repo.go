package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
)

// Artifact represents a named piece of data produced/consumed by a step.
type Artifact struct {
	ID        uuid.UUID `json:"id"`
	RunID     uuid.UUID `json:"run_id"`
	StepID    uuid.UUID `json:"step_id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // json|text|file
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type artifactRepo struct{ s *Store }

func (r artifactRepo) Create(a *Artifact) error {
	a.ID = uuid.New()
	a.CreatedAt = Now()
	q := `INSERT INTO artifacts(id,run_id,step_id,name,type,content,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.s.db.Exec(q, a.ID, a.RunID, a.StepID, a.Name, a.Type, a.Content, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	return nil
}

func (r artifactRepo) Get(id uuid.UUID) (*Artifact, error) {
	q := `SELECT id,run_id,step_id,name,type,content,created_at FROM artifacts WHERE id=$1`
	var a Artifact
	err := r.s.db.QueryRow(q, id).Scan(&a.ID, &a.RunID, &a.StepID, &a.Name, &a.Type, &a.Content, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("artifact not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return &a, nil
}

func (r artifactRepo) ByRun(runID uuid.UUID) ([]*Artifact, error) {
	q := `SELECT id,run_id,step_id,name,type,content,created_at FROM artifacts WHERE run_id=$1 ORDER BY created_at ASC`
	rows, err := r.s.db.Query(q, runID)
	if err != nil {
		return nil, fmt.Errorf("by run: %w", err)
	}
	defer rows.Close()
	var as []*Artifact
	for rows.Next() {
		var a Artifact
		err := rows.Scan(&a.ID, &a.RunID, &a.StepID, &a.Name, &a.Type, &a.Content, &a.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		as = append(as, &a)
	}
	return as, rows.Err()
}

type loopIterationRepo struct{ s *Store }

func (r loopIterationRepo) Create(li *domain.LoopIteration) error {
	li.ID = uuid.New()
	li.CreatedAt = Now()
	q := `INSERT INTO loop_iterations(id,run_id,step_id,iteration,status,eval_score,eval_details,converged,created_at)
	      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.s.db.Exec(q, li.ID, li.RunID, li.StepID, li.Iteration, li.Status,
		li.EvalScore, li.EvalDetails, li.Converged, li.CreatedAt)
	if err != nil {
		return fmt.Errorf("create loop iteration: %w", err)
	}
	return nil
}

func (r loopIterationRepo) Update(li *domain.LoopIteration) error {
	q := `UPDATE loop_iterations SET status=$2,eval_score=$3,eval_details=$4,converged=$5 WHERE id=$6`
	_, err := r.s.db.Exec(q, li.Status, li.EvalScore, li.EvalDetails, li.Converged, li.ID)
	return err
}

func (r loopIterationRepo) Get(runID, stepID uuid.UUID, iteration int) (*domain.LoopIteration, error) {
	q := `SELECT id,run_id,step_id,iteration,status,eval_score,eval_details,converged,created_at
	      FROM loop_iterations WHERE run_id=$1 AND step_id=$2 AND iteration=$3`
	var li domain.LoopIteration
	err := r.s.db.QueryRow(q, runID, stepID, iteration).Scan(
		&li.ID, &li.RunID, &li.StepID, &li.Iteration, &li.Status,
		&li.EvalScore, &li.EvalDetails, &li.Converged, &li.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("loop iteration not found: %s/%s/%d", runID, stepID, iteration)
	}
	if err != nil {
		return nil, fmt.Errorf("get loop iteration: %w", err)
	}
	return &li, nil
}

func (r loopIterationRepo) ByStep(runID, stepID uuid.UUID) ([]*domain.LoopIteration, error) {
	q := `SELECT id,run_id,step_id,iteration,status,eval_score,eval_details,converged,created_at
	      FROM loop_iterations WHERE run_id=$1 AND step_id=$2 ORDER BY iteration ASC`
	rows, err := r.s.db.Query(q, runID, stepID)
	if err != nil {
		return nil, fmt.Errorf("by step: %w", err)
	}
	defer rows.Close()
	var lis []*domain.LoopIteration
	for rows.Next() {
		var li domain.LoopIteration
		err := rows.Scan(&li.ID, &li.RunID, &li.StepID, &li.Iteration, &li.Status,
			&li.EvalScore, &li.EvalDetails, &li.Converged, &li.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan loop iteration: %w", err)
		}
		lis = append(lis, &li)
	}
	return lis, rows.Err()
}