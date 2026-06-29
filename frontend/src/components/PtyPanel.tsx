import { useEffect, useMemo, useState } from 'react';
import type { NodeInfo } from '../stores/workflowStore';
import { XtermTerminal } from './XtermTerminal';

export interface PTYSessionInfo {
  sessionId: string;
  runId: string;
  stepId: string;
  nodeName: string;
  subtaskId: string;
  agentType: string;
  status: string;
  workDir: string;
  createdAt: string;
}

interface PtyPanelProps {
  sessions: PTYSessionInfo[];
  fallbackNodes?: NodeInfo[];
  gateStatus?: string;
}

function sessionLabel(session: PTYSessionInfo) {
  return (
    session.subtaskId ||
    session.nodeName ||
    session.agentType ||
    session.sessionId.slice(0, 8)
  );
}

function isActiveStatus(status: string) {
  return ['starting', 'running', 'waiting_input'].includes(status.toLowerCase());
}

export function PtyPanel({ sessions, fallbackNodes = [], gateStatus }: PtyPanelProps) {
  const [activeId, setActiveId] = useState<string | null>(
    sessions[0]?.sessionId ?? null
  );
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (sessions.length === 0) {
      setActiveId(null);
      return;
    }
    if (!activeId || !sessions.some((session) => session.sessionId === activeId)) {
      setActiveId(sessions[0].sessionId);
    }
  }, [activeId, sessions]);

  const activeSession = useMemo(
    () => sessions.find((session) => session.sessionId === activeId) ?? null,
    [activeId, sessions]
  );

  if (sessions.length === 0) {
    return (
      <section className="pty-panel" aria-label="PTY sessions">
        <div className="pty-panel-header">
          <h2>PTY Sessions</h2>
          <span className="pty-connection-state idle">idle</span>
        </div>
        {fallbackNodes.length > 0 ? (
          <div className="pty-node-fallback">
            {fallbackNodes.map((node) => (
              <div key={node.id} className="pty-node-row">
                <span>{node.name || node.id}</span>
                <strong>{node.status}</strong>
              </div>
            ))}
          </div>
        ) : (
          <div className="pty-empty">No PTY sessions for this run</div>
        )}
      </section>
    );
  }

  if (gateStatus === 'waiting_user') {
    return (
      <section className="pty-panel" aria-label="PTY sessions">
        <div className="pty-panel-header">
          <h2>PTY Sessions</h2>
          <span className="pty-connection-state idle">approval</span>
        </div>
        <div className="pty-approval-card">
          <div className="pty-approval-icon" aria-hidden="true">
            ⏳
          </div>
          <div className="pty-approval-content">
            <h3>需要人工审批</h3>
            <p>当前节点正在等待审批。</p>
            <p>请前往任务详情页面确认。</p>
            <button
              type="button"
              onClick={() =>
                document
                  .querySelector('.task-detail')
                  ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
              }
            >
              查看任务详情
            </button>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section className="pty-panel" aria-label="PTY sessions">
      <div className="pty-panel-header">
        <h2>PTY Sessions</h2>
        <span className={`pty-connection-state ${connected ? 'connected' : 'idle'}`}>
          {connected ? 'connected' : 'disconnected'}
        </span>
      </div>
      <div className="pty-tabs" role="tablist" aria-label="PTY session tabs">
        {sessions.map((session) => (
          <button
            key={session.sessionId}
            type="button"
            role="tab"
            aria-selected={session.sessionId === activeId}
            className={session.sessionId === activeId ? 'active' : ''}
            onClick={() => setActiveId(session.sessionId)}
            title={`${session.sessionId} ${session.workDir}`}
          >
            <span
              className={`sub-agent-status-dot ${
                isActiveStatus(session.status) ? 'running' : 'idle'
              }`}
            />
            <span className="pty-tab-label">{sessionLabel(session)}</span>
            <span className="pty-tab-meta">{session.agentType}</span>
          </button>
        ))}
      </div>
      <div className="pty-content">
        {activeSession && (
          <XtermTerminal
            key={activeSession.sessionId}
            sessionId={activeSession.sessionId}
            onConnectionChange={setConnected}
          />
        )}
      </div>
    </section>
  );
}
