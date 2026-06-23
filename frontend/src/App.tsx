import { useEffect, useCallback } from "react";
import { WorkflowStatusBar } from "./components/WorkflowStatusBar";
import { ConversationPanel } from "./components/ConversationPanel";
import { ArtifactPanel } from "./components/ArtifactPanel";
import { useWorkflowStore } from "./stores/workflowStore";

export default function App() {
  const {
    runId,
    currentNodeId,
    currentNodeStatus,
    runStatus,
    connectWebSocket,
    addMessage,
    setCurrentNode,
  } = useWorkflowStore();

  // Check if current node is waiting for user input
  const isWaitingForInput = currentNodeStatus === "WAITING_USER_INPUT";

  // Auto-connect WebSocket when runId is set
  useEffect(() => {
    if (runId) {
      connectWebSocket(runId);
    }
  }, [runId, connectWebSocket]);

  // Handle sending a user message
  const handleSendMessage = useCallback(
    (message: string) => {
      const userMessage = {
        id: `msg_${Date.now()}`,
        role: "user" as const,
        content: message,
        timestamp: new Date(),
      };
      addMessage(userMessage);

      // In a real implementation, this would call the backend API
      // For now, simulate switching out of WAITING_USER_INPUT state
      if (isWaitingForInput) {
        setCurrentNode(currentNodeId, "PROCESSING");
        // Simulate agent processing then returning to waiting
        setTimeout(() => {
          setCurrentNode(currentNodeId, "WAITING_USER_INPUT");
        }, 1500);
      }
    },
    [addMessage, isWaitingForInput, currentNodeId, setCurrentNode]
  );

  return (
    <div className="app">
      <WorkflowStatusBar />
      <main className="main-content">
        <ArtifactPanel runId={runId} nodeId={currentNodeId} />
        <ConversationPanel
          onSendMessage={handleSendMessage}
          disabled={!isWaitingForInput}
        />
      </main>
    </div>
  );
}
