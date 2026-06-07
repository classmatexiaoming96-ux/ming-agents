// Package agent defines agent configuration and the in-memory registry that is
// synced into the agents table on startup. Agents are never hardcoded — they
// come from config (see server/config.go).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config is one agent as described by configuration (env/JSON file).
type Config struct {
	Name               string   `json:"name"`
	RuntimeMode        string   `json:"runtime_mode"`
	MaxConcurrentTasks int      `json:"max_concurrent_tasks"`
	Model              string   `json:"model"`
	ThinkingLevel      string   `json:"thinking_level"`
	// Command/Args override how the child process is launched for this agent.
	// Empty Command falls back to the daemon-wide claude command. Args support
	// the {{model}} placeholder.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func (c Config) withDefaults() Config {
	if c.RuntimeMode == "" {
		c.RuntimeMode = "exec"
	}
	if c.MaxConcurrentTasks <= 0 {
		c.MaxConcurrentTasks = 1
	}
	if c.Model == "" {
		c.Model = "claude-opus-4-8"
	}
	if c.ThinkingLevel == "" {
		c.ThinkingLevel = "medium"
	}
	return c
}

// Agent is a registered agent with its database id resolved.
type Agent struct {
	ID int64
	Config
}

// Registry holds the live set of agents keyed by id and name.
type Registry struct {
	mu     sync.RWMutex
	byID   map[int64]*Agent
	byName map[string]*Agent
}

func NewRegistry() *Registry {
	return &Registry{byID: map[int64]*Agent{}, byName: map[string]*Agent{}}
}

// Sync upserts each configured agent into the agents table and loads back the
// assigned ids, populating the registry.
func (r *Registry) Sync(ctx context.Context, pool *pgxpool.Pool, configs []Config) error {
	if len(configs) == 0 {
		return fmt.Errorf("no agents configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, raw := range configs {
		c := raw.withDefaults()
		argsJSON, _ := json.Marshal(c.Args)
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO agents (name, runtime_mode, max_concurrent_tasks, model, thinking_level, command, args)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (name) DO UPDATE SET
				runtime_mode         = EXCLUDED.runtime_mode,
				max_concurrent_tasks = EXCLUDED.max_concurrent_tasks,
				model                = EXCLUDED.model,
				thinking_level       = EXCLUDED.thinking_level,
				command              = EXCLUDED.command,
				args                 = EXCLUDED.args
			RETURNING id`,
			c.Name, c.RuntimeMode, c.MaxConcurrentTasks, c.Model, c.ThinkingLevel, c.Command, string(argsJSON),
		).Scan(&id)
		if err != nil {
			return fmt.Errorf("upsert agent %q: %w", c.Name, err)
		}
		// Reload command and args from DB to get the persisted values (including defaults)
		var dbCommand string
		var dbArgsJSON string
		if err := pool.QueryRow(ctx, `SELECT command, args FROM agents WHERE id = $1`, id).Scan(&dbCommand, &dbArgsJSON); err != nil {
			return fmt.Errorf("reload agent %q: %w", c.Name, err)
		}
		c.Command = dbCommand
		json.Unmarshal([]byte(dbArgsJSON), &c.Args)
		a := &Agent{ID: id, Config: c}
		r.byID[id] = a
		r.byName[c.Name] = a
	}
	return nil
}

func (r *Registry) ByID(id int64) (*Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byID[id]
	return a, ok
}

func (r *Registry) ByName(name string) (*Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byName[name]
	return a, ok
}

// IDs returns all registered agent ids (used by the scheduler to claim work).
func (r *Registry) IDs() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]int64, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	return ids
}

// All returns a snapshot of every registered agent.
func (r *Registry) All() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Agent, 0, len(r.byID))
	for _, a := range r.byID {
		out = append(out, a)
	}
	return out
}
