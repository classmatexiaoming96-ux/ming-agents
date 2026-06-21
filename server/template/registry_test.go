package template

import (
	"encoding/json"
	"testing"
)

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	tmpl := &Template{
		Name:        "bugfix-loop",
		Description: "Bug fix loop template",
		Version:     "1.0.0",
		WDLContent:  "version: '1.0'\nsteps:\n  - name: locate\n  - name: fix",
		ParamSchema: []*ParamSchema{
			{Name: "prompt", Type: ParamTypeString, Required: true, Description: "The prompt for fixing"},
			{Name: "max_iterations", Type: ParamTypeNumber, Required: false, Default: 6.0},
		},
	}

	err := r.Register(tmpl)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Verify it's stored.
	got := r.Get("bugfix-loop")
	if got == nil {
		t.Fatal("Get() returned nil after Register()")
	}
	if got.Name != "bugfix-loop" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "bugfix-loop")
	}
	if got.Version != "1.0.0" {
		t.Errorf("Get().Version = %q, want %q", got.Version, "1.0.0")
	}
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	r := NewRegistry()
	tmpl := &Template{Name: "test", Version: "1.0"}

	err := r.Register(tmpl)
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	// Second registration should fail.
	err = r.Register(tmpl)
	if err == nil {
		t.Fatal("expected error on duplicate Register(), got nil")
	}
}

func TestRegistry_RegisterOrUpdate(t *testing.T) {
	r := NewRegistry()
	tmpl := &Template{Name: "test", Version: "1.0"}

	err := r.RegisterOrUpdate(tmpl)
	if err != nil {
		t.Fatalf("RegisterOrUpdate() error = %v", err)
	}

	// Update should succeed.
	tmpl.Version = "2.0"
	err = r.RegisterOrUpdate(tmpl)
	if err != nil {
		t.Fatalf("RegisterOrUpdate() update error = %v", err)
	}

	got := r.Get("test")
	if got.Version != "2.0" {
		t.Errorf("Get().Version = %q, want %q", got.Version, "2.0")
	}
}

func TestRegistry_Register_EmptyName(t *testing.T) {
	r := NewRegistry()
	tmpl := &Template{Name: "", Version: "1.0"}

	err := r.Register(tmpl)
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()

	// Get on empty registry.
	got := r.Get("nonexistent")
	if got != nil {
		t.Errorf("Get() on empty registry = %v, want nil", got)
	}

	// Register and get.
	r.Register(&Template{Name: "foo", Version: "1.0"})
	got = r.Get("foo")
	if got == nil {
		t.Fatal("Get() returned nil for registered template")
	}
	if got.Name != "foo" {
		t.Errorf("Get().Name = %q, want %q", got.Name, "foo")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()

	// List empty registry.
	list := r.List()
	if len(list) != 0 {
		t.Errorf("List() on empty registry = %d items, want 0", len(list))
	}

	// Register some templates.
	r.Register(&Template{Name: "a", Version: "1.0"})
	r.Register(&Template{Name: "b", Version: "1.0"})
	r.Register(&Template{Name: "c", Version: "1.0"})

	list = r.List()
	if len(list) != 3 {
		t.Errorf("List() = %d items, want 3", len(list))
	}
}

func TestRegistry_Validate(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "name", Type: ParamTypeString, Required: true},
			{Name: "count", Type: ParamTypeNumber, Required: false},
			{Name: "enabled", Type: ParamTypeBoolean, Required: false},
			{Name: "tags", Type: ParamTypeArray, Required: false},
			{Name: "model", Type: ParamTypeString, Required: false, Enum: []any{"gpt-4", "claude-3"}},
			{Name: "threshold", Type: ParamTypeNumber, Required: false, Min: floatPtr(0), Max: floatPtr(100)},
		},
	})

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid params",
			params:  map[string]any{"name": "hello", "count": 5.0, "enabled": true, "tags": []any{"a", "b"}, "model": "gpt-4", "threshold": 50.0},
			wantErr: false,
		},
		{
			name:    "valid with only required",
			params:  map[string]any{"name": "hello"},
			wantErr: false,
		},
		{
			name:    "valid with defaults",
			params:  map[string]any{"name": "hello", "count": json.Number("10")},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Validate("test", tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errMsg != "" && err != nil && err.Error() != tt.errMsg {
				// Partial match check.
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, want containing %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestValidate_TypeErrors(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "str_param", Type: ParamTypeString, Required: true},
			{Name: "num_param", Type: ParamTypeNumber, Required: true},
			{Name: "bool_param", Type: ParamTypeBoolean, Required: true},
			{Name: "arr_param", Type: ParamTypeArray, Required: true},
		},
	})

	tests := []struct {
		name   string
		params map[string]any
		param  string
	}{
		{"string got number", map[string]any{"str_param": 123, "num_param": 1.0, "bool_param": true, "arr_param": []any{1}}, "str_param"},
		{"string got array", map[string]any{"str_param": []any{"a"}, "num_param": 1.0, "bool_param": true, "arr_param": []any{1}}, "str_param"},
		{"number got string", map[string]any{"str_param": "hello", "num_param": "not a number", "bool_param": true, "arr_param": []any{1}}, "num_param"},
		{"boolean got string", map[string]any{"str_param": "hello", "num_param": 1.0, "bool_param": "not a bool", "arr_param": []any{1}}, "bool_param"},
		{"boolean got number", map[string]any{"str_param": "hello", "num_param": 1.0, "bool_param": 42, "arr_param": []any{1}}, "bool_param"},
		{"array got string", map[string]any{"str_param": "hello", "num_param": 1.0, "bool_param": true, "arr_param": "not an array"}, "arr_param"},
		{"array got number", map[string]any{"str_param": "hello", "num_param": 1.0, "bool_param": true, "arr_param": 42}, "arr_param"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Validate("test", tt.params)
			if err == nil {
				t.Fatalf("Validate() expected error for param %q, got nil", tt.param)
			}
			errMsg := err.Error()
			if !contains(errMsg, tt.param) {
				t.Errorf("Validate() error = %q, should mention param %q", errMsg, tt.param)
			}
		})
	}
}

func TestValidate_EnumViolation(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "model", Type: ParamTypeString, Enum: []any{"gpt-4", "claude-3", "o1"}},
		},
	})

	err := r.Validate("test", map[string]any{"model": "unknown-model"})
	if err == nil {
		t.Fatal("expected error for enum violation, got nil")
	}
	errMsg := err.Error()
	if !contains(errMsg, "model") {
		t.Errorf("Validate() error = %q, should mention param 'model'", errMsg)
	}
	if !contains(errMsg, "not one of the allowed values") {
		t.Errorf("Validate() error = %q, should mention enum violation", errMsg)
	}
}

func TestValidate_RangeViolation(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "threshold", Type: ParamTypeNumber, Min: floatPtr(0), Max: floatPtr(100)},
			{Name: "min_value", Type: ParamTypeNumber, Min: floatPtr(10)},
			{Name: "max_value", Type: ParamTypeNumber, Max: floatPtr(50)},
		},
	})

	tests := []struct {
		name    string
		params  map[string]any
		param   string
		errHint string
	}{
		{"below min", map[string]any{"threshold": -5.0}, "threshold", "below minimum"},
		{"above max", map[string]any{"threshold": 150.0}, "threshold", "above maximum"},
		{"above global min", map[string]any{"min_value": 5.0}, "min_value", "below minimum"},
		{"below global max", map[string]any{"max_value": 60.0}, "max_value", "above maximum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Validate("test", tt.params)
			if err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
			errMsg := err.Error()
			if !contains(errMsg, tt.param) {
				t.Errorf("Validate() error = %q, should mention param %q", errMsg, tt.param)
			}
			if !contains(errMsg, tt.errHint) {
				t.Errorf("Validate() error = %q, should mention %q", errMsg, tt.errHint)
			}
		})
	}
}

func TestValidate_RequiredMissing(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "required_str", Type: ParamTypeString, Required: true},
			{Name: "optional_str", Type: ParamTypeString, Required: false},
		},
	})

	err := r.Validate("test", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required param, got nil")
	}
	errMsg := err.Error()
	if !contains(errMsg, "required_str") {
		t.Errorf("Validate() error = %q, should mention 'required_str'", errMsg)
	}
	if !contains(errMsg, "required parameter is missing") {
		t.Errorf("Validate() error = %q, should mention 'required parameter is missing'", errMsg)
	}
}

func TestValidate_UnknownParam(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "name", Type: ParamTypeString, Required: true},
		},
	})

	err := r.Validate("test", map[string]any{"name": "hello", "unknown_param": "oops"})
	if err == nil {
		t.Fatal("expected error for unknown param, got nil")
	}
	errMsg := err.Error()
	if !contains(errMsg, "unknown_param") {
		t.Errorf("Validate() error = %q, should mention 'unknown_param'", errMsg)
	}
	if !contains(errMsg, "unknown parameter") {
		t.Errorf("Validate() error = %q, should mention 'unknown parameter'", errMsg)
	}
}

func TestValidate_TemplateNotFound(t *testing.T) {
	r := NewRegistry()

	err := r.Validate("nonexistent", map[string]any{"name": "hello"})
	if err == nil {
		t.Fatal("expected error for nonexistent template, got nil")
	}
	errMsg := err.Error()
	if !contains(errMsg, "not found") {
		t.Errorf("Validate() error = %q, should mention 'not found'", errMsg)
	}
}

func TestValidate_BooleanString(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "enabled", Type: ParamTypeBoolean, Required: false},
		},
	})

	// String "true" should be accepted.
	err := r.Validate("test", map[string]any{"enabled": "true"})
	if err != nil {
		t.Errorf("Validate() with string 'true' error = %v", err)
	}

	err = r.Validate("test", map[string]any{"enabled": "false"})
	if err != nil {
		t.Errorf("Validate() with string 'false' error = %v", err)
	}
}

func TestValidate_NumericTypes(t *testing.T) {
	r := NewRegistry()
	r.Register(&Template{
		Name:    "test",
		Version: "1.0",
		ParamSchema: []*ParamSchema{
			{Name: "count", Type: ParamTypeNumber, Required: true},
		},
	})

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"int", map[string]any{"count": int(42)}},
		{"int8", map[string]any{"count": int8(42)}},
		{"int16", map[string]any{"count": int16(42)}},
		{"int32", map[string]any{"count": int32(42)}},
		{"int64", map[string]any{"count": int64(42)}},
		{"float32", map[string]any{"count": float32(42.5)}},
		{"float64", map[string]any{"count": float64(42.5)}},
		{"uint", map[string]any{"count": uint(42)}},
		{"json.Number", map[string]any{"count": json.Number("42.5")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Validate("test", tt.params)
			if err != nil {
				t.Errorf("Validate() with %s error = %v", tt.name, err)
			}
		})
	}
}

// Helper functions.

func floatPtr(f float64) *float64 {
	return &f
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}