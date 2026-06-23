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

export interface AgentMessage {
  id: string;
  role: 'user' | 'assistant' | 'system' | 'toolResult';
  content: string;
  toolCalls?: ToolCall[];
}

export type AgentEvent =
  | { type: 'message_start'; agentMessage: AgentMessage }
  | {
      type: 'message_update';
      assistantMessageEvent: { type: 'text_delta'; delta: string };
    }
  | { type: 'message_end'; agentMessage: AgentMessage }
  | { type: 'tool_execution_start'; toolCallId: string; toolName: string }
  | {
      type: 'tool_execution_end';
      toolCallId: string;
      toolName: string;
      result: unknown;
      isError: boolean;
    }
  | { type: 'error'; error: unknown };

interface AgentConfig {
  initialState: {
    systemPrompt: string;
    model: { provider: string; name: string };
    tools: AgentTool[];
    messages: AgentMessage[];
  };
  convertToLlm?: (messages: AgentMessage[]) => unknown[];
  streamFn: (
    model: unknown,
    context: unknown,
    options?: unknown
  ) => Promise<ReadableStream<Uint8Array> | null>;
}

export class Agent {
  private readonly config: AgentConfig;
  private readonly handlers = new Set<(event: AgentEvent) => void>();
  private readonly messages: AgentMessage[];
  private aborted = false;

  constructor(config: AgentConfig) {
    this.config = config;
    this.messages = [...config.initialState.messages];
    if (config.initialState.systemPrompt) {
      this.messages.push({
        id: 'system',
        role: 'system',
        content: config.initialState.systemPrompt,
      });
    }
  }

  subscribe(handler: (event: AgentEvent) => void): () => void {
    this.handlers.add(handler);
    return () => this.handlers.delete(handler);
  }

  abort() {
    this.aborted = true;
  }

  async prompt(message: string): Promise<void> {
    this.aborted = false;
    this.messages.push({
      id: `user-${Date.now()}`,
      role: 'user',
      content: message,
    });

    const assistantMessage: AgentMessage = {
      id: `assistant-${Date.now()}`,
      role: 'assistant',
      content: '',
    };
    this.emit({ type: 'message_start', agentMessage: assistantMessage });

    try {
      const context = this.config.convertToLlm?.(this.messages) ?? this.messages;
      const stream = await this.config.streamFn(
        this.config.initialState.model,
        context
      );
      if (!stream) {
        throw new Error('LLM stream unavailable');
      }

      for await (const delta of readTextDeltas(stream)) {
        if (this.aborted) {
          break;
        }
        assistantMessage.content += delta;
        this.emit({
          type: 'message_update',
          assistantMessageEvent: { type: 'text_delta', delta },
        });
      }

      this.messages.push(assistantMessage);
      this.emit({ type: 'message_end', agentMessage: assistantMessage });
    } catch (error) {
      this.emit({ type: 'error', error });
      throw error;
    }
  }

  private emit(event: AgentEvent) {
    this.handlers.forEach((handler) => handler(event));
  }
}

async function* readTextDeltas(stream: ReadableStream<Uint8Array>) {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';

    for (const line of lines) {
      const delta = parseStreamLine(line);
      if (delta) {
        yield delta;
      }
    }
  }

  const tail = parseStreamLine(buffer);
  if (tail) {
    yield tail;
  }
}

function parseStreamLine(line: string) {
  const trimmed = line.trim();
  if (!trimmed || trimmed === 'data: [DONE]') {
    return '';
  }
  const raw = trimmed.startsWith('data:') ? trimmed.slice(5).trim() : trimmed;
  try {
    const data = JSON.parse(raw);
    return (
      data.choices?.[0]?.delta?.content ||
      data.choices?.[0]?.message?.content ||
      data.choices?.[0]?.messages?.[0]?.text ||
      data.choices?.[0]?.text ||
      ''
    );
  } catch {
    return raw;
  }
}
