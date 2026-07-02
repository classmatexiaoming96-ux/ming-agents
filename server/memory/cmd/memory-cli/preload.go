package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ming-agents/server/memory"
	"gopkg.in/yaml.v3"
)

// preloadEntry is one operator-authored seed memory. It mirrors IngestOptions
// but is decoded from YAML/JSON/Markdown frontmatter so an operator can seed
// L1/L2/L3 authority without going through low-level add/import.
type preloadEntry struct {
	Content string   `yaml:"content" json:"content"`
	Body    string   `yaml:"body" json:"body"` // alias for content
	Type    string   `yaml:"type" json:"type"`
	Project string   `yaml:"project" json:"project"`
	Tags    []string `yaml:"tags" json:"tags"`
	Source  string   `yaml:"source" json:"source"`
	Title   string   `yaml:"title" json:"title"`

	Inject            string   `yaml:"inject" json:"inject"`
	Layer             string   `yaml:"layer" json:"layer"`
	ExperienceKind    string   `yaml:"experience_kind" json:"experience_kind"`
	SourceSystem      string   `yaml:"source_system" json:"source_system"`
	SourceGranularity string   `yaml:"source_granularity" json:"source_granularity"`
	ScopeProject      string   `yaml:"scope_project" json:"scope_project"`
	ScopeRunID        string   `yaml:"scope_run_id" json:"scope_run_id"`
	ScopePhase        string   `yaml:"scope_phase" json:"scope_phase"`
	Parents           []string `yaml:"parents" json:"parents"`
	BlockedParents    []string `yaml:"blocked_parents" json:"blocked_parents"`
}

// preloadDoc is the YAML/JSON document shape that wraps entries under a
// top-level "memories" key.
type preloadDoc struct {
	Memories []preloadEntry `yaml:"memories" json:"memories"`
}

// cmdPreload lets an operator seed L1/L2/L3 memories in batch from a
// YAML/JSON/Markdown file. It is a thin batch wrapper around IngestWithOptions:
// no bundled domain knowledge, only operator-provided content. Duplicate
// content resolves to the same id and preserves prior counters (idempotent).
func cmdPreload(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("preload", flag.ExitOnError)
	project := fs.String("project", "", "override project for all entries")
	layer := fs.String("layer", "", "default layer when an entry omits one (l1|l2|l3)")
	source := fs.String("source", "preloaded", "default source for entries")
	inject := fs.String("inject", "", "default inject mode when an entry omits one")
	dryRun := fs.Bool("dry-run", false, "parse and report without writing")
	strict := fs.Bool("strict", false, "fail on any invalid entry instead of skipping")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"dry-run": true, "strict": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("preload requires exactly one <file.yaml|file.json|file.md>")
	}
	path := fs.Arg(0)
	entries, err := parsePreloadFile(path)
	if err != nil {
		return err
	}

	mode := "apply"
	if *dryRun {
		mode = "dry-run"
	}

	seeded := 0
	skipped := 0
	for i, e := range entries {
		content := e.Content
		if content == "" {
			content = e.Body
		}
		content = strings.TrimSpace(content)
		if content == "" {
			if *strict {
				return fmt.Errorf("preload entry %d has empty content", i)
			}
			skipped++
			continue
		}
		opts := memory.IngestOptions{
			Type:              e.Type,
			Project:           firstNonEmpty(*project, e.Project),
			Tags:              e.Tags,
			Source:            firstNonEmpty(e.Source, *source),
			Title:             e.Title,
			Inject:            firstNonEmpty(e.Inject, *inject),
			Layer:             firstNonEmpty(e.Layer, *layer),
			ExperienceKind:    e.ExperienceKind,
			SourceSystem:      e.SourceSystem,
			SourceGranularity: e.SourceGranularity,
			ScopeProject:      e.ScopeProject,
			ScopeRunID:        e.ScopeRunID,
			ScopePhase:        e.ScopePhase,
			Parents:           e.Parents,
			BlockedParents:    e.BlockedParents,
		}
		if *dryRun {
			fmt.Fprintf(out, "- would seed [%s/%s] %s\n", opts.Layer, opts.Source, snippet(content, 60))
			seeded++
			continue
		}
		res, err := memory.IngestWithOptions(content, opts)
		if err != nil {
			if *strict {
				return fmt.Errorf("preload entry %d: %w", i, err)
			}
			skipped++
			continue
		}
		fmt.Fprintf(out, "- seeded id=%s layer=%s path=%s\n", res.ID, opts.Layer, res.Path)
		seeded++
	}
	fmt.Fprintf(out, "preload %s: seeded=%d skipped=%d\n", mode, seeded, skipped)
	return nil
}

// parsePreloadFile decodes a preload file by extension: .md uses frontmatter
// (single memory), .json uses a JSON array or {memories:[...]}, everything else
// is YAML (list or {memories:[...]}).
func parsePreloadFile(path string) ([]preloadEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preload file: %w", err)
	}
	switch {
	case strings.HasSuffix(path, ".md"):
		return parseMarkdownPreload(raw)
	case strings.HasSuffix(path, ".json"):
		return parseJSONPreload(raw)
	default:
		return parseYAMLPreload(raw)
	}
}

// parseYAMLPreload accepts either a top-level list of entries or a document
// with a "memories" key.
func parseYAMLPreload(raw []byte) ([]preloadEntry, error) {
	var list []preloadEntry
	if err := yaml.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	var doc preloadDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml preload: %w", err)
	}
	if len(doc.Memories) == 0 {
		return nil, fmt.Errorf("yaml preload contains no memories")
	}
	return doc.Memories, nil
}

// parseJSONPreload accepts either a JSON array or {"memories":[...]}.
func parseJSONPreload(raw []byte) ([]preloadEntry, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var list []preloadEntry
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("parse json preload: %w", err)
		}
		return list, nil
	}
	var doc preloadDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse json preload: %w", err)
	}
	if len(doc.Memories) == 0 {
		return nil, fmt.Errorf("json preload contains no memories")
	}
	return doc.Memories, nil
}

// parseMarkdownPreload reads a single YAML frontmatter block and uses the body
// after it as the memory content.
func parseMarkdownPreload(raw []byte) ([]preloadEntry, error) {
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("markdown preload must start with a --- frontmatter block")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("markdown preload frontmatter is not closed with ---")
	}
	fm := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	var entry preloadEntry
	if err := yaml.Unmarshal([]byte(fm), &entry); err != nil {
		return nil, fmt.Errorf("parse markdown frontmatter: %w", err)
	}
	if entry.Content == "" && entry.Body == "" {
		entry.Content = strings.TrimSpace(body)
	}
	return []preloadEntry{entry}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
