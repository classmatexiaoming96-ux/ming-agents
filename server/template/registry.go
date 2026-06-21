// Package template provides the Template Registry for WDL template management
// and param-schema validation. This implements Epic 2.10.
package template

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ParamType represents the type of a template parameter.
type ParamType string

const (
	ParamTypeString  ParamType = "string"
	ParamTypeNumber  ParamType = "number"
	ParamTypeBoolean ParamType = "boolean"
	ParamTypeArray   ParamType = "array"
)

// ParamSchema describes a single parameter in a template's param schema.
type ParamSchema struct {
	Name        string      `json:"name"`
	Type        ParamType   `json:"type"`
	Description string      `json:"description,omitempty"`
	Required    bool        `json:"required"`
	Enum        []any       `json:"enum,omitempty"`        // allowed values (for enum type)
	Min         *float64    `json:"min,omitempty"`           // minimum value (for number type)
	Max         *float64    `json:"max,omitempty"`           // maximum value (for number type)
	Default     any         `json:"default,omitempty"`       // default value
}

// Template represents a registered WDL template with its param schema.
type Template struct {
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Version         string         `json:"version"`
	WDLContent      string         `json:"wdl_content"`
	ParamSchema     []*ParamSchema `json:"param_schema"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CriticallyNodes []CriticallyNode `json:"critically_nodes,omitempty"` // Epic 2.14: post-run validation checks
}

// RegistryError represents a validation error with details.
type RegistryError struct {
	Param   string `json:"param"`
	Message string `json:"message"`
}

func (e *RegistryError) Error() string {
	return fmt.Sprintf("parameter %q: %s", e.Param, e.Message)
}

// ValidationErrors collects multiple validation errors.
type ValidationErrors []RegistryError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return "validation succeeded"
	}
	if len(ve) == 1 {
		return ve[0].Error()
	}
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = e.Error()
	}
	return fmt.Sprintf("validation failed (%d errors): %s", len(ve), joinErrors(msgs, "; "))
}

func joinErrors(msgs []string, sep string) string {
	if len(msgs) == 0 {
		return ""
	}
	if len(msgs) == 1 {
		return msgs[0]
	}
	result := msgs[0]
	for i := 1; i < len(msgs); i++ {
		result += sep + msgs[i]
	}
	return result
}

// Registry manages template registration and validation.
type Registry struct {
	mu        sync.RWMutex
	templates map[string]*Template
}

// NewRegistry creates a new template registry.
func NewRegistry() *Registry {
	return &Registry{
		templates: make(map[string]*Template),
	}
}

// Register registers a template. Returns error if a template with the same name already exists.
func (r *Registry) Register(tmpl *Template) error {
	if tmpl.Name == "" {
		return errors.New("template name cannot be empty")
	}
	// Epic 2.14: Validate critically nodes at registration.
	if len(tmpl.CriticallyNodes) > 0 {
		if err := ValidateCriticallyNodes(tmpl.CriticallyNodes); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.templates[tmpl.Name]; exists {
		return fmt.Errorf("template %q already registered", tmpl.Name)
	}
	// Make a copy to prevent external mutation.
	t := *tmpl
	r.templates[tmpl.Name] = &t
	return nil
}

// RegisterOrUpdate registers a template, or updates it if it already exists.
func (r *Registry) RegisterOrUpdate(tmpl *Template) error {
	if tmpl.Name == "" {
		return errors.New("template name cannot be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := *tmpl
	r.templates[tmpl.Name] = &t
	return nil
}

// Get returns a template by name. Returns nil if not found.
func (r *Registry) Get(name string) *Template {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.templates[name]
}

// List returns all registered templates.
func (r *Registry) List() []*Template {
	r.mu.RLock()
	defer r.mu.RUnlock()
	templates := make([]*Template, 0, len(r.templates))
	for _, t := range r.templates {
		templates = append(templates, t)
	}
	return templates
}

// Validate validates a parameter map against the template's param schema.
// Returns ValidationErrors with details for each failed validation.
// Structural changes (WDL content changes) cannot occur via parameters -
// this validation only checks that provided params conform to the schema.
func (r *Registry) Validate(name string, params map[string]any) error {
	tmpl := r.Get(name)
	if tmpl == nil {
		return fmt.Errorf("template %q not found", name)
	}
	return ValidateParams(tmpl, params)
}

// ValidateParams validates params against a template's schema.
func ValidateParams(tmpl *Template, params map[string]any) error {
	var errs ValidationErrors

	// Build a map of param names for quick lookup.
	schemaMap := make(map[string]*ParamSchema)
	for _, p := range tmpl.ParamSchema {
		schemaMap[p.Name] = p
	}

	// Check for unknown parameters (params not in schema).
	for paramName := range params {
		if _, ok := schemaMap[paramName]; !ok {
			errs = append(errs, RegistryError{
				Param:   paramName,
				Message: "unknown parameter; not defined in template schema",
			})
		}
	}

	// Validate each schema parameter.
	for _, schema := range tmpl.ParamSchema {
		value, provided := params[schema.Name]

		// Check required.
		if schema.Required && !provided {
			errs = append(errs, RegistryError{
				Param:   schema.Name,
				Message: "required parameter is missing",
			})
			continue
		}

		// If not provided but has default, that's OK.
		if !provided {
			continue
		}

		// Validate type.
		if err := validateType(schema.Name, schema.Type, value); err != nil {
			errs = append(errs, RegistryError{
				Param:   schema.Name,
				Message: err.Error(),
			})
			continue
		}

		// Validate enum.
		if len(schema.Enum) > 0 {
			if !isValidEnum(value, schema.Enum) {
				errs = append(errs, RegistryError{
					Param:   schema.Name,
					Message: fmt.Sprintf("value %v is not one of the allowed values: %v", value, schema.Enum),
				})
				continue
			}
		}

		// Validate range (for number type).
		if schema.Type == ParamTypeNumber {
			if err := validateRange(schema.Name, value, schema.Min, schema.Max); err != nil {
				errs = append(errs, RegistryError{
					Param:   schema.Name,
					Message: err.Error(),
				})
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// validateType checks that a value matches the expected type.
func validateType(paramName string, expectedType ParamType, value any) error {
	if value == nil {
		return nil // nil is handled by required check
	}

	switch expectedType {
	case ParamTypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case ParamTypeNumber:
		if !isNumeric(value) {
			return fmt.Errorf("expected number, got %T", value)
		}
	case ParamTypeBoolean:
		if _, ok := value.(bool); ok {
			return nil
		}
		// Also accept string "true"/"false".
		if s, ok := value.(string); ok && (s == "true" || s == "false") {
			return nil
		}
		return fmt.Errorf("expected boolean, got %T", value)
	case ParamTypeArray:
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("expected array, got %T", value)
		}
	}
	return nil
}

// isNumeric returns true if the value is a numeric type (int, float, etc.).
func isNumeric(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, float32, float64, uint, uint8, uint16, uint32, uint64:
		return true
	case json.Number:
		return true
	}
	return false
}

// isValidEnum checks if a value is in the enum list.
func isValidEnum(value any, enum []any) bool {
	for _, e := range enum {
		if e == value {
			return true
		}
		// Also compare JSON representation for numeric equality.
		if isNumeric(value) && isNumeric(e) {
			if v, ok := toFloat64(value); ok {
				if e2, ok2 := toFloat64(e); ok2 && v == e2 {
					return true
				}
			}
		}
	}
	return false
}

// validateRange checks that a numeric value is within min/max bounds.
func validateRange(paramName string, value any, min, max *float64) error {
	num, ok := toFloat64(value)
	if !ok {
		return fmt.Errorf("cannot validate range for non-numeric value")
	}
	if min != nil && num < *min {
		return fmt.Errorf("value %v is below minimum allowed value %v", num, *min)
	}
	if max != nil && num > *max {
		return fmt.Errorf("value %v is above maximum allowed value %v", num, *max)
	}
	return nil
}

// toFloat64 converts a numeric value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}