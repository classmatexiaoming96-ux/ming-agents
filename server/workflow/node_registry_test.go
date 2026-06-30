package workflow

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type testWorkflowNode struct{}

func (testWorkflowNode) Kind() NodeKind { return "test" }

func (testWorkflowNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusCompleted}, nil
}

func TestNodeRegistryResolvesRegisteredFactory(t *testing.T) {
	registry := NewNodeRegistry()
	registry.Register("test", func() WorkflowNode { return testWorkflowNode{} })

	node, err := registry.Resolve("test")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if node.Kind() != "test" {
		t.Fatalf("Resolve() kind = %q, want test", node.Kind())
	}
}

func TestNodeRegistryReturnsErrorForUnknownKind(t *testing.T) {
	registry := NewNodeRegistry()

	_, err := registry.Resolve("missing")
	if err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Resolve() error = %q, want missing kind", err.Error())
	}
}

func TestWorkflowNodeInterfaceMinimal(t *testing.T) {
	typ := reflect.TypeOf((*WorkflowNode)(nil)).Elem()
	if typ.NumMethod() != 2 {
		t.Fatalf("WorkflowNode has %d methods, want 2", typ.NumMethod())
	}
	if _, ok := typ.MethodByName("Kind"); !ok {
		t.Fatal("WorkflowNode missing Kind method")
	}
	if _, ok := typ.MethodByName("Execute"); !ok {
		t.Fatal("WorkflowNode missing Execute method")
	}
}

func TestRollbackTypesExposeFailureClasses(t *testing.T) {
	classes := []FailureClass{
		FailureClassNone,
		FailureClassHumanReject,
		FailureClassTransient,
		FailureClassMissingEvidence,
		FailureClassContractError,
		FailureClassInconclusive,
		FailureClassProductDefect,
		FailureClassEnvironmentBlock,
		FailureClassValidatorIssue,
		FailureClassInvalidInput,
		FailureClassUserBlocked,
		FailureClassUnsafeOrOutOfScope,
	}
	if len(classes) != 12 {
		t.Fatalf("len(classes) = %d, want 12", len(classes))
	}

	var _ RollbackCapableNode
}
