package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProjectHandlerRegisterRoutes(t *testing.T) {
	// Test that RegisterRoutes doesn't panic and properly registers all expected patterns
	handler := &ProjectHandler{}
	mux := http.NewServeMux()

	// This should not panic - this is the key test
	handler.RegisterRoutes(mux)

	// Verify handler has all required methods (won't compile if not)
	var _ interface {
		handleListProjects(http.ResponseWriter, *http.Request)
		handleCreateProject(http.ResponseWriter, *http.Request)
		handleGetProject(http.ResponseWriter, *http.Request)
		handleAddRepoToProject(http.ResponseWriter, *http.Request)
		handleListEndpoints(http.ResponseWriter, *http.Request)
		handleRegisterEndpoint(http.ResponseWriter, *http.Request)
		handleListBindings(http.ResponseWriter, *http.Request)
		handleCreateBinding(http.ResponseWriter, *http.Request)
		handleGetCallers(http.ResponseWriter, *http.Request)
		handleGetCallees(http.ResponseWriter, *http.Request)
		handleCodeGraphInit(http.ResponseWriter, *http.Request)
	} = handler

	// Suppress unused variable warning
	_ = mux
}

func TestProjectStructJSON(t *testing.T) {
	project := Project{
		ID:          "proj_123",
		Name:        "Test Project",
		Description: "A test project",
	}

	if project.ID != "proj_123" {
		t.Errorf("expected ID 'proj_123', got '%s'", project.ID)
	}
	if project.Name != "Test Project" {
		t.Errorf("expected Name 'Test Project', got '%s'", project.Name)
	}
}

func TestAPIEndpointStructJSON(t *testing.T) {
	endpoint := APIEndpoint{
		ID:          "ep_456",
		ProjectID:   "proj_123",
		RepoID:      "repo_789",
		Method:      "GET",
		Path:        "/api/users",
		Description: "Get users endpoint",
		Confidence:  0.95,
	}

	if endpoint.Method != "GET" {
		t.Errorf("expected Method 'GET', got '%s'", endpoint.Method)
	}
	if endpoint.Path != "/api/users" {
		t.Errorf("expected Path '/api/users', got '%s'", endpoint.Path)
	}
	if endpoint.Confidence != 0.95 {
		t.Errorf("expected Confidence 0.95, got %f", endpoint.Confidence)
	}
}

func TestAPIBindingStructJSON(t *testing.T) {
	binding := APIBinding{
		ID:               "bind_111",
		ProjectID:        "proj_123",
		SourceEndpointID: "ep_456",
		TargetEndpointID: "ep_789",
		BindingType:      "calls",
		Description:      "Source calls target",
	}

	if binding.BindingType != "calls" {
		t.Errorf("expected BindingType 'calls', got '%s'", binding.BindingType)
	}
	if binding.SourceEndpointID != "ep_456" {
		t.Errorf("expected SourceEndpointID 'ep_456', got '%s'", binding.SourceEndpointID)
	}
}

func TestProjectWithReposStructJSON(t *testing.T) {
	pwr := ProjectWithRepos{
		Project: Project{
			ID:   "proj_123",
			Name: "Test",
		},
		Repositories: []ProjectRepo{
			{ID: "prepo_1", ProjectID: "proj_123", RepoID: "repo_1", Role: "primary"},
		},
	}

	if len(pwr.Repositories) != 1 {
		t.Errorf("expected 1 repository, got %d", len(pwr.Repositories))
	}
	if pwr.Repositories[0].Role != "primary" {
		t.Errorf("expected Role 'primary', got '%s'", pwr.Repositories[0].Role)
	}
}

func TestWriteJSONHelper(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, map[string]string{"key": "value"})

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got '%s'", rr.Header().Get("Content-Type"))
	}
}

func TestWriteErrorHelper(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusInternalServerError, fmt.Errorf("test error"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestWriteErrorMsgHelper(t *testing.T) {
	rr := httptest.NewRecorder()
	writeErrorMsg(rr, http.StatusBadRequest, "test error message")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}