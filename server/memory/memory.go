// Package memory is a self-evolving memory system, rewritten in Go from the
// original memory_api.py. It scores incoming notes, files them into an Obsidian
// vault (notes / inbox / archive), retrieves them with simple filters, records
// usage feedback, and archives expired entries.
//
// The vault lives outside the repository (defaults to $HOME/.hermes/vault) so
// it can be shared with the legacy Python CLI. Override VaultDir for tests.
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
// $HOME/.hermes/vault and may be reassigned (e.g. in tests).
var VaultDir = defaultVaultDir()

func defaultVaultDir() string {
	// os.ExpandEnv keeps parity with the Python version's path handling and
	// works for any user/home directory.
	return os.ExpandEnv("$HOME/.hermes/vault")
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

	// §13 explicit 证据（Phase 1）— 与 implicit 解耦，explicit-trumps tier 读取。
	// 旧文件无此字段 → 零值；历史上被 explicit feedback 过的老 memory 也算 0
	// （无法回填），explicit-trumps 对它们不生效，按 composite tier 裁决。
	ExplicitHits int    `yaml:"explicit_hits,omitempty"`
	LastExplicit string `yaml:"last_explicit,omitempty"`

	// §7 implicit（Phase 3 依赖，提前加字段，本期不加逻辑）。
	ImplicitHits int     `yaml:"implicit_hits,omitempty"`
	PendingScore float64 `yaml:"pending_score,omitempty"`
	LastImplicit string  `yaml:"last_implicit,omitempty"`

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

// computeID derives a short, content-addressed id (parity with Python md5[:8]).
func computeID(content string) string {
	head := content
	if len(head) > 200 {
		head = head[:200]
	}
	sum := md5.Sum([]byte(head))
	return "mem_" + hex.EncodeToString(sum[:])[:8]
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
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// readAllMemories scans notes/inbox/archive. If status is non-empty, only
// memories with that status are returned.
func readAllMemories(status string) ([]Memory, error) {
	var results []Memory
	for _, sub := range []string{"notes", "inbox", "archive"} {
		root := filepath.Join(VaultDir, sub)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
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
			if status != "" && mem.Status != status {
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
	return results, nil
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
		title = content
		if len(title) > 60 {
			title = title[:60]
		}
		title = strings.ReplaceAll(title, "\n", " ")
	}
	if source == "" {
		source = "manual"
	}

	id := computeID(content)
	today := now().Format(dateLayout)

	// novelty: 1 - highest similarity to any existing active memory.
	existing, err := readAllMemories("active")
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
		Body:        content,
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

	reason := fmt.Sprintf("score=%g (accepted)", score)
	if !accepted {
		reason = fmt.Sprintf("score=%g (below threshold %g)", score, scoreThreshold)
	}
	return Result{Accepted: accepted, Score: score, ID: id, Path: path, Reason: reason}, nil
}

// Recall returns active memories matching the filters, highest score first.
// Empty string / nil / zero filters are ignored. status defaults to "active".
func Recall(query, project, memType string, tags []string, minScore float64, status string, limit int) ([]Memory, error) {
	if status == "" {
		status = "active"
	}
	memories, err := readAllMemories(status)
	if err != nil {
		return nil, err
	}

	var results []Memory
	for _, m := range memories {
		if project != "" && m.Project != project {
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
			q := strings.ToLower(query)
			if !strings.Contains(strings.ToLower(m.Body), q) &&
				!strings.Contains(strings.ToLower(m.Title), q) {
				continue
			}
		}
		results = append(results, m)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
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
	active, err := readAllMemories("active")
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
	all, err := readAllMemories("")
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	byType = map[string]int{}
	for _, m := range all {
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
	return len(all), active, archived, superseded, byType, nil
}
