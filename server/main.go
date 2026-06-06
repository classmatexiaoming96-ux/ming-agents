// Command server is the SHRIMP MVP Version A daemon: a Postgres-backed task
// queue that runs Claude Code via exec.Command, with a REST + WebSocket API.
//
// Usage:
//
//	server          # run the daemon in the foreground (default)
//	server run      # same as above
//	server version  # print version
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/shrimp-mvp/server/agent"
	"github.com/shrimp-mvp/server/codegraph"
	"github.com/shrimp-mvp/server/db"
	"github.com/shrimp-mvp/server/task"
)

var version = "0.1.0-mvp-a"

func main() {
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "run", "start":
		if err := run(); err != nil {
			log.Fatalf("fatal: %v", err)
		}
	case "version", "-v", "--version":
		fmt.Println("shrimp-server", version)
	default:
		fmt.Printf("unknown command %q\nusage: server [run|version]\n", cmd)
		os.Exit(2)
	}
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Printf("migrations applied")

	reg := agent.NewRegistry()
	if err := reg.Sync(ctx, pool, cfg.Agents); err != nil {
		return fmt.Errorf("sync agents: %w", err)
	}
	log.Printf("synced %d agent(s)", len(reg.All()))

	bus := NewEventBus()

	// Initialize CodeGraph CLI and registry
	codegraphCLI := codegraph.NewCodeGraphCLI(cfg.CodeGraphPath)
	registry := codegraph.NewRepoRegistry()

	daemon := NewDaemon(cfg, pool, reg, bus, codegraphCLI, registry)
	queue := task.NewQueue(pool)
	srv := NewServer(daemon, queue, reg, bus, codegraphCLI, registry)

	// HTTP server.
	httpErr := make(chan error, 1)
	go func() { httpErr <- StartHTTP(ctx, cfg.HTTPAddr, srv.Handler()) }()

	// Scheduler loop (blocks until ctx canceled).
	schedErr := make(chan error, 1)
	go func() { schedErr <- daemon.Run(ctx) }()

	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received")
	case err := <-httpErr:
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
	case err := <-schedErr:
		if err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
	}

	// Wait for the scheduler to drain in-flight tasks.
	stop()
	if err := <-schedErr; err != nil {
		return err
	}
	log.Printf("stopped cleanly")
	return nil
}
