package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const RunBundleLargeFileThreshold = 5 * 1024 * 1024

type NodeKind string

type ReuseAck struct {
	RunID     string          `json:"run_id"`
	Phase     string          `json:"phase"`
	Timestamp time.Time       `json:"timestamp"`
	Applied   json.RawMessage `json:"applied,omitempty"`
	Ignored   json.RawMessage `json:"ignored,omitempty"`
	Accepted  bool            `json:"accepted"`
	Note      string          `json:"note,omitempty"`
}

type runBundleManifest struct {
	Project        string         `json:"project"`
	RunID          string         `json:"run_id"`
	CreatedAt      string         `json:"created_at"`
	FrozenAt       *string        `json:"frozen_at"`
	State          string         `json:"state"`
	ArtifactCounts map[string]int `json:"artifact_counts"`
}

type runBundlePointer struct {
	SourcePath string `json:"source_path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

// RunBundleReceiver mirrors raw workflow artifacts into the L3 run bundle.
// Receive methods return write errors to callers for observability, but callers
// should treat those errors as soft failures and continue the workflow.
type RunBundleReceiver struct {
	project string
	runID   string
	root    string
}

// RunBundlePath returns the L3 raw run bundle namespace for one project run.
// It is intentionally separate from archive/<project>, which stores curated L2
// memory history rather than raw workflow artifacts.
func RunBundlePath(project, runID string) string {
	return filepath.Join(VaultDir, "runs", project, runID)
}

func NewRunBundleReceiver(project, runID string) *RunBundleReceiver {
	return &RunBundleReceiver{
		project: project,
		runID:   runID,
		root:    RunBundlePath(project, runID),
	}
}

func (r *RunBundleReceiver) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

func (r *RunBundleReceiver) ReceivePhaseReuse(phase, content string) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	name := safeBundleName(phase) + ".md"
	return r.writeArtifact(filepath.Join("phase-reuse", name), []byte(content))
}

func (r *RunBundleReceiver) ReceiveReuseAck(phase string, ack ReuseAck) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if ack.RunID == "" {
		ack.RunID = r.runID
	}
	if ack.Phase == "" {
		ack.Phase = phase
	}
	if ack.Timestamp.IsZero() {
		ack.Timestamp = time.Now().UTC()
	}
	return r.writeJSONArtifact(filepath.Join("reuse-ack", safeBundleName(phase)+".json"), ack)
}

func (r *RunBundleReceiver) ReceiveBriefAudit(kind NodeKind, audit *BriefAudit, auditName string) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if audit == nil {
		return nil
	}
	name := string(kind) + "-brief"
	if auditName != "" {
		name = safeBundleName(auditName) + "-brief"
	}
	return r.writeJSONArtifact(filepath.Join("brief-audit", name+".json"), audit)
}

func (r *RunBundleReceiver) ReceiveEvidencePointer(name, sourcePath string) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	filePointer, err := pointerForFile(sourcePath)
	if err != nil {
		return err
	}
	pointer := map[string]any{
		"name":        name,
		"source_path": sourcePath,
		"size":        filePointer.Size,
		"sha256":      filePointer.SHA256,
		"received_at": time.Now().UTC().Format(time.RFC3339),
	}
	line, err := json.Marshal(pointer)
	if err != nil {
		return err
	}
	return r.appendArtifact(filepath.Join("evidence", "pointers.jsonl"), append(line, '\n'))
}

func (r *RunBundleReceiver) ReceiveAutoMindSummary(rawContent []byte, format string) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	ext := ".md"
	if strings.EqualFold(format, "json") {
		ext = ".json"
	}
	return r.writeArtifact(filepath.Join("automind-summary", "raw-summary"+ext), rawContent)
}

func (r *RunBundleReceiver) Freeze() error {
	if r == nil {
		return errors.New("nil run bundle receiver")
	}
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return err
	}
	manifest, err := r.loadManifest()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	manifest.State = "frozen"
	manifest.FrozenAt = &now
	if err := r.writeManifest(manifest); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.root, "_frozen"), []byte(now+"\n"), 0644); err != nil {
		return err
	}
	return filepath.WalkDir(r.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(path, 0555)
		}
		return os.Chmod(path, 0444)
	})
}

func (r *RunBundleReceiver) ensureOpen() error {
	if r == nil {
		return errors.New("nil run bundle receiver")
	}
	if _, err := os.Stat(filepath.Join(r.root, "_frozen")); err == nil {
		return fmt.Errorf("run bundle %s is frozen", r.root)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	manifest, err := r.loadManifest()
	if err != nil {
		return err
	}
	if manifest.State == "frozen" {
		return fmt.Errorf("run bundle %s is frozen", r.root)
	}
	return nil
}

func (r *RunBundleReceiver) writeArtifact(rel string, data []byte) error {
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return err
	}
	if len(data) > RunBundleLargeFileThreshold {
		pointer := runBundlePointer{
			Size:   int64(len(data)),
			SHA256: sha256Hex(data),
		}
		ext := filepath.Ext(rel)
		pointerRel := strings.TrimSuffix(rel, ext) + ".pointer.json"
		return r.writeJSONArtifact(pointerRel, pointer)
	}
	path := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	manifest, err := r.loadManifest()
	if err != nil {
		return err
	}
	return r.writeManifest(manifest)
}

func (r *RunBundleReceiver) appendArtifact(rel string, data []byte) error {
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return err
	}
	path := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	manifest, err := r.loadManifest()
	if err != nil {
		return err
	}
	return r.writeManifest(manifest)
}

func (r *RunBundleReceiver) writeJSONArtifact(rel string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return r.writeArtifact(rel, append(data, '\n'))
}

func (r *RunBundleReceiver) loadManifest() (runBundleManifest, error) {
	path := filepath.Join(r.root, "manifest.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return runBundleManifest{
			Project:        r.project,
			RunID:          r.runID,
			CreatedAt:      time.Now().UTC().Format(time.RFC3339),
			State:          "open",
			ArtifactCounts: map[string]int{},
		}, nil
	}
	if err != nil {
		return runBundleManifest{}, err
	}
	var manifest runBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return runBundleManifest{}, err
	}
	if manifest.ArtifactCounts == nil {
		manifest.ArtifactCounts = map[string]int{}
	}
	return manifest, nil
}

func (r *RunBundleReceiver) writeManifest(manifest runBundleManifest) error {
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.root, "manifest.json"), append(data, '\n'), 0644)
}

func safeBundleName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "..", "-")
	return replacer.Replace(name)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func pointerForFile(path string) (runBundlePointer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runBundlePointer{}, err
	}
	return runBundlePointer{
		SourcePath: path,
		Size:       int64(len(data)),
		SHA256:     sha256Hex(data),
	}, nil
}
