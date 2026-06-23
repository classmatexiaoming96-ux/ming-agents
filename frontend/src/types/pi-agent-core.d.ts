// Stub types for @earendil-works/pi-agent-core (workspace package)
export interface AgentTool {
  name: string;
  description: string;
  parameters: {
    type: 'object';
    properties: Record<string, unknown>;
    required?: string[];
  };
  execute: (
    toolCallId: string,
    params: Record<string, unknown>
  ) => Promise<{
    content: Array<{ type: string; text: string }>;
    isError?: boolean;
    details?: Record<string, unknown>;
  }>;
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: Record<string, unknown>;
}

export type AgentMessage = {
  id: string;
  role: 'user' | 'assistant' | 'system' | 'toolResult';
  content: string;
  toolCalls?: ToolCall[];
};

export type MessageDeltaEvent = {
  type: 'message_update';
  assistantMessageEvent: {
    type: 'text_delta';
    delta: string;
  };
};

export type MessageStartEvent = {
  type: 'message_start';
  agentMessage: AgentMessage;
};

export type MessageEndEvent = {
  type: 'message_end';
  agentMessage: AgentMessage;
};

export type ToolExecutionStartEvent = {
  type: 'tool_execution_start';
  toolCallId: string;
  toolName: string;
};

export type ToolExecutionEndEvent = {
  type: 'tool_execution_end';
  toolCallId: string;
  toolName: string;
  result: unknown;
  isError: boolean;
};

export type ErrorEvent = {
  type: 'error';
  error: unknown;
};

export type AgentEvent =
  | MessageStartEvent
  | MessageDeltaEvent
  | MessageEndEvent
  | ToolExecutionStartEvent
  | ToolExecutionEndEvent
  | ErrorEvent;

export interface Agent {
  prompt(message: string): Promise<void>;
  abort(): void;
  subscribe(handler: (event: AgentEvent) => void): () => void;
}

export interface AgentConfig {
  initialState: {
    systemPrompt: string;
    model: { provider: string; name: string };
    tools: AgentTool[];
    messages: unknown[];
  };
  convertToLlm?: (msgs: unknown[]) => unknown[];
  streamFn?: (
    model: unknown,
    context: unknown,
    options: unknown
  ) => Promise<ReadableStream<Uint8Array> | null>;
}

export declare class Agent {
  constructor(config: AgentConfig);
  prompt(message: string): Promise<void>;
  abort(): void;
  subscribe(handler: (event: AgentEvent) => void): () => void;
}
