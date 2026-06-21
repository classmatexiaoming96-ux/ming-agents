// Package template provides the Template Registry for WDL template management
// and param-schema validation. This implements Epic 2.10 and Epic 2.11.
package template

import (
	"encoding/json"
	"sync"
)

// DomainTemplateRegistry is a registry pre-populated with domain-specific templates.
type DomainTemplateRegistry struct {
	*Registry
}

var (
	defaultRegistry     *DomainTemplateRegistry
	defaultRegistryOnce sync.Once
)

// DefaultDomainRegistry returns the default domain template registry singleton.
func DefaultDomainRegistry() *DomainTemplateRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewDomainRegistry()
	})
	return defaultRegistry
}

// NewDomainRegistry creates a new registry pre-populated with domain templates.
func NewDomainRegistry() *DomainTemplateRegistry {
	r := &DomainTemplateRegistry{
		Registry: NewRegistry(),
	}

	// Register BugFixTemplate
	r.Register(&Template{
		Name:        "bugfix",
		Description: "Bug fix loop workflow with identify → fix → verify pattern. Supports iterative fixing until tests pass.",
		Version:     "1.0.0",
		WDLContent: `version: '1.0'
name: bugfix-loop
description: Standard bug fix workflow with identify → fix → verify pattern

structure:
  static:
    steps:
      - name: locate
        role: investigator
        description: Locate and identify the bug source
      - name: fix
        role: fixer
        description: Apply the fix for the identified bug
        fanout: true  # fanout over locate.output.files
      - name: test
        role: test-runner
        description: Verify the fix passes tests
        gate: true  # gate for loop convergence
      - name: fix-loop
        type: loop
        description: Iterative fix loop while tests fail
        body: fix
        evaluator: test
        max_iterations: 6
        convergence: while !test.passed
      - name: pr
        role: integrator
        description: Create pull request with the fix

dynamic:
  parameters:
    - fix.prompt
    - test.assertions
    - max_iterations (clamped by platform)
    - model bindings per node
`,
		ParamSchema: []*ParamSchema{
			{
				Name:        "repo_path",
				Type:        ParamTypeString,
				Description: "Path to the repository containing the bug",
				Required:    true,
			},
			{
				Name:        "issue_id",
				Type:        ParamTypeString,
				Description: "Issue or ticket ID tracking this bug",
				Required:    true,
			},
			{
				Name:        "branch_name",
				Type:        ParamTypeString,
				Description: "Optional branch name for the fix (defaults to bugfix/<issue_id>)",
				Required:    false,
			},
			{
				Name:        "assignee",
				Type:        ParamTypeString,
				Description: "Optional assignee for the bug fix task",
				Required:    false,
			},
			{
				Name:        "priority",
				Type:        ParamTypeNumber,
				Description: "Priority level (1=critical, 2=high, 3=medium)",
				Required:    false,
				Enum:        []any{1, 2, 3},
			},
			{
				Name:        "dry_run",
				Type:        ParamTypeBoolean,
				Description: "If true, validate changes without applying them",
				Required:    false,
				Default:     false,
			},
		},
		Metadata: mustMarshalMetadata(TemplateMetadata{
			Category:    "bugfix",
			Tags:        []string{"repair", "ci-cd"},
			Description: "Bug fix loop template following locate → fix → test → pr pattern",
		}),
	})

	// Register TestTemplate
	r.Register(&Template{
		Name:        "test",
		Description: "Test workflow with setup → run tests → report pattern. Supports unit, integration, and e2e test suites.",
		Version:     "1.0.0",
		WDLContent: `version: '1.0'
name: test-workflow
description: Standard test workflow with setup → run tests → report pattern

structure:
  static:
    steps:
      - name: setup
        role: test-setup
        description: Setup test environment and dependencies
      - name: run-tests
        role: test-runner
        description: Execute the test suite
      - name: report
        role: reporter
        description: Aggregate and report test results

dynamic:
  parameters:
    - test.prompt
    - coverage_target
    - parallel_workers
    - model bindings per node
`,
		ParamSchema: []*ParamSchema{
			{
				Name:        "repo_path",
				Type:        ParamTypeString,
				Description: "Path to the repository to test",
				Required:    true,
			},
			{
				Name:        "test_suite",
				Type:        ParamTypeString,
				Description: "Type of test suite to run",
				Required:    true,
				Enum:        []any{"unit", "integration", "e2e"},
			},
			{
				Name:        "coverage_target",
				Type:        ParamTypeNumber,
				Description: "Target code coverage percentage (0-100)",
				Required:    false,
				Min:         newFloatPtr(0),
				Max:         newFloatPtr(100),
			},
			{
				Name:        "parallel",
				Type:        ParamTypeBoolean,
				Description: "If true, run tests in parallel (respects coverage_target)",
				Required:    false,
				Default:     true,
			},
		},
		Metadata: mustMarshalMetadata(TemplateMetadata{
			Category:    "testing",
			Tags:        []string{"qa", "coverage"},
			Description: "Test workflow template following setup → run tests → report pattern",
		}),
	})

	return r
}

// TemplateMetadata represents template metadata.
type TemplateMetadata struct {
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
}

// mustMarshalMetadata marshals metadata or panics on error.
func mustMarshalMetadata(m TemplateMetadata) json.RawMessage {
	data, err := json.Marshal(m)
	if err != nil {
		panic("failed to marshal template metadata: " + err.Error())
	}
	return data
}

// GetByCategory returns all templates with the given category.
func (r *DomainTemplateRegistry) GetByCategory(category string) []*Template {
	var result []*Template
	for _, t := range r.Registry.List() {
		var meta TemplateMetadata
		if err := json.Unmarshal(t.Metadata, &meta); err == nil {
			if meta.Category == category {
				result = append(result, t)
			}
		}
	}
	return result
}

// GetByTag returns all templates containing the given tag.
func (r *DomainTemplateRegistry) GetByTag(tag string) []*Template {
	var result []*Template
	for _, tmpl := range r.Registry.List() {
		var meta TemplateMetadata
		if err := json.Unmarshal(tmpl.Metadata, &meta); err == nil {
			for _, t := range meta.Tags {
				if t == tag {
					result = append(result, tmpl)
					break
				}
			}
		}
	}
	return result
}