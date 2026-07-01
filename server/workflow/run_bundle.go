package workflow

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

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
	return memory.NewRunBundleReceiver(project, runID)
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
