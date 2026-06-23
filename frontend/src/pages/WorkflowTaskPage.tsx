import { useCallback, useEffect, useRef, useState } from 'react';
import { WorkflowStatusBar } from '../components/WorkflowStatusBar';
import { ConversationPanel } from '../components/ConversationPanel';
import { ArtifactPanel } from '../components/ArtifactPanel';
import {
  NotificationToast,
  type ToastNotification,
} from '../components/NotificationToast';
import { useRunStatusSSE, type StepStatusChange } from '../hooks/useRunStatusSSE';
import { useWorkflowStore } from '../stores/workflowStore';

function runIdFromPath() {
  const match = window.location.pathname.match(/^\/runs\/([^/]+)/);
  return match?.[1] || null;
}

export function WorkflowTaskPage() {
  const {
    runId,
    currentNodeId,
    currentNodeStatus,
    addMessage,
    setCurrentNode,
    setRunId,
    setRunStatus,
    setWsConnected,
  } = useWorkflowStore();
  const [notifications, setNotifications] = useState<ToastNotification[]>([]);
  const conversationRef = useRef<HTMLDivElement>(null);

  const effectiveRunId = runId || runIdFromPath();
  const isWaitingForInput =
    currentNodeStatus === 'WAITING_USER_INPUT' ||
    currentNodeStatus === 'waiting_user_input';

  useEffect(() => {
    if (effectiveRunId && effectiveRunId !== runId) {
      setRunId(effectiveRunId);
    }
  }, [effectiveRunId, runId, setRunId]);

  const refreshRunStatus = useCallback(
    async (id: string) => {
      const response = await fetch(`/api/runs/${id}/status`);
      if (!response.ok) {
        return;
      }
      const status = await response.json();
      setRunStatus(status);
      if (status.current_node) {
        const current = status.nodes?.find(
          (node: { id: string }) => node.id === status.current_node
        );
        setCurrentNode(status.current_node, current?.status || status.status);
      }
    },
    [setCurrentNode, setRunStatus]
  );

  const handleStatusChange = useCallback(
    (change: StepStatusChange) => {
      if (!effectiveRunId) {
        return;
      }
      refreshRunStatus(effectiveRunId);
      if (change.to === 'waiting_user_input') {
        setNotifications((items) => [
          {
            id: `${change.step_id}-${Date.now()}`,
            runId: effectiveRunId,
            change,
          },
          ...items,
        ].slice(0, 5));
      }
    },
    [effectiveRunId, refreshRunStatus]
  );

  useRunStatusSSE(effectiveRunId, handleStatusChange, setWsConnected);

  const handleSendMessage = useCallback(
    (message: string) => {
      const userMessage = {
        id: `msg_${Date.now()}`,
        role: 'user' as const,
        content: message,
        timestamp: new Date(),
      };
      addMessage(userMessage);

      if (isWaitingForInput) {
        setCurrentNode(currentNodeId, 'PROCESSING');
        window.setTimeout(() => {
          setCurrentNode(currentNodeId, 'WAITING_USER_INPUT');
        }, 1500);
      }
    },
    [addMessage, currentNodeId, isWaitingForInput, setCurrentNode]
  );

  return (
    <div className="app">
      <WorkflowStatusBar />
      <main className="main-content">
        <ArtifactPanel runId={effectiveRunId} nodeId={currentNodeId} />
        <div ref={conversationRef} className="conversation-region">
          <ConversationPanel
            onSendMessage={handleSendMessage}
            disabled={!isWaitingForInput}
          />
        </div>
      </main>
      <NotificationToast
        notifications={notifications}
        onDismiss={(id) =>
          setNotifications((items) => items.filter((item) => item.id !== id))
        }
        onOpen={(notification) => {
          setNotifications((items) =>
            items.filter((item) => item.id !== notification.id)
          );
          conversationRef.current?.scrollIntoView({ behavior: 'smooth' });
        }}
      />
    </div>
  );
}
