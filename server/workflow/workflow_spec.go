package workflow

type WorkflowSpec struct {
	RunID string     `json:"run_id,omitempty"`
	Nodes []NodeSpec `json:"nodes"`
}

var DefaultWorkflowSpec = WorkflowSpec{
	Nodes: []NodeSpec{
		defaultWorkflowNode("clarification", NodeKindClarification, nil),
		defaultWorkflowNode("planning", NodeKindPlanning, []string{"clarification"}),
		defaultWorkflowNode("development", NodeKindDevelopment, []string{"planning"}),
		defaultWorkflowNode("review", NodeKindReview, []string{"development"}),
		defaultWorkflowNode("evaluation", NodeKindEvaluation, []string{"review"}),
	},
}

func defaultWorkflowNode(id string, kind NodeKind, dependsOn []string) NodeSpec {
	spec := NodeSpec{
		ID:         id,
		Kind:       kind,
		DependsOn:  dependsOn,
		MaxRetries: defaultNodeMaxRetries(kind),
		RetryOn:    defaultNodeRetryOn(kind),
		Rollback:   defaultWorkflowRollbackSpec(kind),
	}
	return spec
}

func defaultNodeMaxRetries(kind NodeKind) int {
	switch kind {
	case NodeKindPlanning, NodeKindEvaluation:
		return 2
	case NodeKindClarification, NodeKindDevelopment, NodeKindReview:
		return 1
	default:
		return 0
	}
}

func defaultNodeRetryOn(kind NodeKind) []FailureClass {
	switch kind {
	case NodeKindClarification:
		return []FailureClass{FailureClassTransient, FailureClassMissingEvidence, FailureClassInconclusive}
	case NodeKindPlanning:
		return []FailureClass{FailureClassTransient, FailureClassContractError, FailureClassMissingEvidence, FailureClassInconclusive}
	case NodeKindDevelopment:
		return []FailureClass{FailureClassTransient, FailureClassValidatorIssue, FailureClassMissingEvidence}
	case NodeKindReview:
		return []FailureClass{FailureClassTransient, FailureClassContractError, FailureClassMissingEvidence, FailureClassInconclusive}
	case NodeKindEvaluation:
		return []FailureClass{FailureClassTransient, FailureClassValidatorIssue, FailureClassMissingEvidence, FailureClassInconclusive}
	default:
		return nil
	}
}

func defaultWorkflowRollbackSpec(kind NodeKind) RollbackSpec {
	if kind == NodeKindReview {
		return RollbackSpec{
			DefaultUnit:   RollbackUnit{Scope: "review", MaxAttempts: 1, ReusePolicy: SessionReuseSameSession},
			OnContract:    RollbackActionRetryReport,
			OnHumanReject: RollbackActionRetryReport,
		}
	}
	return DefaultRollbackSpec(kind)
}
