import { useWorkflowStore } from '../stores/workflowStore';

interface ArtifactPanelProps {
  runId: string | null;
  nodeId: string;
}

export function ArtifactPanel({ runId, nodeId }: ArtifactPanelProps) {
  const { runStatus } = useWorkflowStore();

  // Find artifact for current node
  const nodeArtifact = runStatus?.nodes.find(n => n.id === nodeId)?.artifact;

  return (
    <aside className="artifact-panel">
      <h2>Artifact</h2>
      <div className="artifact-content">
        {nodeArtifact ? (
          <pre className="artifact-code">{nodeArtifact}</pre>
        ) : (
          <p className="artifact-empty">
            {runId
              ? 'No artifact for current node'
              : 'Start a run to see artifacts'}
          </p>
        )}
      </div>
    </aside>
  );
}
