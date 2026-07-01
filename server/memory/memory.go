// Package memory is a self-evolving memory system, rewritten in Go from the
// original memory_api.py. It scores incoming notes, files them into an Obsidian
// vault (notes / inbox / archive), retrieves them with simple filters, records
// usage feedback, and archives expired entries.
//
// The vault defaults to <repo>/.memory/vault so the data lives alongside the
// code base; set MEMORY_VAULT_DIR to override, or fall back to the legacy
// $HOME/.hermes/vault when no repo root is found. Override VaultDir for tests.
package memory

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// VaultDir is the root of the Obsidian vault. It defaults to
// <repo>/.memory/vault so the memory data lives alongside the code, and may be
// reassigned (e.g. in tests) or overridden via MEMORY_VAULT_DIR.
var VaultDir = defaultVaultDir()

// defaultVaultDir resolves the vault root. Precedence:
//  1. $MEMORY_VAULT_DIR if set (explicit override).
//  2. <repo>/.memory/vault — keeps memory data next to the code base.
//  3. $HOME/.hermes/vault — backward-compatible fallback when no repo root is
//     found (preserves the legacy location for existing deployments).
func defaultVaultDir() string {
	if v := os.Getenv("MEMORY_VAULT_DIR"); v != "" {
		return v
	}
	return filepath.Join(storageBase(), "vault")
}

// storageBase is the directory that holds the vault and the FTS index. It is
// <repo>/.memory when a repository root can be located by walking up from the
// working directory for a .git marker; otherwise it falls back to $HOME/.hermes
// so the legacy paths ($HOME/.hermes/vault and $HOME/.hermes/memory.fts.db)
// keep working unchanged.
func storageBase() string {
	if root := findRepoRoot(); root != "" {
		return filepath.Join(root, ".memory")
	}
	return os.ExpandEnv("$HOME/.hermes")
}

// findRepoRoot walks up from the current working directory looking for a .git
// entry (a directory in a normal clone, a file in a worktree/submodule). It
// returns "" if none is found before the filesystem root.
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// TypeTTL maps a memory type to its lifetime in days. A value of 0 means the
// memory never expires.
var TypeTTL = map[string]int{
	"decision":    0, // 永久
	"gotcha":      0, // 永久
	"incident":    365,
	"requirement": 180,
	"snippet":     90,
	"meeting":     30,
	"agent-trace": 7,
}

const (
	// scoreThreshold is the cutoff between vault/notes and vault/inbox.
	scoreThreshold = 3.0
	// neverExpires is the sentinel expiry for permanent memories.
	neverExpires = "9999-12-31"
	dateLayout   = "2006-01-02"
)

// sourceScores mirrors the credibility weights from the Python version.
var sourceScores = map[string]float64{
	"manual":        1.0,
	"code-review":   0.9,
	"debug-session": 0.8,
	"agent-run":     0.7,
	"meeting":       0.6,
}

// Memory is a single stored note. The yaml-tagged fields are persisted as
// frontmatter; Body holds the markdown text after the frontmatter block.
type Memory struct {
	ID          string   `yaml:"id"`
	Type        string   `yaml:"type"` // decision/incident/snippet/requirement/meeting/gotcha/agent-trace
	Project     string   `yaml:"project"`
	Tags        []string `yaml:"tags"`
	Title       string   `yaml:"title"`
	Score       float64  `yaml:"score"`
	Novelty     float64  `yaml:"novelty"`
	Specificity float64  `yaml:"specificity"`
	Reusability float64  `yaml:"reusability"`
	HitCount    int      `yaml:"hit_count"`
	CreatedAt   string   `yaml:"created_at"`
	ExpiresAt   string   `yaml:"expires_at"`
	Status      string   `yaml:"status"` // active/archived/superseded
	Source      string   `yaml:"source"`
	Links       []string `yaml:"links"`
	Layer       string   `yaml:"layer,omitempty"`

	SourceSystem      string `yaml:"source_system,omitempty"`
	SourceGranularity string `yaml:"source_granularity,omitempty"`
	EvidenceRef       string `yaml:"evidence_ref,omitempty"`
	CrossProject      bool   `yaml:"_cross_project,omitempty"`

	// §SHRIMP inject controls whether this memory is auto-injected into context.
	// "always" = inject on every recall regardless of query (token-capped by budget).
	// "query"  = inject only when matched by query (default, backwards-compatible).
	// "never"  = never auto-inject (manual recall only).
	// Orthogonal to Type; does not affect scoring or contradiction logic.
	Inject string `yaml:"inject,omitempty"`

	// §13 explicit 证据（Phase 1）— 与 implicit 解耦，explicit-trumps tier 读取。
	// 旧文件无此字段 → 零值；历史上被 explicit feedback 过的老 memory 也算 0
	// （无法回填），explicit-trumps 对它们不生效，按 composite tier 裁决。
	ExplicitHits int    `yaml:"explicit_hits,omitempty"`
	LastExplicit string `yaml:"last_explicit,omitempty"`

	// §7 implicit（Phase 3 依赖，提前加字段，本期不加逻辑）。
	ImplicitHits int     `yaml:"implicit_hits,omitempty"`
	PendingScore float64 `yaml:"pending_score,omitempty"`
	LastImplicit string  `yaml:"last_implicit,omitempty"`
	PromotedHits int     `yaml:"promoted_hits,omitempty"` // §13: implicit hits that survived probation → score

	// §13 矛盾淘汰（Phase 0）。全部 omitempty：旧文件缺失 → 零值，向后兼容。
	ConflictsWith    []string `yaml:"conflicts_with,omitempty"`    // 待裁决冲突伙伴 id（§7 在线标记写入）
	SupersededBy     string   `yaml:"superseded_by,omitempty"`     // winner id（写在 loser 上）
	Supersedes       []string `yaml:"supersedes,omitempty"`        // loser ids（写在 winner 上）
	SupersededAt     string   `yaml:"superseded_at,omitempty"`     // 日期（dateLayout）
	SupersededReason string   `yaml:"superseded_reason,omitempty"` // 裁决理由

	Body string `yaml:"-"` // 正文（不含 frontmatter）
	Path string `yaml:"-"` // on-disk location, populated on read
}

// Result is returned by Ingest.
type Result struct {
	Accepted bool    `json:"accepted"`
	Score    float64 `json:"score"`
	ID       string  `json:"id"`
	Path     string  `json:"path"`
	Reason   string  `json:"reason"`
}

// FeedbackResult is returned by Feedback.
type FeedbackResult struct {
	ID       string  `json:"id"`
	HitCount int     `json:"hit_count"`
	Score    float64 `json:"score"`
}

// CleanupResult is returned by Cleanup.
type CleanupResult struct {
	Archived int `json:"archived"`
	Resolved int `json:"resolved"` // §13 contradictions superseded this pass
}

var (
	frontmatterRE = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n(.*)`)
	tagRE         = regexp.MustCompile(`\b[a-z]{3,}(?:\.[a-z]{2,})?\b`)
	bigNumberRE   = regexp.MustCompile(`\d{4,}`)
	codeRE        = regexp.MustCompile("```|`")
)

// now is indirected so tests can pin the clock.
var now = time.Now

// computeID derives a content-addressed id from the FULL content (A4).
//
// The previous version hashed only the first 200 bytes, so two memories sharing
// a templated prefix collided and silently overwrote each other (and their
// accumulated evidence counters). Hashing the whole body removes that class of
// collision; the id is widened to 16 hex (64 bits) to push random collisions far
// beyond any realistic vault size. Hashing bytes of the whole string is also
// rune-safe — there is no truncation point to split a multi-byte character.
func computeID(content string) string {
	sum := md5.Sum([]byte(content))
	return "mem_" + hex.EncodeToString(sum[:])[:16]
}

// truncateRunes returns the first n runes of s (A7). Plain byte slicing splits
// multi-byte UTF-8 characters (every CJK char is 3 bytes), producing invalid
// runes in titles/snippets; rune slicing never does.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// parseFrontmatter splits a yaml frontmatter block from the body.
func parseFrontmatter(text string) (Memory, string, error) {
	m := frontmatterRE.FindStringSubmatch(text)
	if m == nil {
		return Memory{}, text, nil
	}
	var mem Memory
	if err := yaml.Unmarshal([]byte(m[1]), &mem); err != nil {
		return Memory{}, "", fmt.Errorf("parse frontmatter: %w", err)
	}
	return mem, m[2], nil
}

// writeMemory serialises a memory (frontmatter + body) into targetDir/{id}.md.
func writeMemory(mem Memory, targetDir string) (string, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", targetDir, err)
	}
	fm, err := yaml.Marshal(mem)
	if err != nil {
		return "", fmt.Errorf("marshal frontmatter: %w", err)
	}
	path := filepath.Join(targetDir, mem.ID+".md")
	content := fmt.Sprintf("---\n%s---\n%s", fm, mem.Body)
	// C1: write to a temp file then atomically rename into place. A reader (the
	// Next.js vault viewer, a concurrent Recall) therefore never observes a
	// half-written file, and a crash mid-write leaves the previous version intact
	// rather than a truncated one. Rename within the same directory is atomic on
	// POSIX filesystems.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename %s: %w", path, err)
	}
	return path, nil
}

// readAllMemories scans notes/inbox/archive, optionally filtered by project and status.
//
// Partitioning (P0-2 + A5): when project != "", the notes/ scan descends only
// into notes/{project}/ (the large subtree), but inbox/ is ALWAYS scanned and
// filtered by the frontmatter project field — inbox holds active, below-threshold
// memories that a project-scoped Recall/Brief must still see. The project filter
// is applied on the frontmatter for every subdir, so a misfiled note can't leak.
//
// status = "active" skips archive/ entirely; status = "" reads all subdirs.
//
// Nested folders are descended into (Obsidian users routinely nest notes); the
// old code's SkipDir-on-name pruning silently dropped those (A5).
//
// C3: if a crash between "write archive copy" and "remove notes copy" left the
// same id in two places, the in-memory dedup at the end keeps the later-scanned
// copy (archive wins over notes/inbox) so callers and Stats don't double-count.
func readAllMemories(status, project string) ([]Memory, error) {
	var results []Memory
	subdirs := []string{"notes", "inbox", "archive"}

	for _, sub := range subdirs {
		root := filepath.Join(VaultDir, sub)

		if sub == "archive" && status == "active" {
			continue
		}
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}

		scanRoot := root
		if sub == "notes" && project != "" {
			scanRoot = filepath.Join(root, project)
			if _, err := os.Stat(scanRoot); os.IsNotExist(err) {
				continue
			}
		}

		err := filepath.WalkDir(scanRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil // descend into nested folders
			}
			if !strings.HasSuffix(path, ".md") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			mem, body, err := parseFrontmatter(string(raw))
			if err != nil {
				return err
			}
			if status != "" && mem.Status != status {
				return nil
			}
			if project != "" && mem.Project != project {
				return nil
			}
			mem.Body = body
			mem.Path = path
			results = append(results, mem)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return dedupeByID(results), nil
}

// dedupeByID collapses duplicate ids that a crash window may have left on disk
// (C3), keeping the last occurrence. readAllMemories scans notes→inbox→archive,
// so the archived/superseded copy (the intended final location) wins over an
// orphaned active copy. In the normal no-duplicate case the input is returned
// in the same order, so determinism is preserved.
func dedupeByID(in []Memory) []Memory {
	idx := map[string]int{}
	out := make([]Memory, 0, len(in))
	for _, m := range in {
		if m.ID == "" {
			out = append(out, m)
			continue
		}
		if i, ok := idx[m.ID]; ok {
			out[i] = m // later copy wins
			continue
		}
		idx[m.ID] = len(out)
		out = append(out, m)
	}
	return out
}

// readMemoriesByIDs loads only the named memories instead of walking the whole
// vault (B1). It probes the likely on-disk locations directly — notes/{project}
// and the flat inbox/ — and resolves any remainder (other projects, nested
// folders, archive) with a single fallback walk. In the common project-scoped,
// FTS-candidate recall this performs zero walks; it is never worse than one full
// scan. Results are returned in the input id order (i.e. BM25 rank order), so a
// later stable sort preserves relevance among equal-Score memories.
func readMemoriesByIDs(ids []string, status, project string) ([]Memory, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	found := map[string]Memory{}

	probe := func(path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return
		}
		mem, body, err := parseFrontmatter(string(raw))
		if err != nil || mem.ID == "" || !want[mem.ID] {
			return
		}
		mem.Body = body
		mem.Path = path
		found[mem.ID] = mem
	}

	for id := range want {
		if project != "" {
			probe(filepath.Join(VaultDir, "notes", project, id+".md"))
		}
		probe(filepath.Join(VaultDir, "inbox", id+".md"))
	}

	// Fallback single walk only if a direct probe missed something.
	for id := range want {
		if _, ok := found[id]; !ok {
			all, err := readAllMemories(status, "")
			if err != nil {
				return nil, err
			}
			for _, m := range all {
				if want[m.ID] {
					found[m.ID] = m
				}
			}
			break
		}
	}

	var out []Memory
	seen := map[string]bool{}
	for _, id := range ids {
		m, ok := found[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if status != "" && m.Status != status {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// §6 共享词法原语：char-bigram Jaccard。CJK 友好（rune 级 2-gram），由 Ingest
// novelty 与 §13 lexicalContradictionScan 共用。取代旧的 word-set Jaccard——后者
// 用 `\w+`，对纯中文匹配空集，导致中文 memory novelty 恒为 1.0、去重失效。

// normalizeForBigram lowercases and collapses runs of whitespace to a single
// space, so layout differences don't perturb the bigram set.
func normalizeForBigram(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// charBigrams returns the set of rune-level 2-grams of s (after normalisation).
// A single-rune string contributes that one rune as a degenerate 1-gram so very
// short strings still compare meaningfully.
func charBigrams(s string) map[string]bool {
	runes := []rune(normalizeForBigram(s))
	set := map[string]bool{}
	if len(runes) == 0 {
		return set
	}
	if len(runes) == 1 {
		set[string(runes)] = true
		return set
	}
	for i := 0; i+1 < len(runes); i++ {
		set[string(runes[i:i+2])] = true
	}
	return set
}

// jaccardOfSets is |A∩B| / |A∪B| over two precomputed char-bigram sets. An empty
// set on either side (including both empty) yields 0 — a bodiless memory overlaps
// nothing — matching the prior bigramJaccard semantics so novelty scoring and the
// contradiction scan are unchanged by the precompute refactor. Callers that have
// already tokenised their inputs use this directly to avoid rebuilding the sets;
// see scanGroup, which shares one set per memory across all O(n²) comparisons.
func jaccardOfSets(setA, setB map[string]bool) float64 {
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	inter := 0
	for g := range setA {
		if setB[g] {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	return float64(inter) / float64(union)
}

// bigramJaccard is |A∩B| / |A∪B| over the two char-bigram sets. Retained for
// callers working from raw strings (Ingest's novelty); it tokenises both sides on
// every call, so hot pairwise loops should precompute and use jaccardOfSets.
func bigramJaccard(a, b string) float64 {
	return jaccardOfSets(charBigrams(a), charBigrams(b))
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true,
	"from": true, "this": true, "that": true,
}

// Budget caps the token budget for Brief injection.
// Rough estimate: 1 token ≈ 4 chars for English, 2 for CJK.
// A SafetyMargin is applied to leave room for framing and the caller.
type Budget struct {
	MaxTokens int // hard cap; Brief respects it or fails loudly
}

// DefaultBudget is the standard budget when none is specified.
// Currently 32k tokens — caller can override via Brief(project, query, Budget{MaxTokens: N}).
const DefaultBudgetTokens = 32000

// BriefAudit records what Brief did for observability.
type BriefAudit struct {
	RunID             string   // workflow run id that produced this audit
	Kind              string   // workflow node kind that produced this audit
	AlwaysCount       int      // how many inject=always memories were injected
	QueryCount        int      // how many query-matched memories were injected
	TotalTokens       int      // rough token estimate of injected content
	Truncated         bool     // true if budget forced an early stop
	TruncatedAt       string   // id of the memory where truncation occurred
	ConflictsDownrank int      // how many memories were down-ranked due to conflicts_with
	InjectedIDs       []string // ids actually injected, in order (for the implicit-feedback loop)
}

// Brief assembles a memory block for injection, subject to a token budget.
// Layer 0: inject=always memories first (token-capped).
// Layer 1: query-based recall, sorted by score, down-ranking conflicts_with entries.
// Truncation is always auditable via BriefAudit.
// Fails loudly on inject failures (does not silently skip).
func Brief(project, query string, budget Budget) (string, BriefAudit, error) {
	if budget.MaxTokens <= 0 {
		budget.MaxTokens = DefaultBudgetTokens
	}

	// Layer 0: collect all inject=always memories in the project.
	always, err := readAllMemories("active", project)
	if err != nil {
		return "", BriefAudit{}, fmt.Errorf("readAllMemories for always: %w", err)
	}
	var alwaysList []Memory
	for _, m := range always {
		if m.Inject == "always" {
			alwaysList = append(alwaysList, m)
		}
	}
	// A6: inject the highest-Score always-memories first, so that when the budget
	// is tight which ones survive is deterministic (the old code used filesystem
	// walk order, making "why wasn't this injected?" unanswerable).
	sort.SliceStable(alwaysList, func(i, j int) bool {
		return alwaysList[i].Score > alwaysList[j].Score
	})

	// Layer 1: query-based recall. downrankConflicts=true makes Recall apply the
	// Score×0.5 penalty to conflicts_with-marked memories and re-sort, so the
	// down-ranking the doc promises actually changes the injection order (A6).
	queryMemories, _, err := Recall(query, project, "", nil, 0, "active", 0, true)
	if err != nil {
		return "", BriefAudit{}, fmt.Errorf("Recall for brief: %w", err)
	}

	// Build conflict set for down-ranking.
	conflictSet := map[string]bool{}
	for _, m := range alwaysList {
		for _, cw := range m.ConflictsWith {
			conflictSet[cw] = true
		}
	}
	for _, m := range queryMemories {
		for _, cw := range m.ConflictsWith {
			conflictSet[cw] = true
		}
	}

	var injected []Memory
	injectedSet := map[string]bool{}
	audit := BriefAudit{}

	// Rough token estimate: count runes, divide by ~3.5 (mixed CJK/English).
	estimateTokens := func(s string) int {
		return len([]rune(s)) / 3
	}

	usedTokens := 0

	// Layer 0: inject always memories, respect budget.
	for _, m := range alwaysList {
		toks := estimateTokens(m.Title) + estimateTokens(m.Body)
		if usedTokens+toks > budget.MaxTokens {
			// budget exhausted — stop injecting always memories
			break
		}
		injected = append(injected, m)
		injectedSet[m.ID] = true
		usedTokens += toks
		audit.AlwaysCount++
	}
	audit.TotalTokens = usedTokens

	// Layer 1: query memories, sorted by score.
	// Down-rank those in conflictSet (they get injected at lower priority).
	// But still include them if budget allows.
	remainingBudget := budget.MaxTokens - usedTokens

	// Inject query memories until budget runs out. queryMemories already arrives
	// conflict-down-ranked and re-sorted from Recall, so iteration order reflects
	// the penalty.
	for _, m := range queryMemories {
		if injectedSet[m.ID] { // dedup against Layer 0
			continue
		}

		toks := estimateTokens(m.Title) + estimateTokens(m.Body)
		if toks > remainingBudget && remainingBudget > 0 {
			// We could still fit smaller memories but this one is too big.
			// Only truncate if this is the first memory that doesn't fit.
			audit.Truncated = true
			audit.TruncatedAt = m.ID
			break
		}

		// Down-rank: if this memory is in conflictSet, note it but still inject
		// if budget allows (conflict marker means "treat carefully", not "skip").
		if conflictSet[m.ID] {
			audit.ConflictsDownrank++
		}

		if toks <= remainingBudget {
			injected = append(injected, m)
			injectedSet[m.ID] = true
			usedTokens += toks
			remainingBudget -= toks
			audit.QueryCount++
		} else {
			// Budget exhausted
			audit.Truncated = true
			audit.TruncatedAt = m.ID
			break
		}
	}

	audit.TotalTokens = usedTokens

	// Assemble the block.
	var sb strings.Builder
	for _, m := range injected {
		// Include inject=always marker in the block for transparency.
		prefix := ""
		if m.Inject == "always" {
			prefix = "[always] "
		}
		fmt.Fprintf(&sb, "%s%s\n---\n", prefix, m.Title)
		sb.WriteString(m.Body)
		sb.WriteString("\n\n")
		audit.InjectedIDs = append(audit.InjectedIDs, m.ID)
	}

	return sb.String(), audit, nil
}

// classification holds the auto-inferred type/project/tags for content.
type classification struct {
	Type    string
	Project string
	Tags    []string
}

func containsAny(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// autoClassify infers type/project/tags from raw content (parity with Python).
func autoClassify(content string) classification {
	text := strings.ToLower(content)
	var memType string
	switch {
	case containsAny(text, "bug", "error", "crash", "panic", "fail"):
		memType = "incident"
	case containsAny(text, "为什么选", "权衡", "决定", "采用", "strategy"):
		memType = "decision"
	case strings.Contains(content, "```") || strings.Contains(content, "`"):
		memType = "snippet"
	case containsAny(text, "会议", "结论", "action", "对齐"):
		memType = "meeting"
	case containsAny(text, "需求", "prd", "feature", "requirement"):
		memType = "requirement"
	case containsAny(text, "踩坑", "坑", "约定", "注意", "gotcha"):
		memType = "gotcha"
	default:
		memType = "agent-trace"
	}

	// Extract up to 5 unique english-ish tags, skipping common stopwords.
	var tags []string
	seen := map[string]bool{}
	for _, t := range tagRE.FindAllString(text, -1) {
		if stopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
		if len(tags) == 5 {
			break
		}
	}

	project := "general"
	if cwd, err := os.Getwd(); err == nil {
		if base := filepath.Base(cwd); base != "" && base != "." && base != "/" {
			project = base
		}
	}

	return classification{Type: memType, Project: project, Tags: tags}
}

// round1 rounds to one decimal place.
func round1(x float64) float64 {
	return math.Round(x*10) / 10
}

func round2(x float64) float64 {
	return math.Round(x*100) / 100
}

// Ingest scores content and writes it to notes (accepted) or inbox (below
// threshold). Empty type/project/tags are auto-classified.
func Ingest(content, memType, project string, tags []string, source, title string) (Result, error) {
	auto := autoClassify(content)
	if memType == "" {
		memType = auto.Type
	}
	if project == "" {
		project = auto.Project
	}
	if tags == nil {
		tags = auto.Tags
	}
	if title == "" {
		title = strings.ReplaceAll(truncateRunes(content, 60), "\n", " ")
	}
	if source == "" {
		source = "manual"
	}

	id := computeID(content)
	today := now().Format(dateLayout)

	// novelty: 1 - highest similarity to any existing active memory.
	existing, err := readAllMemories("active", "")
	if err != nil {
		return Result{}, err
	}
	novelty := 1.0
	if len(existing) > 0 {
		maxSim := 0.0
		for _, m := range existing {
			if s := bigramJaccard(content, m.Body); s > maxSim {
				maxSim = s
			}
		}
		novelty = 1.0 - maxSim
	}

	// specificity: concrete details raise the score.
	specificity := 0.5
	if bigNumberRE.MatchString(content) {
		specificity += 0.1
	}
	if codeRE.MatchString(content) {
		specificity += 0.15
	}
	if containsAny(content, "因为", "所以", "决定", "原因", "why", "because") {
		specificity += 0.15
	}
	specificity = math.Min(1.0, specificity)

	reusability := math.Min(1.0, 0.5+float64(len(tags))/10)

	sourceScore, ok := sourceScores[source]
	if !ok {
		sourceScore = 0.5
	}

	score := round1((0.3*novelty + 0.3*specificity + 0.25*reusability + 0.15*sourceScore) * 5)

	expiresAt := neverExpires
	if ttl := TypeTTL[memType]; ttl > 0 {
		expiresAt = now().AddDate(0, 0, ttl).Format(dateLayout)
	}

	mem := Memory{
		ID:          id,
		Type:        memType,
		Project:     project,
		Tags:        tags,
		Title:       title,
		Score:       score,
		Novelty:     round2(novelty),
		Specificity: round2(specificity),
		Reusability: round2(reusability),
		HitCount:    0,
		CreatedAt:   today,
		ExpiresAt:   expiresAt,
		Status:      "active",
		Source:      source,
		Links:       []string{},
		Inject:      "query", // P1-3: default inject mode; omitempty keeps old files compatible
		Body:        content,
	}

	// A4: re-ingesting identical content yields the same id. Instead of resetting
	// the row (and destroying the usage/evidence counters that survivorScore and
	// §13 depend on), carry the prior counters forward and keep the original
	// creation date. existing is the active set we just read for novelty, so this
	// costs no extra I/O. (Novelty itself is intentionally still computed against
	// the full set including the prior copy, so a duplicate's score still drops.)
	for i := range existing {
		if existing[i].ID == id {
			p := existing[i]
			mem.HitCount = p.HitCount
			mem.ExplicitHits = p.ExplicitHits
			mem.LastExplicit = p.LastExplicit
			mem.ImplicitHits = p.ImplicitHits
			mem.PromotedHits = p.PromotedHits
			mem.PendingScore = p.PendingScore
			mem.LastImplicit = p.LastImplicit
			mem.CreatedAt = p.CreatedAt
			if p.Inject != "" {
				mem.Inject = p.Inject
			}
			break
		}
	}

	accepted := score >= scoreThreshold
	var targetDir string
	if accepted {
		targetDir = filepath.Join(VaultDir, "notes", project)
	} else {
		targetDir = filepath.Join(VaultDir, "inbox")
	}

	path, err := writeMemory(mem, targetDir)
	if err != nil {
		return Result{}, err
	}

	// Phase 1: update FTS5 index. Log errors so index drift is observable.
	if err := IndexMemory(id, title, content, project, memType, tags); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 index error for %s: %v\n", id, err)
	}

	reason := fmt.Sprintf("score=%g (accepted)", score)
	if !accepted {
		reason = fmt.Sprintf("score=%g (below threshold %g)", score, scoreThreshold)
	}
	return Result{Accepted: accepted, Score: score, ID: id, Path: path, Reason: reason}, nil
}

// Recall returns active memories matching the filters, highest score first.
// Empty string / nil / zero filters are ignored. status defaults to "active".
// Returns results, total (all matched before limit), error.
// The returned total lets callers know if the result was truncated.
//
// Phase 1: when query is non-empty, FTS5 BM25 is used to pre-filter candidates
// before Go-side filtering. When query is empty, all memories matching
// status/project are considered (full scan, same as before).
//
// Optional DownrankConflicts parameter (default false): when true, memories
// that appear in any other memory's conflicts_with list are penalized (score × 0.5)
// before ranking, so contradicted memories surface lower in normal recall too.
// (Brief always applies this penalty regardless of this flag.)
func Recall(query, project, memType string, tags []string, minScore float64, status string, limit int, downrankConflicts ...bool) ([]Memory, int, error) {
	if status == "" {
		status = "active"
	}

	// Phase 1: use FTS5 to get candidate IDs when query is present.
	//
	// A1: fall back to the full-scan + substring path whenever FTS does not give
	// us a usable candidate set — that means BOTH on error AND on an empty result.
	// The old code only fell back on error, so a query that FTS simply failed to
	// MATCH (very common for CJK, since unicode61 does not segment Han runs)
	// produced a non-nil empty candidate set and filtered out every memory.
	// B1: when we DO have candidates, read just those files instead of walking
	// the whole vault.
	var candidateIDs map[string]bool
	var memories []Memory
	if query != "" {
		ids, err := SearchIDs(query, project, memType, ftsCandidateLimit(limit))
		if err == nil && len(ids) > 0 {
			candidateIDs = map[string]bool{}
			for _, id := range ids {
				candidateIDs[id] = true
			}
			memories, err = readMemoriesByIDs(ids, status, project)
			if err != nil {
				return nil, 0, err
			}
		}
	}
	if candidateIDs == nil { // no query, FTS error, or zero FTS hits → full scan
		var err error
		memories, err = readAllMemories(status, project)
		if err != nil {
			return nil, 0, err
		}
	}

	var results []Memory
	for _, m := range memories {
		if !isRecallVisibleMemory(m) {
			continue
		}
		if memType != "" && m.Type != memType {
			continue
		}
		if m.Score < minScore {
			continue
		}
		if len(tags) > 0 && !anyTagMatch(tags, m.Tags) {
			continue
		}
		if query != "" {
			// FTS5 already filtered; candidateIDs == nil means FTS5 unavailable.
			// Only apply substring filter when we have NO FTS5 pre-filter.
			if candidateIDs == nil {
				q := strings.ToLower(query)
				if !strings.Contains(strings.ToLower(m.Body), q) &&
					!strings.Contains(strings.ToLower(m.Title), q) {
					continue
				}
			}
		}
		results = append(results, m)
	}

	// Optional conflict down-ranking: build the conflict set and penalize.
	if len(downrankConflicts) > 0 && downrankConflicts[0] {
		conflictSet := map[string]bool{}
		for _, m := range results {
			for _, cw := range m.ConflictsWith {
				conflictSet[cw] = true
			}
		}
		for i := range results {
			if conflictSet[results[i].ID] {
				results[i].Score *= 0.5
			}
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	total := len(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, total, nil
}

// ftsCandidateLimit sizes the BM25 candidate window (B2). It must exceed the
// caller's final limit so that Go-side filters (status/type/tags) and any stale
// rows still left in the index don't starve the result set. We over-fetch by 3×
// with a floor of 50.
func ftsCandidateLimit(limit int) int {
	n := 50
	if limit*3 > n {
		n = limit * 3
	}
	return n
}

func anyTagMatch(want, have []string) bool {
	set := map[string]bool{}
	for _, t := range have {
		set[t] = true
	}
	for _, t := range want {
		if set[t] {
			return true
		}
	}
	return false
}

// Feedback records that a memory was used: it bumps hit_count and nudges the
// score (used: +0.05, helpful: +0.1).
func Feedback(id string, used, helpful bool) (FeedbackResult, error) {
	for _, sub := range []string{"notes", "inbox"} {
		root := filepath.Join(VaultDir, sub)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		var found string
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == id+".md" {
				found = path
			}
			return nil
		})
		if err != nil {
			return FeedbackResult{}, err
		}
		if found == "" {
			continue
		}

		raw, err := os.ReadFile(found)
		if err != nil {
			return FeedbackResult{}, fmt.Errorf("read %s: %w", found, err)
		}
		mem, body, err := parseFrontmatter(string(raw))
		if err != nil {
			return FeedbackResult{}, err
		}
		mem.Body = body
		mem.HitCount++
		// §13: Feedback is the explicit-evidence channel. Record a distinct
		// explicit_hits count + recency so survivorScore's explicit-trumps tier
		// has a judgement basis separate from implicit/usage signals.
		mem.ExplicitHits++
		mem.LastExplicit = now().Format(dateLayout)
		if used {
			mem.Score = round1(mem.Score + 0.05)
		}
		if helpful {
			mem.Score = round1(mem.Score + 0.1)
		}
		if _, err := writeMemory(mem, filepath.Dir(found)); err != nil {
			return FeedbackResult{}, err
		}
		return FeedbackResult{ID: id, HitCount: mem.HitCount, Score: mem.Score}, nil
	}
	return FeedbackResult{}, fmt.Errorf("memory %q not found", id)
}

// Cleanup moves expired active memories from notes/inbox into
// archive/{project}, flipping their status to "archived". It then runs the §13
// resolution phase (existing archival/scoring math is untouched): contradiction
// candidates are gathered from online conflicts_with markers and the at-rest
// ContradictionDetector, then funneled through ResolveContradictions.
//
// The automatic pass is deliberately conservative — AutoEvict is OFF, so Cleanup
// only flags conflicts and writes the audit log; it never auto-supersedes. Actual
// eviction is an explicit operator action (memory-cli resolve). First rollout
// should still verify with ResolveOptions.DryRun before enabling eviction.
func Cleanup() (CleanupResult, error) {
	today := now().Format(dateLayout)
	count := 0
	for _, sub := range []string{"notes", "inbox"} {
		root := filepath.Join(VaultDir, sub)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		var toArchive []Memory
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			mem, body, err := parseFrontmatter(string(raw))
			if err != nil {
				return err
			}
			if mem.Status != "active" {
				return nil
			}
			if mem.ExpiresAt != "" && mem.ExpiresAt < today {
				mem.Body = body
				mem.Path = path
				toArchive = append(toArchive, mem)
			}
			return nil
		})
		if err != nil {
			return CleanupResult{}, err
		}

		for _, mem := range toArchive {
			mem.Status = "archived"
			project := mem.Project
			if project == "" {
				project = "unknown"
			}
			archiveDir := filepath.Join(VaultDir, "archive", project)
			if _, err := writeMemory(mem, archiveDir); err != nil {
				return CleanupResult{}, err
			}
			if err := os.Remove(mem.Path); err != nil {
				return CleanupResult{}, fmt.Errorf("remove %s: %w", mem.Path, err)
			}
			// B2: an archived memory leaves the active set, so drop it from the FTS
			// index — otherwise stale ids keep crowding the BM25 candidate window.
			// Non-fatal: index drift is observable and self-heals on RebuildIndex.
			if err := DeleteFromIndex(mem.ID); err != nil {
				fmt.Fprintf(os.Stderr, "[memory] FTS5 delete error for %s: %v\n", mem.ID, err)
			}
			count++
		}
	}

	resolved, err := resolutionPhase()
	if err != nil {
		return CleanupResult{}, err
	}
	return CleanupResult{Archived: count, Resolved: resolved}, nil
}

// resolutionPhase gathers contradiction candidates from online conflicts_with
// markers (implicit source) and the at-rest ContradictionDetector (lexical /
// holographic source), funnels them through ResolveContradictions, and returns
// the number of pairs actually superseded. AutoEvict is OFF here (flag-only).
func resolutionPhase() (int, error) {
	active, err := readAllMemories("active", "")
	if err != nil {
		return 0, err
	}

	var cands []Contradiction
	// Implicit candidates from durable conflicts_with markers. These carry only a
	// low confidence on their own — the at-rest detector must independently
	// corroborate the pair to lift it above the eviction floor.
	seen := map[string]bool{}
	for _, m := range active {
		for _, partner := range m.ConflictsWith {
			c := Contradiction{A: m.ID, B: partner, Source: "implicit", Confidence: implicitMarkerConfidence, Detail: "online conflicts_with marker"}
			k := c.PairKey()
			if seen[k] {
				continue
			}
			seen[k] = true
			cands = append(cands, c)
		}
	}
	// At-rest candidates from the injectable detector.
	if ContradictionDetector != nil {
		cands = append(cands, ContradictionDetector(active)...)
	}

	if len(cands) == 0 {
		return 0, nil
	}

	results, err := ResolveContradictions(cands, ResolveOptions{AutoEvict: false})
	if err != nil {
		return 0, err
	}
	resolved := 0
	for _, r := range results {
		if r.Action == "superseded" {
			resolved++
		}
	}
	return resolved, nil
}

// Stats summarises the vault: total/active/archived/superseded counts and a
// by-type breakdown of active memories.
func Stats() (total, active, archived, superseded int, byType map[string]int, err error) {
	all, err := readAllMemories("", "")
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	byType = map[string]int{}
	for _, m := range all {
		if !isRecallVisibleMemory(m) {
			continue
		}
		switch m.Status {
		case "active":
			active++
			byType[m.Type]++
		case "archived":
			archived++
		case "superseded":
			superseded++
		}
	}
	return active + archived + superseded, active, archived, superseded, byType, nil
}

func isRecallVisibleMemory(m Memory) bool {
	return m.Layer != "l2_inbox"
}
