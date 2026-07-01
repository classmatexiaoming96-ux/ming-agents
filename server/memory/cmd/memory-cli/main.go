// Command memory-cli is the Go port of memory_cli.py. It exposes the memory
// package over a small subcommand interface: add / search / list / feedback /
// cleanup / stats.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ming-agents/server/memory"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "add":
		err = cmdAdd(args)
	case "search":
		err = cmdSearch(args)
	case "list":
		err = cmdList(args)
	case "feedback":
		err = cmdFeedback(args)
	case "cleanup":
		err = cmdCleanup(args)
	case "stats":
		err = cmdStats(args)
	case "implicit":
		err = cmdImplicit(args)
	case "fts":
		err = cmdFTS(args)
	case "import-automind-summary":
		err = cmdImportAutoMindSummary(args, os.Stdout)
	case "promote":
		err = cmdPromote(args, os.Stdout)
	case "curate":
		err = cmdCurate(args, os.Stdout)
	case "list-pending-promotion":
		err = cmdListPendingPromotion(args, os.Stdout)
	case "revoke":
		err = cmdRevoke(args, os.Stdout)
	case "migrate-promotion-state":
		err = cmdMigratePromotionState(args, os.Stdout)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `memory-cli — self-evolving memory CLI

usage:
  memory-cli add <content> [--type T] [--project P] [--tags a,b] [--source S]
  memory-cli search [--query Q] [--project P] [--type T] [--tags a,b] [--min-score N] [--limit N]
  memory-cli list [--type T] [--project P] [--status S] [--limit N]
  memory-cli feedback <id> [--helpful]
  memory-cli implicit <id[,id2,...]> --log "<conversation text>"
  memory-cli fts rebuild
  memory-cli import-automind-summary <path> [--accept] [--project P] [--cross-project-policy inbox|reject]
  memory-cli promote --source <id> --to l2 --rationale "..." [--actor N] [--evidence a,b] [--override] [--apply]
  memory-cli curate --source <id> --to l1 --approver <name> --rationale "..." [--mode reject|supersede|allow_separate] [--supersedes a,b] [--apply]
  memory-cli list-pending-promotion [--project P] [--to l2|l1] [--ready-only] [--format table|json]
  memory-cli revoke --target <id> --reason "..." --actor N [--mode archive|supersede] [--superseded-by <id>] [--apply]
  memory-cli migrate-promotion-state [--apply]
  memory-cli cleanup
  memory-cli stats`)
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// reorderFlags moves flag tokens ahead of positional args so the standard flag
// package (which stops at the first positional) can parse flags written in any
// position, e.g. `add "content" --type decision`. boolFlags names the flags
// that do not consume a following value.
func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			// `--flag=value` and bool flags carry no separate value token.
			if !strings.Contains(a, "=") && !boolFlags[name] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func cmdAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	typ := fs.String("type", "", "memory type")
	project := fs.String("project", "", "project")
	tags := fs.String("tags", "", "comma-separated tags")
	source := fs.String("source", "manual", "source")
	if err := fs.Parse(reorderFlags(args, nil)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("add requires <content>")
	}
	content := strings.Join(fs.Args(), " ")
	res, err := memory.Ingest(content, *typ, *project, splitTags(*tags), *source, "")
	if err != nil {
		return err
	}
	label := "📥 inbox"
	if res.Accepted {
		label = "✅ accepted"
	}
	fmt.Printf("%s: %s\n", label, res.Reason)
	fmt.Printf("   id=%s path=%s\n", res.ID, res.Path)
	return nil
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	query := fs.String("query", "", "query keyword")
	project := fs.String("project", "", "project")
	typ := fs.String("type", "", "type")
	tags := fs.String("tags", "", "comma-separated tags")
	minScore := fs.Float64("min-score", 0, "minimum score")
	limit := fs.Int("limit", 10, "max results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	results, _, err := memory.Recall(*query, *project, *typ, splitTags(*tags), *minScore, "active", *limit)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("No results.")
		return nil
	}
	for _, r := range results {
		fmt.Printf("[%s] %s (⭐%g %s)\n", r.Type, r.Title, r.Score, r.Project)
		fmt.Printf("  %s...\n\n", snippet(r.Body, 80))
	}
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	typ := fs.String("type", "", "type")
	project := fs.String("project", "", "project")
	status := fs.String("status", "active", "status")
	limit := fs.Int("limit", 20, "max results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	results, _, err := memory.Recall("", *project, *typ, nil, 0, *status, *limit)
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("[%s] %s (⭐%g %s)\n", r.Type, r.Title, r.Score, r.Project)
	}
	return nil
}

func cmdFeedback(args []string) error {
	fs := flag.NewFlagSet("feedback", flag.ExitOnError)
	helpful := fs.Bool("helpful", false, "mark as helpful")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"helpful": true})); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("feedback requires <id>")
	}
	res, err := memory.Feedback(fs.Arg(0), true, *helpful)
	if err != nil {
		return err
	}
	fmt.Printf("id=%s hit_count=%d score=%g\n", res.ID, res.HitCount, res.Score)
	return nil
}

func cmdCleanup(args []string) error {
	res, err := memory.Cleanup()
	if err != nil {
		return err
	}
	fmt.Printf("Archived %d memories | Resolved %d contradictions\n", res.Archived, res.Resolved)
	return nil
}

func cmdStats(args []string) error {
	total, active, archived, superseded, byType, err := memory.Stats()
	if err != nil {
		return err
	}
	fmt.Printf("Total: %d | Active: %d | Archived: %d | Superseded: %d\n", total, active, archived, superseded)
	fmt.Printf("By type: %v\n", byType)
	return nil
}

func cmdImplicit(args []string) error {
	fs := flag.NewFlagSet("implicit", flag.ExitOnError)
	log := fs.String("log", "", "conversation log (reply text for this turn)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{})); err != nil {
		return err
	}
	if *log == "" {
		return fmt.Errorf("implicit requires --log")
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("implicit requires <id[,id2,...]>")
	}
	ids := strings.Split(fs.Arg(0), ",")
	results, err := memory.ImplicitFeedback(ids, *log)
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("id=%s found=%v outcome=%s ref=%.2f hit_count=%d score=%.1f pending=%.2f\n",
			r.ID, r.Found, r.Outcome, r.Reference, r.HitCount, r.Score, r.Pending)
	}
	return nil
}

func cmdFTS(args []string) error {
	fs := flag.NewFlagSet("fts", flag.ExitOnError)
	rebuild := fs.Bool("rebuild", false, "rebuild the FTS5 index from vault")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rebuild {
		count, err := memory.RebuildIndex()
		if err != nil {
			return err
		}
		fmt.Printf("FTS5 index rebuilt: %d memories indexed\n", count)
		return nil
	}
	fs.Usage()
	return fmt.Errorf("fts: specify --rebuild")
}

func cmdImportAutoMindSummary(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("import-automind-summary", flag.ExitOnError)
	accept := fs.Bool("accept", false, "write routed memories and bundles")
	project := fs.String("project", "", "override summary project")
	crossProjectPolicy := fs.String("cross-project-policy", "inbox", "inbox or reject")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"accept": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("import-automind-summary requires <path>")
	}
	result, err := memory.ImportSummary(fs.Arg(0), memory.SummaryImportOptions{
		Accept:             *accept,
		ProjectOverride:    *project,
		CrossProjectPolicy: *crossProjectPolicy,
	})
	if err != nil {
		return err
	}
	mode := "accept"
	if result.DryRun {
		mode = "dry-run"
	}
	fmt.Fprintf(out, "AutoMind summary import %s: l2=%d l3=%d inbox=%d\n", mode, result.L2, result.L3, result.Inbox)
	for _, route := range result.Routes {
		status := "planned"
		if route.Written {
			status = "written"
		}
		if route.Path != "" {
			fmt.Fprintf(out, "- %s -> %s %s %s\n", route.Kind, route.Target, status, route.Path)
			continue
		}
		fmt.Fprintf(out, "- %s -> %s %s\n", route.Kind, route.Target, status)
	}
	return nil
}

func cmdPromote(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("promote", flag.ExitOnError)
	source := fs.String("source", "", "source memory id")
	to := fs.String("to", "l2", "target layer (l2)")
	rationale := fs.String("rationale", "", "why this promotion is justified")
	actor := fs.String("actor", "", "actor name")
	evidence := fs.String("evidence", "", "comma-separated evidence refs")
	override := fs.Bool("override", false, "human single-run override")
	apply := fs.Bool("apply", false, "write changes (default is dry-run)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"override": true, "apply": true})); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("promote requires --source")
	}
	if *rationale == "" {
		return fmt.Errorf("promote requires --rationale")
	}
	result, err := memory.Promote(memory.PromotionRequest{
		SourceID:      *source,
		TargetLayer:   *to,
		Rationale:     *rationale,
		Actor:         memory.PromotionActor{Kind: "human", Name: *actor},
		EvidenceRefs:  splitTags(*evidence),
		HumanOverride: *override,
		DryRun:        !*apply,
	})
	if err != nil {
		return err
	}
	printPromotionResult(out, "promote", result)
	return nil
}

func cmdCurate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("curate", flag.ExitOnError)
	source := fs.String("source", "", "source L2 memory id")
	to := fs.String("to", "l1", "target layer (l1)")
	approver := fs.String("approver", "", "human approver name (required)")
	rationale := fs.String("rationale", "", "why this belongs in global memory")
	mode := fs.String("mode", "", "conflict mode: supersede|allow_separate")
	supersedes := fs.String("supersedes", "", "comma-separated L1 ids to supersede")
	apply := fs.Bool("apply", false, "write changes (default is dry-run)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"apply": true})); err != nil {
		return err
	}
	if *to != "l1" {
		return fmt.Errorf("curate only supports --to l1")
	}
	if *source == "" {
		return fmt.Errorf("curate requires --source")
	}
	if *approver == "" {
		return fmt.Errorf("curate requires --approver")
	}
	if *rationale == "" {
		return fmt.Errorf("curate requires --rationale")
	}
	result, err := memory.Curate(memory.CurationRequest{
		SourceID:     *source,
		Rationale:    *rationale,
		Approver:     memory.PromotionActor{Kind: "human", Name: *approver},
		ConflictMode: *mode,
		Supersedes:   splitTags(*supersedes),
		DryRun:       !*apply,
	})
	if err != nil {
		return err
	}
	printPromotionResult(out, "curate", result)
	return nil
}

func cmdListPendingPromotion(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("list-pending-promotion", flag.ExitOnError)
	project := fs.String("project", "", "project filter")
	to := fs.String("to", "l2", "target layer: l2|l1")
	readyOnly := fs.Bool("ready-only", false, "hide blocked candidates")
	format := fs.String("format", "table", "table|json")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"ready-only": true})); err != nil {
		return err
	}
	pending, err := memory.ListPending(memory.PromotionFilter{
		Project:   *project,
		ToLayer:   *to,
		ReadyOnly: *readyOnly,
	})
	if err != nil {
		return err
	}
	if *format == "json" {
		data, err := json.MarshalIndent(pending, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	if len(pending) == 0 {
		fmt.Fprintln(out, "No pending promotions.")
		return nil
	}
	for _, p := range pending {
		status := "blocked"
		if p.Eligible {
			status = "eligible"
		} else if p.ReadyForReview {
			status = "ready-for-review"
		}
		fmt.Fprintf(out, "[%s->%s] %s %s (%s)\n", p.FromLayer, p.ToLayer, p.ID, p.Title, status)
		if len(p.BlockingReasons) > 0 {
			fmt.Fprintf(out, "  blocked: %s\n", strings.Join(p.BlockingReasons, ", "))
		}
	}
	return nil
}

func cmdRevoke(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	target := fs.String("target", "", "target memory id")
	reason := fs.String("reason", "", "why this memory is revoked")
	actor := fs.String("actor", "", "actor name")
	mode := fs.String("mode", "archive", "archive|supersede")
	supersededBy := fs.String("superseded-by", "", "replacement id (supersede mode)")
	apply := fs.Bool("apply", false, "write changes (default is dry-run)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"apply": true})); err != nil {
		return err
	}
	if *target == "" {
		return fmt.Errorf("revoke requires --target")
	}
	if *reason == "" {
		return fmt.Errorf("revoke requires --reason")
	}
	result, err := memory.Revoke(memory.RevokeRequest{
		TargetID:     *target,
		Reason:       *reason,
		Mode:         *mode,
		SupersededBy: *supersededBy,
		Actor:        memory.PromotionActor{Kind: "human", Name: *actor},
		DryRun:       !*apply,
	})
	if err != nil {
		return err
	}
	mode2 := "apply"
	if result.DryRun {
		mode2 = "dry-run"
	}
	fmt.Fprintf(out, "revoke %s: %s %s -> %s", mode2, result.TargetID, result.FromState, result.ToState)
	if result.AuditEventID != "" {
		fmt.Fprintf(out, " audit=%s", result.AuditEventID)
	}
	fmt.Fprintln(out)
	return nil
}

func cmdMigratePromotionState(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate-promotion-state", flag.ExitOnError)
	apply := fs.Bool("apply", false, "write changes (default is dry-run)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"apply": true})); err != nil {
		return err
	}
	result, err := memory.BackfillPromotionState(!*apply)
	if err != nil {
		return err
	}
	mode := "apply"
	if result.DryRun {
		mode = "dry-run"
	}
	fmt.Fprintf(out, "migrate-promotion-state %s: scanned=%d updated=%d\n", mode, result.Scanned, result.Updated)
	return nil
}

func printPromotionResult(out io.Writer, verb string, result *memory.PromotionResult) {
	mode := "apply"
	if result.DryRun {
		mode = "dry-run"
	}
	fmt.Fprintf(out, "%s %s: %s -> %s (%s -> %s)", verb, mode, result.SourceID, result.TargetID, result.FromState, result.ToState)
	if result.AuditEventID != "" {
		fmt.Fprintf(out, " audit=%s", result.AuditEventID)
	}
	fmt.Fprintln(out)
	if result.ConflictReport.HasBlockingConflict {
		fmt.Fprintf(out, "  conflicts: %s\n", strings.Join(result.ConflictReport.PossibleConflicts, ", "))
	}
}

func snippet(s string, n int) string {
	// A7: rune-aware truncation so CJK snippets don't end in a mojibake byte.
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
