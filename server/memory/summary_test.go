package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestIngestDurableLessons_DryRunDoesNotWriteL2(t *testing.T) {
	vault := useTempVault(t)
	lessons := []SummaryItem{{
		Kind:  SummaryKindDurableLesson,
		Title: "Prefer focused receiver tests",
		Body:  "Run package tests before wiring the CLI because receiver behavior is isolated.",
		Tags:  []string{"tests"},
	}}

	routes, err := IngestDurableLessons("ming-agents", lessons, false)
	if err != nil {
		t.Fatalf("IngestDurableLessons() error = %v", err)
	}
	if len(routes) != 1 || routes[0].Written {
		t.Fatalf("routes = %+v, want one dry-run route", routes)
	}
	if _, err := os.Stat(filepath.Join(vault, "notes", "ming-agents")); !os.IsNotExist(err) {
		t.Fatalf("notes dir exists after dry-run: err=%v", err)
	}
}

func TestIngestDurableLessons_AcceptWritesL2WithProvenance(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC))
	lessons := []SummaryItem{{
		Kind:        SummaryKindDurableLesson,
		Title:       "Prefer focused receiver tests",
		Body:        "Run package tests before wiring the CLI because receiver behavior is isolated.",
		Tags:        []string{"tests"},
		EvidenceRef: ".automind/tasks/001/log.md",
	}}

	routes, err := IngestDurableLessons("ming-agents", lessons, true)
	if err != nil {
		t.Fatalf("IngestDurableLessons() error = %v", err)
	}
	if len(routes) != 1 || !routes[0].Written {
		t.Fatalf("routes = %+v, want one written route", routes)
	}
	if filepath.Dir(routes[0].Path) != filepath.Join(vault, "notes", "ming-agents") {
		t.Fatalf("path = %s, want L2 notes project dir", routes[0].Path)
	}

	raw, err := os.ReadFile(routes[0].Path)
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	mem, body, err := parseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("parse memory: %v", err)
	}
	if body != lessons[0].Body {
		t.Fatalf("body = %q, want %q", body, lessons[0].Body)
	}
	if mem.Layer != "l2" || mem.SourceSystem != "automind" || mem.SourceGranularity != "task_summary" {
		t.Fatalf("provenance = layer %q source_system %q granularity %q", mem.Layer, mem.SourceSystem, mem.SourceGranularity)
	}
	if mem.ID == "" || !strings.Contains(mem.ID, "automind_") {
		t.Fatalf("id = %q, want automind-derived id", mem.ID)
	}
}

func TestArchiveRawBundle_WritesSummaryBundleToL3(t *testing.T) {
	vault := useTempVault(t)
	source := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(source, []byte("# AutoMind summary\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	items := []SummaryItem{
		{Kind: SummaryKindRawEvidence, Title: "log", Body: "raw command log"},
		{Kind: SummaryKindRawEvidence, Title: "ack", Body: "reuse ack record"},
	}

	routes, err := ArchiveRawBundle("ming-agents", "run-001", items, source)
	if err != nil {
		t.Fatalf("ArchiveRawBundle() error = %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes len = %d, want 2", len(routes))
	}
	root := filepath.Join(vault, "runs", "ming-agents", "run-001")
	itemsPath := filepath.Join(root, "summary", "items.jsonl")
	rawPath := filepath.Join(root, "summary", "raw-summary.md")
	if _, err := os.Stat(itemsPath); err != nil {
		t.Fatalf("items.jsonl missing: %v", err)
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("raw summary missing: %v", err)
	}
	if strings.Contains(itemsPath, string(filepath.Separator)+"archive"+string(filepath.Separator)) ||
		strings.Contains(itemsPath, string(filepath.Separator)+"notes"+string(filepath.Separator)) {
		t.Fatalf("L3 summary path mixed with L2/archive namespace: %s", itemsPath)
	}
	rawItems, err := os.ReadFile(itemsPath)
	if err != nil {
		t.Fatalf("read items: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawItems)), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl lines = %d, want 2", len(lines))
	}
	var got SummaryItem
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("decode jsonl: %v", err)
	}
	if got.Title != "log" {
		t.Fatalf("first item title = %q, want log", got.Title)
	}

	receiver, err := NewRunBundleReceiver("ming-agents", "run-001")
	if err != nil {
		t.Fatalf("NewRunBundleReceiver() error = %v", err)
	}
	if err := receiver.VerifyIntegrity(); err != nil {
		t.Fatalf("VerifyIntegrity() error = %v", err)
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
