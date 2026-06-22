package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/api"
	"github.com/ming-agents/server/codegraph"
	pgxdb "github.com/ming-agents/server/db"
	"github.com/ming-agents/server/engine"
	"github.com/ming-agents/server/eval"
	"github.com/ming-agents/server/memory"
	"github.com/ming-agents/server/store"
)

func main() {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	addr := flag.String("addr", ":8080", "HTTP address")
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "Postgres DSN")
	cleanup := flag.Bool("cleanup", false, "run retention cleanup once and exit")
	retentionDays := flag.Int("retention-days", 7, "retention period in days for cleanup of terminal runs' tasks/iterations")
	cleanupInterval := flag.Duration("cleanup-interval", 24*time.Hour, "background cleanup interval; 0 disables periodic cleanup")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("DATABASE_URL not set")
	}

	sqlDB, err := sql.Open("postgres", *dsn)
	if err != nil {
		log.Fatalf("open DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("ping DB: %v", err)
	}
	defer sqlDB.Close()

	pgxPool, err := pgxdb.Connect(context.Background(), *dsn)
	if err != nil {
		log.Fatalf("open pgx DB: %v", err)
	}
	defer pgxPool.Close()
	if err := pgxdb.Migrate(context.Background(), pgxPool); err != nil {
		log.Fatalf("migrate DB: %v", err)
	}

	if err := memory.InitFTS(); err != nil {
		log.Printf("memory FTS init failed; recall will fall back where possible: %v", err)
	}

	s := store.NewStore(sqlDB)

	// One-shot retention cleanup mode: prune tasks/iterations for terminal runs
	// older than the retention period, then exit.
	if *cleanup {
		cfg := store.CleanupConfig{Retention: time.Duration(*retentionDays) * 24 * time.Hour}
		res, err := s.CleanupExpired(cfg)
		if err != nil {
			log.Fatalf("cleanup: %v", err)
		}
		log.Printf("cleanup removed %d tasks, %d loop iterations (cutoff %s)",
			res.TasksDeleted, res.LoopIterationsDeleted, res.Cutoff.Format(time.RFC3339))
		return
	}

	ar := adapter.NewRegistry()
	ar.Register(adapter.FakeAdapter{})
	ar.Register(adapter.APIAdapter{BaseURL: os.Getenv("AGENT_API_URL"), APIKey: os.Getenv("AGENT_API_KEY")})

	er := eval.NewEvalRegistry()
	er.Register(&eval.MaxIterationsEvaluator{MaxIterations: 100})
	er.Register(&eval.ThresholdEvaluator{Threshold: 0.001})
	er.Register(&eval.NoProgressEvaluator{Patience: 3})

	eng := engine.NewEngine(s, ar)
	graph := codegraph.NewRepoGraph()
	srv := api.NewServer(s, eng, ar, er, api.WithCodeGraph(graph, pgxPool))

	// Background retention cleanup: periodically prune tasks/iterations for
	// terminal runs older than the retention period. Tied to the process
	// lifetime; cancelled on shutdown.
	if *cleanupInterval > 0 {
		cfg := store.CleanupConfig{Retention: time.Duration(*retentionDays) * 24 * time.Hour}
		log.Printf("background cleanup enabled: interval=%s retention=%dd", *cleanupInterval, *retentionDays)
		go s.RunPeriodicCleanup(ctx, cfg, *cleanupInterval)
	}

	log.Printf("Loop Engineering server listening on %s", *addr)
	if err := srv.ListenContext(ctx, *addr); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
