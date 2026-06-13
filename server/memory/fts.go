// Package memory is a self-evolving memory system.
package memory

// This file implements FTS5 (Full-Text Search) integration using SQLite.
// The FTS5 index lives at <repo>/.memory/memory.fts.db by default (override via
// MEMORY_FTS_DB; legacy fallback $HOME/.hermes/memory.fts.db). Go writes to it on
// Ingest; Recall uses it to pre-filter candidates before applying Go-side filters.
//
// FTS5 schema (parity with memory_api.py):
//   CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
//       id, title, body, project, mtype, tags,
//       tokenize='unicode61 remove_diacritics 1'
//   )
//
// BM25 is used for relevance ranking. Search returns results sorted by BM25
// score ascending (lower = more relevant), then the caller applies score/threshold
// filtering on the Go side.
//
// Durability/concurrency (C2): the connection is opened with WAL +
// busy_timeout so a concurrent CLI/daemon writer gets queued instead of
// SQLITE_BUSY, index mutations run inside transactions so a crash can't leave
// a memory half-indexed, and the lazy *sql.DB singleton is mutex-guarded.

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// ftsDB is the path to the shared SQLite FTS5 database. It is a var (like
// VaultDir) so tests can point it at a temp file. Precedence mirrors
// defaultVaultDir: $MEMORY_FTS_DB override, else <repo>/.memory/memory.fts.db,
// else the legacy $HOME/.hermes/memory.fts.db (via storageBase).
var ftsDB = defaultFTSDB()

func defaultFTSDB() string {
	if v := os.Getenv("MEMORY_FTS_DB"); v != "" {
		return v
	}
	return filepath.Join(storageBase(), "memory.fts.db")
}

// ftsDB_ holds the cached *sql.DB for FTS operations, lazily initialized on
// first use. ftsMu guards initialization and reset (sql.DB itself is
// goroutine-safe once created).
var (
	ftsMu  sync.Mutex
	ftsDB_ *sql.DB
)

// getFTSDB returns the cached *sql.DB, initialising it if necessary.
func getFTSDB() (*sql.DB, error) {
	ftsMu.Lock()
	defer ftsMu.Unlock()
	if ftsDB_ != nil {
		return ftsDB_, nil
	}
	if err := os.MkdirAll(filepath.Dir(ftsDB), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir fts db dir: %w", err)
	}
	// WAL lets one writer coexist with readers; busy_timeout makes a second
	// writer wait up to 5s instead of failing with SQLITE_BUSY immediately.
	dsn := "file:" + ftsDB + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open fts db: %w", err)
	}
	ftsDB_ = db
	return db, nil
}

// InitFTS creates the FTS5 virtual table if it does not already exist.
// It is idempotent — calling it multiple times is safe.
func InitFTS() error {
	conn, err := getFTSDB()
	if err != nil {
		return err
	}
	_, err = conn.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			id,
			title,
			body,
			project,
			mtype,
			tags,
			tokenize='unicode61 remove_diacritics 1'
		)
	`)
	if err != nil {
		return fmt.Errorf("create fts table: %w", err)
	}
	return nil
}

// IndexMemory inserts or replaces a memory row in the FTS5 index.
// It is called by Ingest after writing the .md file. DELETE+INSERT run in one
// transaction so a crash between them can't silently drop the row.
func IndexMemory(id, title, body, project, mtype string, tags []string) error {
	if err := InitFTS(); err != nil {
		return err
	}
	conn, err := getFTSDB()
	if err != nil {
		return err
	}
	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("fts begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("fts delete: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO memory_fts(id, title, body, project, mtype, tags) VALUES(?,?,?,?,?,?)`,
		id, title, body, project, mtype, strings.Join(tags, " "),
	); err != nil {
		return fmt.Errorf("fts insert: %w", err)
	}
	return tx.Commit()
}

// DeleteFromIndex removes a memory row from the FTS5 index. Called when a
// memory leaves the active set (archived by Cleanup, superseded by §13) so
// stale ids stop crowding the BM25 candidate window (B2).
func DeleteFromIndex(id string) error {
	conn, err := getFTSDB()
	if err != nil {
		return err
	}
	if _, err := conn.Exec(`DELETE FROM memory_fts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("fts delete %s: %w", id, err)
	}
	return nil
}

// escapeFTSQuery turns arbitrary user text into a safe FTS5 phrase query.
// Wrapping the whole input in double quotes (with internal quotes doubled)
// prevents FTS5 syntax characters (-, *, ", parens, column filters) from
// being interpreted as operators and erroring out (A1).
func escapeFTSQuery(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// SearchResult holds a single FTS5 search result.
type SearchResult struct {
	ID   string
	BM25 float64
}

// Search queries the FTS5 index and returns matching memory IDs sorted by
// BM25 score ascending (most relevant first). The query is escaped to a
// phrase query, so user input can never produce an FTS5 syntax error.
//
// Pass limit <= 0 to use the default of 50.
func Search(query, project, mtype string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 50
	}
	conn, err := getFTSDB()
	if err != nil {
		return nil, err
	}

	var args []interface{}
	var where []string

	if query != "" {
		where = append(where, "memory_fts MATCH ?")
		args = append(args, escapeFTSQuery(query))
	}
	if project != "" {
		where = append(where, "project = ?")
		args = append(args, project)
	}
	if mtype != "" {
		where = append(where, "mtype = ?")
		args = append(args, mtype)
	}

	whereClause := "1=1"
	if len(where) > 0 {
		whereClause = strings.Join(where, " AND ")
	}

	sql := fmt.Sprintf(`
		SELECT id, bm25(memory_fts) as rank
		FROM memory_fts
		WHERE %s
		ORDER BY rank
		LIMIT ?
	`, whereClause)
	args = append(args, limit)

	stmt, err := conn.Prepare(sql)
	if err != nil {
		return nil, fmt.Errorf("fts prepare: %w", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(args...)
	if err != nil {
		return nil, fmt.Errorf("fts query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var id string
		var bm25 float64
		if err := rows.Scan(&id, &bm25); err != nil {
			return nil, fmt.Errorf("fts scan: %w", err)
		}
		results = append(results, SearchResult{ID: id, BM25: bm25})
	}
	return results, rows.Err()
}

// SearchIDs is a convenience wrapper that returns just the IDs from Search.
func SearchIDs(query, project, mtype string, limit int) ([]string, error) {
	results, err := Search(query, project, mtype, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids, nil
}

// RebuildIndex clears the FTS5 table and re-indexes all active memories from
// the vault, in a single transaction (a failure mid-way leaves the previous
// index intact instead of a half-empty one). Returns the number indexed.
func RebuildIndex() (int, error) {
	if err := InitFTS(); err != nil {
		return 0, err
	}
	conn, err := getFTSDB()
	if err != nil {
		return 0, err
	}

	memories, err := readAllMemories("active", "")
	if err != nil {
		return 0, err
	}

	tx, err := conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("fts begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM memory_fts`); err != nil {
		return 0, fmt.Errorf("clear fts: %w", err)
	}

	count := 0
	for _, m := range memories {
		if _, err := tx.Exec(
			`INSERT INTO memory_fts(id, title, body, project, mtype, tags) VALUES(?,?,?,?,?,?)`,
			m.ID, m.Title, m.Body, m.Project, m.Type, strings.Join(m.Tags, " "),
		); err != nil {
			return 0, fmt.Errorf("fts rebuild insert %s: %w", m.ID, err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// CloseFTS releases the cached SQLite connection.
// Call this at program shutdown, or in tests after repointing ftsDB.
func CloseFTS() error {
	ftsMu.Lock()
	defer ftsMu.Unlock()
	if ftsDB_ == nil {
		return nil
	}
	err := ftsDB_.Close()
	ftsDB_ = nil
	return err
}
