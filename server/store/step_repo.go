package store

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
)

type stepRepo struct{ s *Store }

func (r stepRepo) Create(step *domain.Step) error {
	step.ID = uuid.New()
	step.CreatedAt = Now()
	step.UpdatedAt = Now()
	q := `INSERT INTO steps(id,run_id,name,step_type,status,iteration,attempt,when_cond,inputs_json,created_at,updated_at)
	      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	_, err := r.s.db.Exec(q, step.ID, step.RunID, step.Name, step.StepType, step.Status,
		step.Iteration, step.Attempt, step.When, step.InputsJSON, step.CreatedAt, step.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create step: %w", err)
	}
	return nil
}

func (r stepRepo) Get(id uuid.UUID) (*domain.Step, error) {
	q := `SELECT id,run_id,name,step_type,status,iteration,attempt,when_cond,inputs_json,outputs_json,skip_reason,created_at,updated_at
	      FROM steps WHERE id=$1`
	var step domain.Step
	err := r.s.db.QueryRow(q, id).Scan(
		&step.ID, &step.RunID, &step.Name, &step.StepType, &step.Status,
		&step.Iteration, &step.Attempt, &step.When, &step.InputsJSON, &step.OutputsJSON,
		&step.SkipReason, &step.CreatedAt, &step.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("step not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get step: %w", err)
	}
	return &step, nil
}

func (r stepRepo) Update(step *domain.Step) error {
	step.UpdatedAt = Now()
	q := `UPDATE steps SET status=$2,iteration=$3,attempt=$4,when_cond=$5,inputs_json=$6,outputs_json=$7,skip_reason=$8,updated_at=$9
	      WHERE id=$1`
	res, err := r.s.db.Exec(q, step.ID, step.Status, step.Iteration, step.Attempt,
		step.When, step.InputsJSON, step.OutputsJSON, step.SkipReason, step.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update step: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("step not found: %s", step.ID)
	}
	return nil
}

func (r stepRepo) ByRun(runID uuid.UUID) ([]*domain.Step, error) {
	q := `SELECT id,run_id,name,step_type,status,iteration,attempt,when_cond,inputs_json,outputs_json,skip_reason,created_at,updated_at
	      FROM steps WHERE run_id=$1 ORDER BY created_at ASC`
	rows, err := r.s.db.Query(q, runID)
	if err != nil {
		return nil, fmt.Errorf("by run: %w", err)
	}
	defer rows.Close()
	var steps []*domain.Step
	for rows.Next() {
		var step domain.Step
		err := rows.Scan(&step.ID, &step.RunID, &step.Name, &step.StepType, &step.Status,
			&step.Iteration, &step.Attempt, &step.When, &step.InputsJSON, &step.OutputsJSON,
			&step.SkipReason, &step.CreatedAt, &step.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}
		steps = append(steps, &step)
	}
	return steps, rows.Err()
}

func (r stepRepo) UpdateStatus(id uuid.UUID, to domain.StepStatus) error {
	q := `UPDATE steps SET status=$2,updated_at=$3 WHERE id=$1`
	res, err := r.s.db.Exec(q, id, to, Now())
	if err != nil {
		return fmt.Errorf("update step status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("step not found: %s", id)
	}
	return nil
}