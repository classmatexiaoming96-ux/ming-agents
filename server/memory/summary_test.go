package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSummary_ParsesAutoMindInput(t *testing.T) {
	path := writeSummaryFixture(t, `
run_id: run-001
project: ming-agents
source_system: automind
summary_path: .automind/summary/index.md
items:
  - kind: durable_lesson
    title: Build verification command
    body: Run go test ./memory/... because it covers receiver behavior.
    tags: [go, memory]
    evidence_ref: .automind/tasks/001/log.md
`)

	input, err := LoadSummary(path)
	if err != nil {
		t.Fatalf("LoadSummary() error = %v", err)
	}
	if input.RunID != "run-001" || input.Project != "ming-agents" || input.SourceSystem != "automind" {
		t.Fatalf("unexpected input metadata: %+v", input)
	}
	if len(input.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(input.Items))
	}
	item := input.Items[0]
	if item.Kind != "durable_lesson" || item.Title != "Build verification command" || item.EvidenceRef == "" {
		t.Fatalf("unexpected item: %+v", item)
	}
}

func TestLoadSummary_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "empty run id",
			body: `
project: ming-agents
source_system: automind
items:
  - kind: durable_lesson
    title: Lesson
    body: Body
`,
			want: "run_id",
		},
		{
			name: "wrong source",
			body: `
run_id: run-001
project: ming-agents
source_system: other
items:
  - kind: durable_lesson
    title: Lesson
    body: Body
`,
			want: "source_system",
		},
		{
			name: "empty items",
			body: `
run_id: run-001
project: ming-agents
source_system: automind
items: []
`,
			want: "items",
		},
		{
			name: "unknown kind",
			body: `
run_id: run-001
project: ming-agents
source_system: automind
items:
  - kind: mystery
    title: Lesson
    body: Body
`,
			want: "kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSummaryFixture(t, tt.body)
			_, err := LoadSummary(path)
			if err == nil {
				t.Fatal("LoadSummary() error = nil, want rejection")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadSummary() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestSummaryClassifier_ClassifiesItemsByKind(t *testing.T) {
	input := &SummaryInput{
		RunID:        "run-001",
		Project:      "ming-agents",
		SourceSystem: "automind",
		Items: []SummaryItem{
			{Kind: SummaryKindDurableLesson, Title: "lesson", Body: "body"},
			{Kind: SummaryKindRawEvidence, Title: "raw", Body: "body"},
			{Kind: SummaryKindCrossProjectCandidate, Title: "cross", Body: "body"},
		},
	}

	classified, err := SummaryClassifier{}.Classify(input)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(classified.DurableLessons) != 1 {
		t.Fatalf("durable lessons = %d, want 1", len(classified.DurableLessons))
	}
	if len(classified.RawEvidence) != 1 {
		t.Fatalf("raw evidence = %d, want 1", len(classified.RawEvidence))
	}
	if len(classified.CrossProjectCandidates) != 1 {
		t.Fatalf("cross-project candidates = %d, want 1", len(classified.CrossProjectCandidates))
	}
}

func TestSummaryClassifier_RejectsUnknownKind(t *testing.T) {
	input := &SummaryInput{
		RunID:        "run-001",
		Project:      "ming-agents",
		SourceSystem: "automind",
		Items: []SummaryItem{
			{Kind: "mystery", Title: "unknown", Body: "body"},
		},
	}

	_, err := SummaryClassifier{}.Classify(input)
	if err == nil {
		t.Fatal("Classify() error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Fatalf("Classify() error = %v, want kind rejection", err)
	}
}

func writeSummaryFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "summary.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
