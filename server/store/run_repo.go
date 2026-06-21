package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
)

// runRepo implements Run CRUD.
type runRepo struct{ s *Store }

func (r runRepo) Create(run *domain.Run) error {
	run.ID = uuid.New()
	run.CreatedAt = Now()
	run.UpdatedAt = Now()
	q := `INSERT INTO runs(id,name,wdl_version,status,wdl_src,max_parallel,created_at,updated_at,version)
	      VALUES($1,$2,$3,$4,$5,$6,$7,$8,1)`
	_, err := r.s.db.Exec(q,
		run.ID, run.Name, run.WDLVersion, run.Status,
		run.WDLSource, run.MaxParallel, run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (r runRepo) Get(id uuid.UUID) (*domain.Run, error) {
	q := `SELECT id,name,wdl_version,status,wdl_src,max_parallel,created_at,updated_at,ended_at,error_msg,version
	      FROM runs WHERE id=$1`
	var run domain.Run
	err := r.s.db.QueryRow(q, id).Scan(
		&run.ID, &run.Name, &run.WDLVersion, &run.Status,
		&run.WDLSource, &run.MaxParallel, &run.CreatedAt, &run.UpdatedAt,
		&run.EndedAt, &run.ErrorMsg, &run.Version)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	return &run, nil
}

func (r runRepo) Update(run *domain.Run) error {
	run.UpdatedAt = Now()
	q := `UPDATE runs SET status=$2,wdl_src=$3,max_parallel=$4,updated_at=$5,ended_at=$6,error_msg=$7,version=version+1
	      WHERE id=$1 AND version=$8`
	res, err := r.s.db.Exec(q, run.ID, run.Status, run.WDLSource, run.MaxParallel,
		run.UpdatedAt, run.EndedAt, run.ErrorMsg, run.Version)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("optimistic lock failed for run %s", run.ID)
	}
	run.Version++
	return nil
}

func (r runRepo) List(limit, offset int) ([]*domain.Run, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id,name,wdl_version,status,wdl_src,max_parallel,created_at,updated_at,ended_at,error_msg,version
	      FROM runs ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.s.db.Query(q, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var runs []*domain.Run
	for rows.Next() {
		var run domain.Run
		err := rows.Scan(&run.ID, &run.Name, &run.WDLVersion, &run.Status,
			&run.WDLSource, &run.MaxParallel, &run.CreatedAt, &run.UpdatedAt,
			&run.EndedAt, &run.ErrorMsg, &run.Version)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		runs = append(runs, &run)
	}
	return runs, rows.Err()
}

// UpdateStatus atomically updates run status with optimistic locking.
func (r runRepo) UpdateStatus(id uuid.UUID, from, to domain.RunStatus, version int) error {
	q := `UPDATE runs SET status=$3,updated_at=$4,version=version+1 WHERE id=$1 AND status=$2 AND version=$5`
	res, err := r.s.db.Exec(q, id, from, to, time.Now().UTC(), version)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("transition %s→%s not allowed for run %s", from, to, id)
	}
	return nil
}