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
	"unicode"
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

type runBundleReceiverStatus map[string]runBundleArtifactStatus

type runBundleArtifactStatus struct {
	Status    string   `json:"status"`
	Files     []string `json:"files,omitempty"`
	Error     string   `json:"error,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

// RunBundleReceiver mirrors raw workflow artifacts into the L3 run bundle.
//
// Soft-fail contract: Receive methods return write errors for observability and
// record receiver-status.json when possible, but workflow callers must not let
// those errors change NodeResult or block the main run. Freeze marks the bundle
// immutable with manifest state, _frozen, and read-only file modes.
type RunBundleReceiver struct {
	project string
	runID   string
	root    string
}

// RunBundlePath returns the L3 raw run bundle namespace for one project run.
// It is intentionally separate from archive/<project>, which stores curated L2
// memory history rather than raw workflow artifacts.
func RunBundlePath(project, runID string) (string, error) {
	if err := validateProjectID(project); err != nil {
		return "", err
	}
	if err := validateRunID(runID); err != nil {
		return "", err
	}
	return filepath.Join(VaultDir, "runs", project, runID), nil
}

func NewRunBundleReceiver(project, runID string) (*RunBundleReceiver, error) {
	root, err := RunBundlePath(project, runID)
	if err != nil {
		return nil, err
	}
	return &RunBundleReceiver{
		project: project,
		runID:   runID,
		root:    root,
	}, nil
}

func (r *RunBundleReceiver) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

func (r *RunBundleReceiver) ReceivePhaseReuse(phase, content string) error {
	var files []string
	err := func() error {
		if err := r.ensureOpen(); err != nil {
			return err
		}
		name := safeBundleName(phase) + ".md"
		written, err := r.writeArtifact(filepath.Join("phase-reuse", name), []byte(content))
		files = append(files, written)
		return err
	}()
	return r.recordReceiveStatus("phase_reuse", files, err)
}

func (r *RunBundleReceiver) ReceiveReuseAck(phase string, ack ReuseAck) error {
	var files []string
	err := func() error {
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
		written, err := r.writeJSONArtifact(filepath.Join("reuse-ack", safeBundleName(phase)+".json"), ack)
		files = append(files, written)
		return err
	}()
	return r.recordReceiveStatus("reuse_ack", files, err)
}

func (r *RunBundleReceiver) ReceiveBriefAudit(kind NodeKind, audit *BriefAudit, auditName string) error {
	var files []string
	err := func() error {
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
		written, err := r.writeJSONArtifact(filepath.Join("brief-audit", name+".json"), audit)
		files = append(files, written)
		return err
	}()
	if audit == nil && err == nil {
		return r.recordSkippedStatus("brief_audit")
	}
	return r.recordReceiveStatus("brief_audit", files, err)
}

func (r *RunBundleReceiver) ReceiveEvidencePointer(name, sourcePath string) error {
	var files []string
	err := func() error {
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
		written, err := r.appendArtifact(filepath.Join("evidence", "pointers.jsonl"), append(line, '\n'))
		files = append(files, written)
		return err
	}()
	return r.recordReceiveStatus("evidence_pointers", files, err)
}

func (r *RunBundleReceiver) ReceiveAutoMindSummary(rawContent []byte, format string) error {
	var files []string
	err := func() error {
		if err := r.ensureOpen(); err != nil {
			return err
		}
		ext := ".md"
		if strings.EqualFold(format, "json") {
			ext = ".json"
		}
		written, err := r.writeArtifact(filepath.Join("automind-summary", "raw-summary"+ext), rawContent)
		files = append(files, written)
		return err
	}()
	return r.recordReceiveStatus("automind_summary", files, err)
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

func (r *RunBundleReceiver) writeArtifact(rel string, data []byte) (string, error) {
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return "", err
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
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	manifest, err := r.loadManifest()
	if err != nil {
		return "", err
	}
	return rel, r.writeManifest(manifest)
}

func (r *RunBundleReceiver) appendArtifact(rel string, data []byte) (string, error) {
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	manifest, err := r.loadManifest()
	if err != nil {
		return "", err
	}
	return rel, r.writeManifest(manifest)
}

func (r *RunBundleReceiver) writeJSONArtifact(rel string, value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
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
	manifest.ArtifactCounts = r.artifactCounts()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.root, "manifest.json"), append(data, '\n'), 0644)
}

func (r *RunBundleReceiver) artifactCounts() map[string]int {
	return map[string]int{
		"phase_reuse":       countRegularFiles(filepath.Join(r.root, "phase-reuse")),
		"reuse_ack":         countRegularFiles(filepath.Join(r.root, "reuse-ack")),
		"brief_audit":       countRegularFiles(filepath.Join(r.root, "brief-audit")),
		"evidence_pointers": countJSONLLines(filepath.Join(r.root, "evidence", "pointers.jsonl")),
		"automind_summary":  countRegularFiles(filepath.Join(r.root, "automind-summary")),
	}
}

func countRegularFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			count++
		}
	}
	return count
}

func countJSONLLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (r *RunBundleReceiver) recordReceiveStatus(artifact string, files []string, receiveErr error) error {
	status := "ok"
	message := ""
	if receiveErr != nil {
		status = "failed"
		message = receiveErr.Error()
	}
	statusErr := r.writeReceiverStatus(artifact, runBundleArtifactStatus{
		Status:    status,
		Files:     compactNonEmpty(files),
		Error:     message,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if receiveErr != nil {
		return receiveErr
	}
	return statusErr
}

func (r *RunBundleReceiver) recordSkippedStatus(artifact string) error {
	return r.writeReceiverStatus(artifact, runBundleArtifactStatus{
		Status:    "skipped",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (r *RunBundleReceiver) writeReceiverStatus(artifact string, entry runBundleArtifactStatus) error {
	if r == nil {
		return errors.New("nil run bundle receiver")
	}
	if err := os.MkdirAll(r.root, 0755); err != nil {
		return err
	}
	path := filepath.Join(r.root, "receiver-status.json")
	status := runBundleReceiverStatus{}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &status)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	status[artifact] = entry
	data, err = json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func compactNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
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

func validateRunID(id string) error {
	return validateBundleID("run_id", id)
}

func validateProjectID(id string) error {
	return validateBundleID("project", id)
}

func validateBundleID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if filepath.IsAbs(id) || strings.HasPrefix(id, "/") {
		return fmt.Errorf("%s %q must not be absolute", kind, id)
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("%s %q must not contain path separators", kind, id)
	}
	if id == "." || id == ".." {
		return fmt.Errorf("%s %q must not be a dot segment", kind, id)
	}
	if filepath.Clean(id) != id {
		return fmt.Errorf("%s %q changes after path cleaning", kind, id)
	}
	for _, r := range id {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return fmt.Errorf("%s %q contains non-printable characters", kind, id)
		}
	}
	if isWindowsReservedName(id) {
		return fmt.Errorf("%s %q uses a reserved Windows name", kind, id)
	}
	return nil
}

func isWindowsReservedName(id string) bool {
	name := strings.ToUpper(id)
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		name = name[:dot]
	}
	switch name {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9' {
		return true
	}
	return false
}
