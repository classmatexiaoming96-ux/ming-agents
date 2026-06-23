import { useCallback, useEffect, useMemo } from 'react';
import { ConversationPanel } from '../components/ConversationPanel';
import { useGlobalRuns, type RunSummary } from '../hooks/useGlobalRuns';
import { usePiAgent } from '../hooks/usePiAgent';
import { useWorkflowStore, type RunStatus } from '../stores/workflowStore';

export function PermanentAgentPage() {
  const { runs, loading, error, refresh } = useGlobalRuns({ limit: 20 });
  const { runId, setRunId, setCurrentNode, setRunStatus } = useWorkflowStore();

  const loadRunStatus = useCallback(
    async (id: string) => {
      const response = await fetch(`/api/runs/${id}/status`);
      if (!response.ok) {
        return;
      }
      const status = (await response.json()) as RunStatus;
      setRunStatus(status);
      if (status.current_node) {
        const current = status.nodes.find((node) => node.id === status.current_node);
        setCurrentNode(status.current_node, current?.status || status.status);
      }
    },
    [setCurrentNode, setRunStatus]
  );

  useEffect(() => {
    if (!runId && runs.length > 0) {
      setRunId(runs[0].run_id);
      void loadRunStatus(runs[0].run_id);
    }
  }, [loadRunStatus, runId, runs, setRunId]);

  const selectedRun = useMemo(
    () => runs.find((run) => run.run_id === runId) || null,
    [runId, runs]
  );

  const selectRun = useCallback(
    (run: RunSummary) => {
      setRunId(run.run_id);
      void loadRunStatus(run.run_id);
    },
    [loadRunStatus, setRunId]
  );

  return (
    <div className="agent-page">
      <aside className="runs-sidebar">
        <div className="runs-header">
          <h1>Agent</h1>
          <button type="button" onClick={refresh}>
            刷新
          </button>
        </div>
        {loading && <p className="runs-state">加载中...</p>}
        {error && <p className="runs-state error">{error}</p>}
        <div className="run-list">
          {runs.map((run) => (
            <button
              key={run.run_id}
              type="button"
              className={`run-row ${run.run_id === runId ? 'selected' : ''}`}
              onClick={() => selectRun(run)}
            >
              <span className="run-row-main">
                <span>{run.current_node || '无当前节点'}</span>
                <span className="run-row-status">{run.status}</span>
              </span>
              <span className="run-row-id">{run.run_id}</span>
            </button>
          ))}
        </div>
      </aside>
      <main className="agent-main">
        {selectedRun ? (
          <AgentConversation runId={selectedRun.run_id} />
        ) : (
          <div className="agent-empty">暂无可用 Run</div>
        )}
      </main>
    </div>
  );
}

function AgentConversation({ runId }: { runId: string }) {
  const { sendMessage } = usePiAgent({ runId });
  const { isStreaming } = useWorkflowStore();

  return (
    <ConversationPanel
      onSendMessage={sendMessage}
      disabled={false}
      isStreaming={isStreaming}
    />
  );
}
