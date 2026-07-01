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
	Kind        string   `yaml:"kind"`
	Title       string   `yaml:"title"`
	Body        string   `yaml:"body"`
	Tags        []string `yaml:"tags,omitempty"`
	EvidenceRef string   `yaml:"evidence_ref,omitempty"`
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

func IngestDurableLessons(project string, lessons []SummaryItem, accept bool) ([]SummaryRoute, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	routes := make([]SummaryRoute, 0, len(lessons))
	for _, lesson := range lessons {
		if lesson.Kind != SummaryKindDurableLesson {
			return nil, fmt.Errorf("durable lesson kind %q is not supported", lesson.Kind)
		}
		id := summaryMemoryID(project, lesson.Title)
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
		route.Path = path
		route.Written = true
		routes = append(routes, route)
	}
	return routes, nil
}

func ArchiveRawBundle(project, runID string, items []SummaryItem, summaryPath string) ([]SummaryRoute, error) {
	receiver, err := NewRunBundleReceiver(project, runID)
	if err != nil {
		return nil, err
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
	if summaryPath != "" {
		source, err := filepath.Abs(summaryPath)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		ext := strings.ToLower(filepath.Ext(source))
		if ext != ".json" {
			ext = ".md"
		}
		if _, err := receiver.writeArtifactWithSource(filepath.Join("summary", "raw-summary"+ext), raw, source); err != nil {
			return nil, err
		}
	}
	if err := receiver.Freeze(); err != nil {
		return nil, err
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
	}
	return &input, nil
}

func summaryMemory(project string, item SummaryItem, id, layer string) Memory {
	tags := append([]string(nil), item.Tags...)
	return Memory{
		ID:                id,
		Type:              "decision",
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

func summaryMemoryID(project, title string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + strings.TrimSpace(title)))
	return "automind_" + hex.EncodeToString(sum[:])[:16]
}
