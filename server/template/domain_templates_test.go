package template

import (
	"encoding/json"
	"testing"
)

func TestDomainRegistry_BugFixTemplate(t *testing.T) {
	r := NewDomainRegistry()

	// Verify BugFixTemplate is registered
	tmpl := r.Get("bugfix")
	if tmpl == nil {
		t.Fatal("BugFixTemplate not found in registry")
	}

	// Verify metadata
	var meta TemplateMetadata
	if err := json.Unmarshal(tmpl.Metadata, &meta); err != nil {
		t.Fatalf("Failed to unmarshal metadata: %v", err)
	}
	if meta.Category != "bugfix" {
		t.Errorf("BugFixTemplate category = %q, want %q", meta.Category, "bugfix")
	}
	if len(meta.Tags) != 2 {
		t.Errorf("BugFixTemplate tags count = %d, want 2", len(meta.Tags))
	}
	if !containsStrings(meta.Tags, "repair") || !containsStrings(meta.Tags, "ci-cd") {
		t.Errorf("BugFixTemplate tags = %v, want containing repair and ci-cd", meta.Tags)
	}

	// Verify param schema
	if len(tmpl.ParamSchema) != 6 {
		t.Errorf("BugFixTemplate param schema count = %d, want 6", len(tmpl.ParamSchema))
	}

	// Verify required params
	schemaMap := make(map[string]*ParamSchema)
	for _, p := range tmpl.ParamSchema {
		schemaMap[p.Name] = p
	}

	if _, ok := schemaMap["repo_path"]; !ok {
		t.Error("BugFixTemplate missing repo_path param")
	}
	if schemaMap["repo_path"].Type != ParamTypeString {
		t.Errorf("repo_path type = %v, want %v", schemaMap["repo_path"].Type, ParamTypeString)
	}
	if !schemaMap["repo_path"].Required {
		t.Error("repo_path should be required")
	}

	if _, ok := schemaMap["issue_id"]; !ok {
		t.Error("BugFixTemplate missing issue_id param")
	}
	if !schemaMap["issue_id"].Required {
		t.Error("issue_id should be required")
	}

	// Verify optional params
	if _, ok := schemaMap["branch_name"]; !ok {
		t.Error("BugFixTemplate missing branch_name param")
	}
	if schemaMap["branch_name"].Required {
		t.Error("branch_name should not be required")
	}

	if _, ok := schemaMap["assignee"]; !ok {
		t.Error("BugFixTemplate missing assignee param")
	}

	// Verify priority enum
	if _, ok := schemaMap["priority"]; !ok {
		t.Error("BugFixTemplate missing priority param")
	}
	if len(schemaMap["priority"].Enum) != 3 {
		t.Errorf("priority enum count = %d, want 3", len(schemaMap["priority"].Enum))
	}

	// Verify dry_run boolean
	if _, ok := schemaMap["dry_run"]; !ok {
		t.Error("BugFixTemplate missing dry_run param")
	}
	if schemaMap["dry_run"].Type != ParamTypeBoolean {
		t.Errorf("dry_run type = %v, want %v", schemaMap["dry_run"].Type, ParamTypeBoolean)
	}
}

func TestDomainRegistry_TestTemplate(t *testing.T) {
	r := NewDomainRegistry()

	// Verify TestTemplate is registered
	tmpl := r.Get("test")
	if tmpl == nil {
		t.Fatal("TestTemplate not found in registry")
	}

	// Verify metadata
	var meta TemplateMetadata
	if err := json.Unmarshal(tmpl.Metadata, &meta); err != nil {
		t.Fatalf("Failed to unmarshal metadata: %v", err)
	}
	if meta.Category != "testing" {
		t.Errorf("TestTemplate category = %q, want %q", meta.Category, "testing")
	}
	if !containsStrings(meta.Tags, "qa") || !containsStrings(meta.Tags, "coverage") {
		t.Errorf("TestTemplate tags = %v, want containing qa and coverage", meta.Tags)
	}

	// Verify param schema
	if len(tmpl.ParamSchema) != 4 {
		t.Errorf("TestTemplate param schema count = %d, want 4", len(tmpl.ParamSchema))
	}

	schemaMap := make(map[string]*ParamSchema)
	for _, p := range tmpl.ParamSchema {
		schemaMap[p.Name] = p
	}

	// Verify required params
	if _, ok := schemaMap["repo_path"]; !ok {
		t.Error("TestTemplate missing repo_path param")
	}
	if !schemaMap["repo_path"].Required {
		t.Error("repo_path should be required")
	}

	if _, ok := schemaMap["test_suite"]; !ok {
		t.Error("TestTemplate missing test_suite param")
	}
	if !schemaMap["test_suite"].Required {
		t.Error("test_suite should be required")
	}
	if len(schemaMap["test_suite"].Enum) != 3 {
		t.Errorf("test_suite enum count = %d, want 3", len(schemaMap["test_suite"].Enum))
	}

	// Verify coverage_target range
	if _, ok := schemaMap["coverage_target"]; !ok {
		t.Error("TestTemplate missing coverage_target param")
	}
	if schemaMap["coverage_target"].Min == nil || *schemaMap["coverage_target"].Min != 0 {
		t.Error("coverage_target min should be 0")
	}
	if schemaMap["coverage_target"].Max == nil || *schemaMap["coverage_target"].Max != 100 {
		t.Error("coverage_target max should be 100")
	}

	// Verify parallel boolean
	if _, ok := schemaMap["parallel"]; !ok {
		t.Error("TestTemplate missing parallel param")
	}
	if schemaMap["parallel"].Type != ParamTypeBoolean {
		t.Errorf("parallel type = %v, want %v", schemaMap["parallel"].Type, ParamTypeBoolean)
	}
}

func TestDomainRegistry_ValidateBugFixTemplate(t *testing.T) {
	r := NewDomainRegistry()

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "valid with required only",
			params:  map[string]any{"repo_path": "/repo/myproject", "issue_id": "ISSUE-123"},
			wantErr: false,
		},
		{
			name:    "valid with all params",
			params:  map[string]any{
				"repo_path":   "/repo/myproject",
				"issue_id":    "ISSUE-123",
				"branch_name": "fix/issue-123",
				"assignee":    "developer@example.com",
				"priority":    1,
				"dry_run":     true,
			},
			wantErr: false,
		},
		{
			name:    "missing required repo_path",
			params:  map[string]any{"issue_id": "ISSUE-123"},
			wantErr: true,
		},
		{
			name:    "missing required issue_id",
			params:  map[string]any{"repo_path": "/repo/myproject"},
			wantErr: true,
		},
		{
			name:    "invalid priority value",
			params:  map[string]any{"repo_path": "/repo/myproject", "issue_id": "ISSUE-123", "priority": 5},
			wantErr: true,
		},
		{
			name:    "dry_run as string",
			params:  map[string]any{"repo_path": "/repo/myproject", "issue_id": "ISSUE-123", "dry_run": "true"},
			wantErr: false,
		},
		{
			name:    "unknown param",
			params:  map[string]any{"repo_path": "/repo/myproject", "issue_id": "ISSUE-123", "unknown_param": "value"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Validate("bugfix", tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDomainRegistry_ValidateTestTemplate(t *testing.T) {
	r := NewDomainRegistry()

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "valid with required only",
			params:  map[string]any{"repo_path": "/repo/myproject", "test_suite": "unit"},
			wantErr: false,
		},
		{
			name:    "valid with all params",
			params:  map[string]any{
				"repo_path":       "/repo/myproject",
				"test_suite":      "integration",
				"coverage_target": 80.0,
				"parallel":        true,
			},
			wantErr: false,
		},
		{
			name:    "missing required repo_path",
			params:  map[string]any{"test_suite": "unit"},
			wantErr: true,
		},
		{
			name:    "missing required test_suite",
			params:  map[string]any{"repo_path": "/repo/myproject"},
			wantErr: true,
		},
		{
			name:    "invalid test_suite value",
			params:  map[string]any{"repo_path": "/repo/myproject", "test_suite": "performance"},
			wantErr: true,
		},
		{
			name:    "coverage_target below min",
			params:  map[string]any{"repo_path": "/repo/myproject", "test_suite": "unit", "coverage_target": -5.0},
			wantErr: true,
		},
		{
			name:    "coverage_target above max",
			params:  map[string]any{"repo_path": "/repo/myproject", "test_suite": "unit", "coverage_target": 150.0},
			wantErr: true,
		},
		{
			name:    "parallel as string",
			params:  map[string]any{"repo_path": "/repo/myproject", "test_suite": "unit", "parallel": "true"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Validate("test", tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDomainRegistry_GetByCategory(t *testing.T) {
	r := NewDomainRegistry()

	bugfixTemplates := r.GetByCategory("bugfix")
	if len(bugfixTemplates) == 0 {
		t.Error("GetByCategory(bugfix) returned empty")
	}
	for _, tmpl := range bugfixTemplates {
		if tmpl.Name != "bugfix" {
			t.Errorf("GetByCategory(bugfix) returned template %q, want bugfix", tmpl.Name)
		}
	}

	testingTemplates := r.GetByCategory("testing")
	if len(testingTemplates) == 0 {
		t.Error("GetByCategory(testing) returned empty")
	}
	for _, tmpl := range testingTemplates {
		if tmpl.Name != "test" {
			t.Errorf("GetByCategory(testing) returned template %q, want test", tmpl.Name)
		}
	}
}

func TestDomainRegistry_List(t *testing.T) {
	r := NewDomainRegistry()

	templates := r.List()
	if len(templates) < 2 {
		t.Errorf("List() returned %d templates, want at least 2", len(templates))
	}

	names := make(map[string]bool)
	for _, tmpl := range templates {
		names[tmpl.Name] = true
	}

	if !names["bugfix"] {
		t.Error("List() missing bugfix template")
	}
	if !names["test"] {
		t.Error("List() missing test template")
	}
}

func TestDefaultDomainRegistry_Singleton(t *testing.T) {
	r1 := DefaultDomainRegistry()
	r2 := DefaultDomainRegistry()

	if r1 != r2 {
		t.Error("DefaultDomainRegistry() should return singleton, got different instances")
	}
}

func TestDefaultDomainRegistry_HasBothTemplates(t *testing.T) {
	r := DefaultDomainRegistry()

	bugfix := r.Get("bugfix")
	if bugfix == nil {
		t.Fatal("DefaultDomainRegistry missing bugfix template")
	}

	test := r.Get("test")
	if test == nil {
		t.Fatal("DefaultDomainRegistry missing test template")
	}
}

// Helper functions

func containsStrings(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}