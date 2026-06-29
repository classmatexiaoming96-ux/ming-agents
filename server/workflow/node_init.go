package workflow

func init() {
	registry := GetRegistry()
	registry.Register(NodeKindClarification, func() WorkflowNode { return &clarificationNode{} })
	registry.Register(NodeKindPlanning, func() WorkflowNode { return &planningNode{} })
	registry.Register(NodeKindDevelopment, func() WorkflowNode { return &developmentNode{} })
	registry.Register(NodeKindEvaluation, func() WorkflowNode { return &evaluationNode{} })
	registry.Register(NodeKindReview, func() WorkflowNode { return &reviewNode{} })
}
