import { useEffect, useState } from 'react';

interface EvidenceItem {
  subtask_id: string;
  evidence_type: 'build_log' | 'test_log' | 'screenshot' | 'document' | 'code_artifacts';
  path: string;
  verified: boolean;
}

interface CompletionCheck {
  run_id: string;
  passed: boolean;
  evidence_index?: EvidenceItem[];
  missing?: string[];
  blocked_items?: { subtask_id: string; reason: string }[];
}

interface PhaseStatus {
  run_id: string;
  phase: string;
  gate_status: string;
  failure_class?: string;
  next_action: string;
  next_action_prompt?: string;
  missing_items?: string[];
  updated_at: string;
}

interface EvaluationResult {
  run_id: string;
  passed: boolean;
  test_results?: { test_id: string; passed: boolean; command: string }[];
  failure_class?: string;
  evidence?: { type: string; path: string }[];
}

interface EvidencePanelProps {
  runId: string | null;
}

const EVIDENCE_ICONS: Record<string, string> = {
  build_log: '📋',
  test_log: '🧪',
  screenshot: '🖼️',
  document: '📄',
  code_artifacts: '📁',
};

const PHASES = ['clarification', 'planning', 'development', 'evaluation', 'review', 'completed'] as const;

function PhaseProgressBar({ phase, gateStatus }: { phase: string; gateStatus: string }) {
  const currentIndex = PHASES.indexOf(phase as typeof PHASES[number]);
  return (
    <div className="phase-progress-bar">
      {PHASES.map((p, i) => {
        const isDone = i < currentIndex;
        const isCurrent = i === currentIndex;
        let dotClass = 'phase-dot';
        if (isDone) dotClass += ' done';
        else if (isCurrent) dotClass += ` current status-${gateStatus}`;
        else dotClass += ' pending';
        return (
          <div key={p} className="phase-step">
            <div className={dotClass} title={p} />
            <span className={`phase-label ${isCurrent ? 'current' : ''}`}>{p}</span>
            {i < PHASES.length - 1 && (
              <div className={`phase-connector ${isDone ? 'done' : ''}`} />
            )}
          </div>
        );
      })}
    </div>
  );
}

function GateBadge({ status }: { status: string }) {
  const map: Record<string, { label: string; cls: string }> = {
    passed: { label: '✓ 通过', cls: 'badge-passed' },
    failed: { label: '✗ 失败', cls: 'badge-failed' },
    waiting_user: { label: '⏳ 等待审批', cls: 'badge-waiting' },
    blocked: { label: '🚫 阻塞', cls: 'badge-blocked' },
  };
  const info = map[status] ?? { label: status, cls: 'badge-default' };
  return <span className={`gate-badge ${info.cls}`}>{info.label}</span>;
}

function NextActionHint({ action }: { action: string }) {
  const hints: Record<string, string> = {
    finish: '✅ 流程完成',
    ask_user: '⏳ 等待人工审批',
    run_evaluator: '🧪 运行验证器',
    retry_generator: '🔁 重试生成',
    retry_evaluation: '🔁 重试验证',
    fix_environment: '🔧 修复环境问题',
  };
  return (
    <span className="next-action-hint">
      {hints[action] ?? action}
    </span>
  );
}

export function EvidencePanel({ runId }: EvidencePanelProps) {
  const [phaseStatus, setPhaseStatus] = useState<PhaseStatus | null>(null);
  const [completionCheck, setCompletionCheck] = useState<CompletionCheck | null>(null);
  const [evaluation, setEvaluation] = useState<EvaluationResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedSubtask, setExpandedSubtask] = useState<string | null>(null);

  useEffect(() => {
    if (!runId) return;
    setLoading(true);
    setError(null);

    Promise.all([
      fetch(`/api/runs/${runId}/phase-status`).then(r => r.ok ? r.json() : null).catch(() => null),
      fetch(`/api/runs/${runId}/evaluation`).then(r => r.ok ? r.json() : null).catch(() => null),
    ]).then(([ps, ev]) => {
      setPhaseStatus(ps);
      setEvaluation(ev);
      // completion_check is embedded in phase-status or fetched separately
      // Build a synthetic completion check from phase-status + evidence
      if (ps) {
        setCompletionCheck({
          run_id: runId,
          passed: ps.gate_status === 'passed',
          missing: ps.missing_items,
        });
      }
      setLoading(false);
    }).catch(() => {
      setError('加载失败');
      setLoading(false);
    });
  }, [runId]);

  if (!runId) {
    return (
      <section className="evidence-panel" aria-label="Evidence">
        <div className="evidence-empty">No run selected</div>
      </section>
    );
  }

  if (loading) {
    return (
      <section className="evidence-panel" aria-label="Evidence">
        <div className="evidence-loading">加载中...</div>
      </section>
    );
  }

  if (error) {
    return (
      <section className="evidence-panel" aria-label="Evidence">
        <div className="evidence-error">{error}</div>
      </section>
    );
  }

  const groupedEvidence = new Map<string, EvidenceItem[]>();
  completionCheck?.evidence_index?.forEach(item => {
    const list = groupedEvidence.get(item.subtask_id) ?? [];
    list.push(item);
    groupedEvidence.set(item.subtask_id, list);
  });

  return (
    <section className="evidence-panel" aria-label="Evidence">
      <div className="evidence-header">
        <h2>Evidence</h2>
        {phaseStatus && (
          <div className="evidence-header-meta">
            <GateBadge status={phaseStatus.gate_status} />
            <NextActionHint action={phaseStatus.next_action} />
          </div>
        )}
      </div>

      {phaseStatus && (
        <PhaseProgressBar phase={phaseStatus.phase} gateStatus={phaseStatus.gate_status} />
      )}

      {phaseStatus?.missing_items && phaseStatus.missing_items.length > 0 && (
        <div className="evidence-missing">
          <strong>缺失项：</strong>
          <ul>
            {phaseStatus.missing_items.map((m, i) => (
              <li key={i} className="evidence-missing-item">🔴 {m}</li>
            ))}
          </ul>
        </div>
      )}

      {completionCheck?.blocked_items && completionCheck.blocked_items.length > 0 && (
        <div className="evidence-blocked">
          <strong>被阻塞项：</strong>
          {completionCheck.blocked_items.map((b, i) => (
            <div key={i} className="evidence-blocked-item">
              <span className="evidence-blocked-subtask">🔴 {b.subtask_id}</span>
              <span className="evidence-blocked-reason">{b.reason}</span>
            </div>
          ))}
        </div>
      )}

      <div className="evidence-list">
        {groupedEvidence.size === 0 && (
          <div className="evidence-empty">
            {completionCheck?.passed
              ? '✅ 所有检查通过，无缺失项'
              : '⚠️ 暂无证据数据'}
          </div>
        )}
        {Array.from(groupedEvidence.entries()).map(([subtaskId, items]) => (
          <div key={subtaskId} className="evidence-subtask-group">
            <button
              className="evidence-subtask-header"
              onClick={() => setExpandedSubtask(expandedSubtask === subtaskId ? null : subtaskId)}
              type="button"
            >
              <span className="evidence-subtask-toggle">
                {expandedSubtask === subtaskId ? '▼' : '▶'}
              </span>
              <span className="evidence-subtask-label">{subtaskId}</span>
              <span className="evidence-subtask-summary">
                {items.filter(i => i.verified).length}/{items.length} ✓
              </span>
            </button>
            {expandedSubtask === subtaskId && (
              <div className="evidence-subtask-items">
                {items.map((item, i) => (
                  <div key={i} className={`evidence-item ${item.verified ? 'verified' : 'unverified'}`}>
                    <span className="evidence-item-icon">{EVIDENCE_ICONS[item.evidence_type] ?? '📎'}</span>
                    <span className="evidence-item-type">{item.evidence_type}</span>
                    <span className="evidence-item-path" title={item.path}>{item.path.split('/').pop()}</span>
                    <span className={`evidence-item-status ${item.verified ? 'status-ok' : 'status-fail'}`}>
                      {item.verified ? '✓' : '✗'}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>

      {evaluation?.test_results && evaluation.test_results.length > 0 && (
        <div className="evidence-test-results">
          <h3>验证结果</h3>
          {evaluation.test_results.map((tr, i) => (
            <div key={i} className={`test-result-row ${tr.passed ? 'pass' : 'fail'}`}>
              <span className="test-result-status">{tr.passed ? '✅' : '❌'}</span>
              <span className="test-result-id">{tr.test_id}</span>
              <code className="test-result-cmd">{tr.command}</code>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
