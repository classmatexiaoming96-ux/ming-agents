package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/shrimp-mvp/server/agent"
)

// Config is the fully-resolved daemon configuration. Everything is sourced from
// env vars (and an optional JSON file for the agent list) — no hardcoded agents.
type Config struct {
	DatabaseURL       string
	HTTPAddr          string
	WorkerID          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	OrphanTimeout     time.Duration
	ClaudeCommand     string
	ClaudeArgs        []string
	Agents            []agent.Config
}

// fileConfig is the optional JSON file (SHRIMP_CONFIG) holding the agent list
// and optional default claude command/args.
type fileConfig struct {
	ClaudeCommand string         `json:"claude_command,omitempty"`
	ClaudeArgs    []string       `json:"claude_args,omitempty"`
	Agents        []agent.Config `json:"agents"`
}

func LoadConfig() (*Config, error) {
	c := &Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		HTTPAddr:          envOr("SHRIMP_HTTP_ADDR", ":8080"),
		WorkerID:          os.Getenv("SHRIMP_WORKER_ID"),
		PollInterval:      envDuration("SHRIMP_POLL_INTERVAL", time.Second),
		HeartbeatInterval: envDuration("SHRIMP_HEARTBEAT_INTERVAL", 5*time.Second),
		OrphanTimeout:     envDuration("SHRIMP_ORPHAN_TIMEOUT", 30*time.Second),
		ClaudeCommand:     envOr("SHRIMP_CLAUDE_CMD", "claude"),
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.WorkerID == "" {
		host, _ := os.Hostname()
		c.WorkerID = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	// Default args: non-interactive print mode with the configured model.
	c.ClaudeArgs = []string{"-p", "--model", "{{model}}"}

	if path := os.Getenv("SHRIMP_CONFIG"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read SHRIMP_CONFIG: %w", err)
		}
		var fc fileConfig
		if err := json.Unmarshal(raw, &fc); err != nil {
			return nil, fmt.Errorf("parse SHRIMP_CONFIG: %w", err)
		}
		if fc.ClaudeCommand != "" {
			c.ClaudeCommand = fc.ClaudeCommand
		}
		if len(fc.ClaudeArgs) > 0 {
			c.ClaudeArgs = fc.ClaudeArgs
		}
		c.Agents = fc.Agents
	}

	if len(c.Agents) == 0 {
		// Single default agent so the daemon is usable out of the box.
		c.Agents = []agent.Config{{
			Name:               envOr("SHRIMP_DEFAULT_AGENT", "default"),
			MaxConcurrentTasks: envInt("SHRIMP_DEFAULT_CONCURRENCY", 2),
			Model:              envOr("SHRIMP_DEFAULT_MODEL", "claude-opus-4-8"),
			ThinkingLevel:      envOr("SHRIMP_DEFAULT_THINKING", "medium"),
		}}
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
