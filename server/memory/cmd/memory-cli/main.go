// Command memory-cli is the Go port of memory_cli.py. It exposes the memory
// package over a small subcommand interface: add / search / list / feedback /
// cleanup / stats.
package main

import (
	"flag"
	"fmt"
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

func snippet(s string, n int) string {
	// A7: rune-aware truncation so CJK snippets don't end in a mojibake byte.
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
