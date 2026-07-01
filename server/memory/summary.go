package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SummaryKindDurableLesson         = "durable_lesson"
	SummaryKindRawEvidence           = "raw_evidence"
	SummaryKindCrossProjectCandidate = "cross_project_candidate"

	SummaryExperienceDecision    = "decision"
	SummaryExperienceSuccessPath = "success_path"
	SummaryExperienceAvoidPath   = "avoid_path"
)

var allowedSummaryKinds = map[string]bool{
	SummaryKindDurableLesson:         true,
	SummaryKindRawEvidence:           true,
	SummaryKindCrossProjectCandidate: true,
}

type SummaryInput struct {
	RunID        string        `yaml:"run_id"`
	Project      string        `yaml:"project"`
	SourceSystem string        `yaml:"source_system"`
	SummaryPath  string        `yaml:"summary_path"`
	Items        []SummaryItem `yaml:"items"`
}

type SummaryItem struct {
	Kind           string   `yaml:"kind"`
	Title          string   `yaml:"title"`
	Body           string   `yaml:"body"`
	ExperienceKind string   `yaml:"experience_kind,omitempty"`
	Tags           []string `yaml:"tags,omitempty"`
	EvidenceRef    string   `yaml:"evidence_ref,omitempty"`
}

type ClassifiedSummary struct {
	DurableLessons         []SummaryItem
	RawEvidence            []SummaryItem
	CrossProjectCandidates []SummaryItem
}

type SummaryClassifier struct{}

type SummaryRoute struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Target  string `json:"target"`
	Path    string `json:"path,omitempty"`
	Written bool   `json:"written"`
}

type SummaryImportOptions struct {
	Accept             bool
	ProjectOverride    string
	CrossProjectPolicy string
}

type SummaryImportResult struct {
	DryRun bool
	Routes []SummaryRoute
	L2     int
	L3     int
	Inbox  int
}

// ImportSummary receives one batch-produced AutoMind task summary and routes it
// without changing Brief, reuse, or implicit feedback paths. Dry-run is the
// default: unless options.Accept is true, it reports the L2, L3, and inbox
// routes that would be used but writes nothing. Accepted imports send durable
// lessons to project L2, raw evidence to the immutable L3 run bundle, and
// cross-project candidates to the human-curated inbox unless policy rejects
// them.
func ImportSummary(path string, options SummaryImportOptions) (*SummaryImportResult, error) {
	input, err := LoadSummary(path)
	if err != nil {
		return nil, err
	}
	if options.ProjectOverride != "" {
		input.Project = options.ProjectOverride
	}
	classified, err := SummaryClassifier{}.Classify(input)
	if err != nil {
		return nil, err
	}
	if options.CrossProjectPolicy == "" {
		options.CrossProjectPolicy = "inbox"
	}
	if options.CrossProjectPolicy != "inbox" && options.CrossProjectPolicy != "reject" {
		return nil, fmt.Errorf("cross-project policy %q is not supported", options.CrossProjectPolicy)
	}
	if options.CrossProjectPolicy == "reject" && len(classified.CrossProjectCandidates) > 0 {
		return nil, fmt.Errorf("cross-project candidates rejected by policy")
	}

	result := &SummaryImportResult{DryRun: !options.Accept}
	if !options.Accept {
		result.Routes = append(result.Routes, dryRunRoutes(classified.DurableLessons, "l2")...)
		result.Routes = append(result.Routes, dryRunRoutes(classified.RawEvidence, "l3")...)
		if options.CrossProjectPolicy == "inbox" {
			result.Routes = append(result.Routes, dryRunRoutes(classified.CrossProjectCandidates, "l2_inbox")...)
		}
		result.countRoutes()
		return result, nil
	}

	receiver, err := NewRunBundleReceiver(input.Project, input.RunID)
	if err != nil {
		return nil, err
	}
	frozen, err := receiver.IsFrozen()
	if err != nil {
		return nil, err
	}
	if frozen {
		return nil, ErrBundleFrozen
	}

	l2Routes, err := IngestDurableLessons(input.Project, classified.DurableLessons, true)
	if err != nil {
		return nil, err
	}
	result.Routes = append(result.Routes, l2Routes...)
	if len(classified.RawEvidence) > 0 {
		summaryPath, allowedRoots, err := importSummarySource(path, input.SummaryPath)
		if err != nil {
			return nil, err
		}
		l3Routes, err := ArchiveRawBundle(input.Project, input.RunID, classified.RawEvidence, summaryPath, allowedRoots)
		if err != nil {
			return nil, err
		}
		result.Routes = append(result.Routes, l3Routes...)
	}
	if options.CrossProjectPolicy == "inbox" {
		inboxRoutes, err := IngestCrossProjectCandidates(classified.CrossProjectCandidates)
		if err != nil {
			return nil, err
		}
		result.Routes = append(result.Routes, inboxRoutes...)
	}
	if len(classified.RawEvidence) == 0 {
		// Importing the task summary is the terminal task-end operation for this
		// run bundle. Phase 5 writes such as phase-reuse and reuse-ack must happen
		// before import-automind-summary, because Freeze intentionally makes the
		// bundle immutable after the summary has been accepted.
		if err := receiver.Freeze(); err != nil {
			return nil, err
		}
	}
	result.countRoutes()
	return result, nil
}

// SummaryClassifier maps summary item kinds to storage routes:
// durable_lesson -> L2 project notes, raw_evidence -> L3 run summary bundle,
// and cross_project_candidate -> L2 inbox for human curation. Unknown kinds are
// rejected instead of being guessed into a storage layer.
func (SummaryClassifier) Classify(input *SummaryInput) (*ClassifiedSummary, error) {
	if input == nil {
		return nil, fmt.Errorf("summary input is required")
	}
	var classified ClassifiedSummary
	for i, item := range input.Items {
		switch item.Kind {
		case SummaryKindDurableLesson:
			classified.DurableLessons = append(classified.DurableLessons, item)
		case SummaryKindRawEvidence:
			classified.RawEvidence = append(classified.RawEvidence, item)
		case SummaryKindCrossProjectCandidate:
			classified.CrossProjectCandidates = append(classified.CrossProjectCandidates, item)
		default:
			return nil, fmt.Errorf("summary items[%d] kind %q is not supported", i, item.Kind)
		}
	}
	return &classified, nil
}

// IngestDurableLessons writes AutoMind durable lessons into L2 project memory
// only when accept is true. In dry-run mode it returns planned L2 routes but
// writes nothing, preserving the receiver's conservative default.
func IngestDurableLessons(project string, lessons []SummaryItem, accept bool) ([]SummaryRoute, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	routes := make([]SummaryRoute, 0, len(lessons))
	for _, lesson := range lessons {
		if lesson.Kind != SummaryKindDurableLesson {
			return nil, fmt.Errorf("durable lesson kind %q is not supported", lesson.Kind)
		}
		id := summaryMemoryID(project, lesson.Title, lesson.Body)
		targetPath := filepath.Join(VaultDir, "notes", project, id+".md")
		route := SummaryRoute{
			Kind:   lesson.Kind,
			Title:  lesson.Title,
			Target: "l2",
			Path:   targetPath,
		}
		if !accept {
			routes = append(routes, route)
			continue
		}
		mem := summaryMemory(project, lesson, id, "l2")
		path, err := writeMemory(mem, filepath.Join(VaultDir, "notes", project))
		if err != nil {
			return nil, err
		}
		if err := IndexMemory(mem.ID, mem.Title, mem.Body, mem.Project, mem.Type, mem.Tags); err != nil {
			fmt.Fprintf(os.Stderr, "[memory] FTS5 index error for %s: %v\n", mem.ID, err)
		}
		route.Path = path
		route.Written = true
		routes = append(routes, route)
	}
	return routes, nil
}

// ArchiveRawBundle stores raw AutoMind summary evidence under the Phase 5 L3
// run bundle namespace, reusing RunBundleReceiver manifest, hash, and freeze
// behavior. It writes summary/items.jsonl plus the raw summary copy through the
// automind-summary receiver namespace, never archive/ or notes/, and returns
// ErrBundleFrozen when the run bundle was already finalized.
func ArchiveRawBundle(project, runID string, items []SummaryItem, summaryPath string, allowedRoots ...[]string) ([]SummaryRoute, error) {
	receiver, err := NewRunBundleReceiver(project, runID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(receiver.Root(), "_frozen")); err == nil {
		return nil, ErrBundleFrozen
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if summaryPath != "" {
		format := summaryFormat(summaryPath)
		if len(allowedRoots) > 0 {
			if err := receiver.ReceiveAutoMindSummaryFromSource(summaryPath, format, allowedRoots[0]); err != nil {
				return nil, err
			}
		} else {
			fmt.Fprintf(os.Stderr, "[memory] warning: importing AutoMind summary %q without allowed roots\n", summaryPath)
			if err := receiver.ReceiveAutoMindSummaryFromSource(summaryPath, format); err != nil {
				return nil, err
			}
		}
	}
	routes := make([]SummaryRoute, 0, len(items))
	for _, item := range items {
		if item.Kind != SummaryKindRawEvidence {
			return nil, fmt.Errorf("raw evidence kind %q is not supported", item.Kind)
		}
		line, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("marshal raw evidence: %w", err)
		}
		written, err := receiver.appendArtifact(filepath.Join("summary", "items.jsonl"), append(line, '\n'))
		if err != nil {
			return nil, err
		}
		routes = append(routes, SummaryRoute{
			Kind:    item.Kind,
			Title:   item.Title,
			Target:  "l3",
			Path:    filepath.Join(receiver.Root(), written),
			Written: true,
		})
	}
	// Importing the task summary is the terminal task-end operation for this run
	// bundle. Phase 5 writes such as phase-reuse and reuse-ack must complete
	// before import-automind-summary, because Freeze intentionally makes later
	// receiver writes return ErrBundleFrozen.
	if err := receiver.Freeze(); err != nil {
		return nil, err
	}
	return routes, nil
}

func IngestCrossProjectCandidates(candidates []SummaryItem) ([]SummaryRoute, error) {
	routes := make([]SummaryRoute, 0, len(candidates))
	targetDir := filepath.Join(VaultDir, "notes", "_inbox", "cross_project_candidates")
	for _, candidate := range candidates {
		if candidate.Kind != SummaryKindCrossProjectCandidate {
			return nil, fmt.Errorf("cross-project candidate kind %q is not supported", candidate.Kind)
		}
		id := summaryMemoryID("_cross_project", candidate.Title, candidate.Body)
		mem := summaryMemory("_cross_project", candidate, id, "l2_inbox")
		mem.CrossProject = true
		path, err := writeMemory(mem, targetDir)
		if err != nil {
			return nil, err
		}
		if err := IndexMemory(mem.ID, mem.Title, mem.Body, mem.Project, mem.Type, mem.Tags); err != nil {
			fmt.Fprintf(os.Stderr, "[memory] FTS5 index error for %s: %v\n", mem.ID, err)
		}
		routes = append(routes, SummaryRoute{
			Kind:    candidate.Kind,
			Title:   candidate.Title,
			Target:  "l2_inbox",
			Path:    path,
			Written: true,
		})
	}
	return routes, nil
}

func LoadSummary(path string) (*SummaryInput, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read summary: %w", err)
	}
	var input SummaryInput
	if err := yaml.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("parse summary YAML: %w", err)
	}
	if input.RunID == "" {
		return nil, fmt.Errorf("summary run_id is required")
	}
	if input.Project == "" {
		return nil, fmt.Errorf("summary project is required")
	}
	if input.SourceSystem != "automind" {
		return nil, fmt.Errorf("summary source_system must be automind")
	}
	if len(input.Items) == 0 {
		return nil, fmt.Errorf("summary items must contain at least one item")
	}
	for i, item := range input.Items {
		if !allowedSummaryKinds[item.Kind] {
			return nil, fmt.Errorf("summary items[%d] kind %q is not supported", i, item.Kind)
		}
		if item.ExperienceKind == "" {
			input.Items[i].ExperienceKind = SummaryExperienceDecision
			continue
		}
		switch item.ExperienceKind {
		case SummaryExperienceDecision, SummaryExperienceSuccessPath, SummaryExperienceAvoidPath:
		default:
			return nil, fmt.Errorf("summary items[%d] experience_kind %q is not supported", i, item.ExperienceKind)
		}
	}
	return &input, nil
}

func summaryMemory(project string, item SummaryItem, id, layer string) Memory {
	tags := append([]string(nil), item.Tags...)
	return Memory{
		ID:                id,
		Type:              summaryMemoryType(item),
		Project:           project,
		Tags:              tags,
		Title:             item.Title,
		Score:             5,
		Novelty:           1,
		Specificity:       1,
		Reusability:       1,
		CreatedAt:         now().Format(dateLayout),
		ExpiresAt:         neverExpires,
		Status:            "active",
		Source:            "automind",
		Links:             []string{},
		Layer:             layer,
		SourceSystem:      "automind",
		SourceGranularity: "task_summary",
		EvidenceRef:       item.EvidenceRef,
		Inject:            "query",
		Body:              item.Body,
	}
}

func summaryMemoryID(project, title, body string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + strings.TrimSpace(title) + "\x00" + strings.TrimSpace(body)))
	return "automind_" + hex.EncodeToString(sum[:])[:16]
}

func summaryMemoryType(item SummaryItem) string {
	switch item.ExperienceKind {
	case SummaryExperienceAvoidPath:
		return "gotcha"
	default:
		return "decision"
	}
}

func importSummarySource(importPath, summaryPath string) (string, []string, error) {
	if summaryPath == "" {
		return "", nil, nil
	}
	importAbs, err := filepath.Abs(importPath)
	if err != nil {
		return "", nil, err
	}
	importDir := filepath.Dir(filepath.Clean(importAbs))
	source := summaryPath
	if !filepath.IsAbs(source) {
		source = filepath.Join(importDir, source)
	}
	return source, []string{importDir}, nil
}

func summaryFormat(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return "json"
	}
	return "md"
}

func dryRunRoutes(items []SummaryItem, target string) []SummaryRoute {
	routes := make([]SummaryRoute, 0, len(items))
	for _, item := range items {
		routes = append(routes, SummaryRoute{
			Kind:   item.Kind,
			Title:  item.Title,
			Target: target,
		})
	}
	return routes
}

func (r *SummaryImportResult) countRoutes() {
	for _, route := range r.Routes {
		switch route.Target {
		case "l2":
			r.L2++
		case "l3":
			r.L3++
		case "l2_inbox":
			r.Inbox++
		}
	}
}
