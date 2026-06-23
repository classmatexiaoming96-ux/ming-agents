import { useEffect, useRef, useCallback } from 'react';
import { Agent, type AgentEvent } from '../lib/pi-agent-core';
import { mingAgentTools } from '../lib/pi-agent';
import { buildSystemPrompt } from '../prompts/systemPrompt';
import { useWorkflowStore } from '../stores/workflowStore';

function createSessionId() {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) {
    return crypto.randomUUID();
  }

  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (char) => {
    const random = Math.floor(Math.random() * 16);
    const value = char === 'x' ? random : (random & 0x3) | 0x8;
    return value.toString(16);
  });
}

function getOrCreateSessionId(runId: string) {
  const key = `ming-agents:session:${runId}`;
  const existing = localStorage.getItem(key);
  if (existing) {
    return existing;
  }

  const sessionId = createSessionId();
  localStorage.setItem(key, sessionId);
  return sessionId;
}

interface UsePiAgentConfig {
  runId: string;
  onEvent?: (event: AgentEvent) => void;
}

export function usePiAgent(config: UsePiAgentConfig) {
  const agentRef = useRef<Agent | null>(null);
  const {
    runId,
    currentNodeId,
    currentNodeStatus,
    messages,
    addMessage,
    updateMessage,
    setAgentReady,
    setIsStreaming,
  } = useWorkflowStore();

  // Build system prompt with current context
  const systemPrompt = buildSystemPrompt(
    'ming-agents workflow',
    currentNodeId,
    currentNodeStatus,
    3 // total nodes
  );

  // Initialize agent
  useEffect(() => {
    if (!runId) return;
    const sessionId = getOrCreateSessionId(runId);

    const agent = new Agent({
      initialState: {
        systemPrompt,
        model: {
          provider: 'minimax',
          name: 'MiniMax-Text-01',
        },
        tools: mingAgentTools,
        messages: [],
      },
      convertToLlm: (msgs) =>
        msgs.filter(
          (m) => m.role === 'user' || m.role === 'assistant' || m.role === 'toolResult'
        ),
      streamFn: async (model, context, options) => {
        // Use BFF proxy for LLM calls
        const response = await fetch('/api/llm/stream', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'x-session-id': sessionId,
          },
          body: JSON.stringify({ model, messages: context, options }),
        });

        if (!response.ok) {
          throw new Error(`LLM proxy error: ${response.statusText}`);
        }

        return response.body;
      },
    });

    // Subscribe to events
    const unsubscribe = agent.subscribe((event: AgentEvent) => {
      handleAgentEvent(event);
      config.onEvent?.(event);
    });

    agentRef.current = agent;
    setAgentReady(true);

    return () => {
      unsubscribe();
      agent.abort();
      setAgentReady(false);
    };
  }, [runId, systemPrompt]);

  // Handle agent events
  const handleAgentEvent = useCallback(
    (event: AgentEvent) => {
      switch (event.type) {
        case 'message_start': {
          if (event.agentMessage.role === 'assistant') {
            addMessage({
              id: event.agentMessage.id,
              role: 'assistant',
              content: '',
              timestamp: new Date(),
            });
          }
          break;
        }

        case 'message_update': {
          if (event.assistantMessageEvent.type === 'text_delta') {
            const msg = messages[messages.length - 1];
            if (msg && msg.role === 'assistant') {
              updateMessage(msg.id, {
                content: msg.content + event.assistantMessageEvent.delta,
              });
            }
          }
          break;
        }

        case 'message_end': {
          if (event.agentMessage.role === 'assistant' && event.agentMessage.toolCalls) {
            const msg = messages[messages.length - 1];
            if (msg && msg.role === 'assistant') {
              updateMessage(msg.id, {
                toolCalls: event.agentMessage.toolCalls.map((tc) => ({
                  id: tc.id,
                  name: tc.name,
                  arguments: JSON.stringify(tc.arguments),
                })),
              });
            }
          }
          setIsStreaming(false);
          break;
        }

        case 'tool_execution_start': {
          setIsStreaming(true);
          break;
        }

        case 'tool_execution_end': {
          const msg = messages[messages.length - 1];
          if (msg && msg.role === 'assistant') {
            const resultText =
              typeof event.result === 'string'
                ? event.result
                : JSON.stringify(event.result, null, 2);
            updateMessage(msg.id, {
              toolResults: [
                ...(msg.toolResults || []),
                {
                  toolCallId: event.toolCallId,
                  result: resultText,
                  isError: event.isError,
                },
              ],
            });
          }
          break;
        }

        case 'error': {
          console.error('Agent error:', event.error);
          setIsStreaming(false);
          break;
        }
      }
    },
    [messages, addMessage, updateMessage, setIsStreaming]
  );

  // Send message to agent
  const sendMessage = useCallback(
    async (message: string) => {
      if (!agentRef.current) {
        console.error('Agent not initialized');
        return;
      }

      // Add user message to conversation
      const userMsgId = `user-${Date.now()}`;
      addMessage({
        id: userMsgId,
        role: 'user',
        content: message,
        timestamp: new Date(),
      });

      setIsStreaming(true);

      try {
        await agentRef.current.prompt(message);
      } catch (error) {
        console.error('Error sending message:', error);
        setIsStreaming(false);
      }
    },
    [addMessage, setIsStreaming]
  );

  return {
    agent: agentRef.current,
    sendMessage,
  };
}
