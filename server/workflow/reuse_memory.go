package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ming-agents/server/memory"
)

const reuseProject = "ming-agents"

func recallReuseHits(query string, limit int) ([]ReuseHit, error) {
	memories, _, err := memory.Recall(query, reuseProject, "", nil, 0, "active", limit, true)
	if err != nil {
		return nil, err
	}
	hits := make([]ReuseHit, 0, len(memories))
	for _, mem := range memories {
		hits = append(hits, ReuseHit{
			MemoryID: mem.ID,
			Title:    mem.Title,
			Score:    mem.Score,
			WhyUsed:  reuseSnippet(mem.Body),
		})
	}
	return hits, nil
}

func writeReuseMarkdown(repoRoot, runID, phase string, hits []ReuseHit) (string, error) {
	runRoot := filepath.Join(repoRoot, ".workflow", "runs", runID)
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(runRoot, "reuse.md")
	return path, writeTextAtomic(path, renderReuseMarkdown(runID, phase, hits))
}

func renderReuseMarkdown(runID, phase string, hits []ReuseHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Reuse for %s\n\n", phase)
	fmt.Fprintf(&b, "run_id: %s\n", runID)
	fmt.Fprintf(&b, "phase: %s\n", phase)
	fmt.Fprintf(&b, "generated_at: %s\n\n", time.Now().Format(time.RFC3339))
	b.WriteString("## Memory Hits\n\n")
	if len(hits) == 0 {
		b.WriteString("No reusable memories found for this phase.\n")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "### %d. %s\n\n", i+1, hit.Title)
		fmt.Fprintf(&b, "- memory_id: %s\n", hit.MemoryID)
		fmt.Fprintf(&b, "- score: %.3f\n", hit.Score)
		if hit.WhyUsed != "" {
			fmt.Fprintf(&b, "- why_used: %s\n", hit.WhyUsed)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func reuseSnippet(body string) string {
	body = strings.TrimSpace(strings.Join(strings.Fields(body), " "))
	if len([]rune(body)) <= 180 {
		return body
	}
	runes := []rune(body)
	return string(runes[:180]) + "..."
}
