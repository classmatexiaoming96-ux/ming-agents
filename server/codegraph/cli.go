// Package codegraph provides a Go wrapper around the official CodeGraph CLI.
package codegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// CodeGraphCLI is the main interface for interacting with CodeGraph repositories.
type CodeGraphCLI struct {
	binaryPath string
	pool       *ProcessPool
}

// NewCodeGraphCLI creates a new CodeGraphCLI instance.
func NewCodeGraphCLI(binaryPath string) *CodeGraphCLI {
	return &CodeGraphCLI{
		binaryPath: binaryPath,
		pool:       NewProcessPool(2, 5*time.Minute),
	}
}

// StatusResult represents the output of `codegraph status --json`.
type StatusResult struct {
	Initialized   bool              `json:"initialized"`
	ProjectPath   string            `json:"projectPath"`
	FileCount     int               `json:"fileCount,omitempty"`
	NodeCount     int               `json:"nodeCount,omitempty"`
	EdgeCount     int               `json:"edgeCount,omitempty"`
	DbsizeBytes   int64 `json:"dbSizeBytes,omitempty"`
	Backend       string            `json:"backend,omitempty"`
	JournalMode   string            `json:"journalMode,omitempty"`
	NodesByKind   map[string]int    `json:"nodesByKind,omitempty"`
	Languages     []string          `json:"languages,omitempty"`
	PendingChanges *PendingChanges `json:"pendingChanges,omitempty"`
}

type PendingChanges struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed int `json:"removed"`
}

// SearchResult represents a single query result.
type SearchResult struct {
	Node      Node     `json:"node"`
	Score     float64  `json:"score"`
	Highlights []string `json:"highlights,omitempty"`
}

// Node represents a code graph node.
type Node struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualifiedName"`
	FilePath      string `json:"filePath"`
	Language      string `json:"language"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	StartColumn int    `json:"startColumn,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
	Signature     string `json:"signature,omitempty"`
	IsExported    bool   `json:"isExported,omitempty"`
}

// FileInfo represents a file entry from `codegraph files --json`.
type FileInfo struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	NodeCount int    `json:"nodeCount"`
	Size int    `json:"size"`
}

// QueryOpts contains options for query operations.
type QueryOpts struct {
	Limit int
}

// CallersResult represents the output of `codegraph callers --json`.
type CallersResult struct {
	Symbol  string   `json:"symbol"`
	Callers []Caller `json:"callers"`
}

// CalleeResult represents the output of `codegraph callees --json`.
type CalleeResult struct {
	Symbol  string   `json:"symbol"`
	Callees []Callee `json:"callees"`
}

// Caller represents a caller of a symbol.
type Caller struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	FilePath string `json:"filePath"`
	StartLine int    `json:"startLine,omitempty"`
}

// Callee represents a callee of a symbol.
type Callee struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	FilePath string `json:"filePath"`
	StartLine int    `json:"startLine,omitempty"`
}

// ContextResult represents the output of `codegraph context --json`.
type ContextResult struct {
	Query      string          `json:"query"`
	Subgraph   *Subgraph       `json:"subgraph,omitempty"`
	EntryPoints []EntryPoint  `json:"entryPoints,omitempty"`
	CodeBlocks []CodeBlock `json:"codeBlocks,omitempty"`
	RelatedFiles []string     `json:"relatedFiles,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Stats *ContextStats `json:"stats,omitempty"`
}

type Subgraph struct {
	Nodes map[string]json.RawMessage `json:"nodes"`
	Edges []Edge `json:"edges"`
	Roots []string                  `json:"roots"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type EntryPoint struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type CodeBlock struct {
	Content    string `json:"content"`
	FilePath   string `json:"filePath"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	Language   string `json:"language"`
}

type ContextStats struct {
	NodeCount      int `json:"nodeCount"`
	EdgeCount      int `json:"edgeCount"`
	FileCount      int `json:"fileCount"`
	CodeBlockCount int `json:"codeBlockCount"`
	TotalCodeSize  int `json:"totalCodeSize"`
}

// Status runs `codegraph status --json --path <repoPath>`.
func (c *CodeGraphCLI) Status(ctx context.Context, repoPath string) (*StatusResult, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, "status", "--json", "--path", repoPath)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph status: %w (stderr: %s)", err, stderr.String())
	}
	var result StatusResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse status result: %w", err)
	}
	return &result, nil
}

// Query runs `codegraph query --json --path <repoPath> <query>`.
func (c *CodeGraphCLI) Query(ctx context.Context, repoPath, query string, opts *QueryOpts) ([]SearchResult, error) {
	args := []string{"query", "--json", "--path", repoPath, query}
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph query: %w (stderr: %s)", err, stderr.String())
	}
	var results []SearchResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		return nil, fmt.Errorf("parse query results: %w", err)
	}
	return results, nil
}

// Files runs `codegraph files --json --path <repoPath>`.
func (c *CodeGraphCLI) Files(ctx context.Context, repoPath string) ([]FileInfo, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, "files", "--json", "--path", repoPath)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph files: %w (stderr: %s)", err, stderr.String())
	}
	var files []FileInfo
	if err := json.Unmarshal(out.Bytes(), &files); err != nil {
		return nil, fmt.Errorf("parse files result: %w", err)
	}
	return files, nil
}

// GetCallers runs `codegraph callers --json --path <repoPath> <symbol>`.
func (c *CodeGraphCLI) GetCallers(ctx context.Context, repoPath, symbol string) (*CallersResult, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, "callers", "--json", "--path", repoPath, symbol)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph callers: %w (stderr: %s)", err, stderr.String())
	}
	var result CallersResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse callers result: %w", err)
	}
	return &result, nil
}

// GetCallees runs `codegraph callees --json --path <repoPath> <symbol>`.
func (c *CodeGraphCLI) GetCallees(ctx context.Context, repoPath, symbol string) (*CalleeResult, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, "callees", "--json", "--path", repoPath, symbol)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph callees: %w (stderr: %s)", err, stderr.String())
	}
	var result CalleeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse callees result: %w", err)
	}
	return &result, nil
}

// GetContext runs `codegraph context --json --path <repoPath> <task>`.
func (c *CodeGraphCLI) GetContext(ctx context.Context, repoPath, task string) (*ContextResult, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, "context", "--json", "--path", repoPath, task)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph context: %w (stderr: %s)", err, stderr.String())
	}
	var result ContextResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse context result: %w", err)
	}
	return &result, nil
}

// Init runs `codegraph init --path <repoPath>`.
func (c *CodeGraphCLI) Init(ctx context.Context, repoPath string) error {
	cmd := exec.CommandContext(ctx, c.binaryPath, "init", "--path", repoPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codegraph init: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}

// Index runs `codegraph index --path <repoPath>`.
func (c *CodeGraphCLI) Index(ctx context.Context, repoPath string) error {
	cmd := exec.CommandContext(ctx, c.binaryPath, "index", "--path", repoPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codegraph index: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}

// ProcessPool returns the process pool for low-latency queries.
func (c *CodeGraphCLI) ProcessPool() *ProcessPool {
	return c.pool
}