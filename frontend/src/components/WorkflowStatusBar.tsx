import { useWorkflowStore } from '../stores/workflowStore';

export function WorkflowStatusBar() {
  const { runId, currentNodeId, currentNodeStatus, wsConnected } = useWorkflowStore();

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'WAITING_USER_INPUT': return '#f59e0b';
      case 'PROCESSING': return '#3b82f6';
      case 'COMPLETED': return '#22c55e';
      case 'FAILED': return '#ef4444';
      default: return '#6b7280';
    }
  };

  return (
    <header className="workflow-status-bar">
      <div className="status-left">
        <span className="status-dot" style={{ backgroundColor: getStatusColor(currentNodeStatus) }} />
        <span className="status-label">Node: {currentNodeId}</span>
        <span className="status-badge" style={{ backgroundColor: getStatusColor(currentNodeStatus) }}>
          {currentNodeStatus}
        </span>
      </div>
      <div className="status-right">
        {runId && <span className="run-id">Run: {runId}</span>}
        <span className={`ws-indicator ${wsConnected ? 'connected' : ''}`}>
          {wsConnected ? 'SSE Connected' : 'SSE Disconnected'}
        </span>
      </div>
    </header>
  );
}
