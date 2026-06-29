package workflow

type WorkflowSpec struct {
	RunID string     `json:"run_id,omitempty"`
	Nodes []NodeSpec `json:"nodes"`
}

var DefaultWorkflowSpec = WorkflowSpec{
	Nodes: []NodeSpec{
		{ID: "clarification", Kind: NodeKindClarification},
		{ID: "planning", Kind: NodeKindPlanning, DependsOn: []string{"clarification"}},
		{ID: "development", Kind: NodeKindDevelopment, DependsOn: []string{"planning"}},
		{ID: "evaluation", Kind: NodeKindEvaluation, DependsOn: []string{"development"}},
		{ID: "review", Kind: NodeKindReview, DependsOn: []string{"evaluation"}},
	},
}
