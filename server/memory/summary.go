package memory

import (
	"fmt"
	"os"

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
