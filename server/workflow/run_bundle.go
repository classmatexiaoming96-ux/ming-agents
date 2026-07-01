package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ming-agents/server/memory"
)

func runBundleReceiver(repoRoot, runID string) *memory.RunBundleReceiver {
	if repoRoot == "" || runID == "" {
		return nil
	}
	project := projectFromRepoRoot(repoRoot)
	if project == "" {
		project = reuseProject
	}
	receiver, err := memory.NewRunBundleReceiver(project, runID)
	if err != nil {
		log.Printf("NewRunBundleReceiver failed: %v", err)
		recordRunBundleReceiverInitFailure(project, runID, err)
		return nil
	}
	return receiver
}

func recordRunBundleReceiverInitFailure(project, runID string, initErr error) {
	sum := sha256.Sum256([]byte(project + "\x00" + runID))
	root := filepath.Join(memory.VaultDir, "runs", "_receiver-errors", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(root, 0755); err != nil {
		log.Printf("RunBundleReceiver init failure status mkdir failed: %v", err)
		return
	}
	status := map[string]any{
		"receiver_init": map[string]string{
			"status":     "failed",
			"error":      initErr.Error(),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		log.Printf("RunBundleReceiver init failure status marshal failed: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(root, "receiver-status.json"), append(data, '\n'), 0644); err != nil {
		log.Printf("RunBundleReceiver init failure status write failed: %v", err)
	}
}

func freezeRunBundle(repoRoot, runID string) {
	receiver := runBundleReceiver(repoRoot, runID)
	if receiver == nil {
		return
	}
	if err := receiver.Freeze(); err != nil {
		log.Printf("RunBundleReceiver.Freeze failed: %v", err)
	}
}

func mirrorPhaseReuseToRunBundle(req NodeRequest, phase, path string) {
	receiver := runBundleReceiver(req.RepoRoot, req.RunID)
	if receiver == nil || path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("RunBundleReceiver.ReceivePhaseReuse read failed: %v", err)
		return
	}
	if err := receiver.ReceivePhaseReuse(phase, string(data)); err != nil {
		log.Printf("RunBundleReceiver.ReceivePhaseReuse failed: %v", err)
	}
}

func mirrorReuseAckToRunBundle(req NodeRequest, phase string, ack ReuseAck) {
	receiver := runBundleReceiver(req.RepoRoot, req.RunID)
	if receiver == nil {
		return
	}
	bundleAck := memory.ReuseAck{
		RunID:     firstNonEmpty(ack.RunID, req.RunID),
		Phase:     firstNonEmpty(ack.Phase, phase),
		Timestamp: ack.Timestamp,
		Accepted:  ack.Accepted,
		Note:      ack.Note,
	}
	bundleAck.Applied, _ = json.Marshal(ack.Applied)
	bundleAck.Ignored, _ = json.Marshal(ack.Ignored)
	if err := receiver.ReceiveReuseAck(phase, bundleAck); err != nil {
		log.Printf("RunBundleReceiver.ReceiveReuseAck failed: %v", err)
	}
}

func mirrorReuseAckFileToRunBundle(req NodeRequest, phase string) {
	path := filepath.Join(req.RepoRoot, ".workflow", "runs", req.RunID, "reuse-ack.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var ack ReuseAck
	if err := json.Unmarshal(data, &ack); err != nil {
		log.Printf("RunBundleReceiver reuse-ack decode failed: %v", err)
		return
	}
	mirrorReuseAckToRunBundle(req, phase, ack)
}

func mirrorBriefAuditToRunBundle(req NodeRequest, brief *BriefInjectResult, auditName string) {
	receiver := runBundleReceiver(req.RepoRoot, req.RunID)
	if receiver == nil || brief == nil || brief.Audit == nil {
		return
	}
	if err := receiver.ReceiveBriefAudit(memory.NodeKind(req.Spec.Kind), brief.Audit, auditName); err != nil {
		log.Printf("RunBundleReceiver.ReceiveBriefAudit failed: %v", err)
	}
}

func mirrorEvidenceToRunBundle(req NodeRequest, result *EvaluationResult) {
	receiver := runBundleReceiver(req.RepoRoot, req.RunID)
	if receiver == nil || result == nil {
		return
	}
	for _, ref := range result.Evidence {
		if ref.Path == "" {
			continue
		}
		name := filepath.Base(ref.Path)
		if name == "." || name == string(filepath.Separator) {
			name = string(ref.Type)
		}
		if err := receiver.ReceiveEvidencePointer(name, ref.Path); err != nil {
			log.Printf("RunBundleReceiver.ReceiveEvidencePointer failed: %v", err)
		}
	}
}
