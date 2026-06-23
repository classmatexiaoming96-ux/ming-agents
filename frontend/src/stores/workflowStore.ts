import { create } from 'zustand';

// Types
export interface NodeInfo {
  id: string;
  name: string;
  status: string;
  artifact?: string;
}

export interface RunStatus {
  run_id: string;
  status: string;
  current_node: string;
  nodes: NodeInfo[];
}

export interface ConversationMessage {
  id: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  timestamp: Date;
  toolCalls?: ToolCall[];
  toolResults?: ToolResult[];
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: string;
}

export interface ToolResult {
  toolCallId: string;
  result: string;
  isError?: boolean;
}

export interface WorkflowUIState {
  // Run state
  runId: string | null;
  runStatus: RunStatus | null;
  currentNodeId: string;
  currentNodeStatus: string;

  // pi-agent
  agentReady: boolean;
  isStreaming: boolean;

  // Conversation
  messages: ConversationMessage[];

  // WebSocket
  wsConnected: boolean;

  // Actions
  setRunId: (runId: string) => void;
  setRunStatus: (status: RunStatus) => void;
  setCurrentNode: (nodeId: string, status: string) => void;
  setAgentReady: (ready: boolean) => void;
  setIsStreaming: (streaming: boolean) => void;
  addMessage: (message: ConversationMessage) => void;
  updateMessage: (id: string, updates: Partial<ConversationMessage>) => void;
  appendToolResult: (toolCallId: string, result: string, isError?: boolean) => void;
  setWsConnected: (connected: boolean) => void;
  reset: () => void;
}

const initialState = {
  runId: null,
  runStatus: null,
  currentNodeId: 'node_1',
  currentNodeStatus: 'WAITING_USER_INPUT',
  agentReady: false,
  isStreaming: false,
  messages: [],
  wsConnected: false,
};

export const useWorkflowStore = create<WorkflowUIState>((set) => ({
  ...initialState,

  setRunId: (runId) => set({ runId }),

  setRunStatus: (status) => set({ runStatus: status }),

  setCurrentNode: (nodeId, status) => set({
    currentNodeId: nodeId,
    currentNodeStatus: status
  }),

  setAgentReady: (ready) => set({ agentReady: ready }),

  setIsStreaming: (streaming) => set({ isStreaming: streaming }),

  addMessage: (message) => set((state) => ({
    messages: [...state.messages, message]
  })),

  updateMessage: (id, updates) => set((state) => ({
    messages: state.messages.map((m) =>
      m.id === id ? { ...m, ...updates } : m
    ),
  })),

  appendToolResult: (toolCallId, result, isError) => set((state) => ({
    messages: state.messages.map((m) => {
      if (m.toolCalls?.some((tc) => tc.id === toolCallId)) {
        return {
          ...m,
          toolResults: [
            ...(m.toolResults || []),
            { toolCallId, result, isError },
          ],
        };
      }
      return m;
    }),
  })),

  setWsConnected: (connected) => set({ wsConnected: connected }),

  reset: () => set(initialState),
}));
