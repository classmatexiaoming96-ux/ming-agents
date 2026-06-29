import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { WorkflowStatusBar } from '../components/WorkflowStatusBar';
import { ConversationPanel } from '../components/ConversationPanel';
import { EvidencePanel } from '../components/EvidencePanel';
import { ArtifactPanel } from '../components/ArtifactPanel';
import { PtyPanel, type PTYSessionInfo } from '../components/PtyPanel';
import {
  NotificationToast,
  type ToastNotification,
} from '../components/NotificationToast';
import { useGlobalRuns, type RunSummary } from '../hooks/useGlobalRuns';
import { useRunStatusSSE, type StepStatusChange } from '../hooks/useRunStatusSSE';
import { buildSystemPrompt } from '../prompts/systemPrompt';
import { useWorkflowStore, type ConversationMessage } from '../stores/workflowStore';

type TaskRun = RunSummary & {
  title?: string;
  name?: string;
};

type ChatMode = 'global' | 'task';

function runIdFromPath() {
  const match = window.location.pathname.match(/^\/runs\/([^/]+)/);
  return match?.[1] || null;
}

function taskTitle(run: TaskRun) {
  return run.title || run.name || run.current_node || `Run ${run.run_id.slice(0, 8)}`;
}

function isActiveTask(run: TaskRun) {
  return !['completed', 'failed', 'cancelled'].includes(run.status.toLowerCase());
}

function storageKey(mode: ChatMode, runId: string | null) {
  return mode === 'global'
    ? 'ming-agents:chat:global'
    : `ming-agents:chat:task:${runId || 'none'}`;
}

function makeMessage(
  role: ConversationMessage['role'],
  content: string
): ConversationMessage {
  return {
    id: `${role}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    role,
    content,
    timestamp: new Date(),
  };
}

function loadMessages(key: string): ConversationMessage[] {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw) as Array<Omit<ConversationMessage, 'timestamp'> & {
      timestamp: string;
    }>;
    return parsed.map((message) => ({
      ...message,
      timestamp: new Date(message.timestamp),
    }));
  } catch {
    return [];
  }
}

function saveMessages(key: string, messages: ConversationMessage[]) {
  localStorage.setItem(key, JSON.stringify(messages));
}

async function streamAssistantReply({
  messages,
  sessionId,
}: {
  messages: ConversationMessage[];
  sessionId: string;
}) {
  const response = await fetch('/api/llm/stream', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'x-session-id': sessionId,
    },
    body: JSON.stringify({
      messages: messages
        .filter((message) => message.role === 'user' || message.role === 'assistant' || message.role === 'system')
        .map((message) => ({
          role: message.role,
          content: message.content,
        })),
    }),
  });

  if (!response.ok || !response.body) {
    throw new Error(`LLM stream failed: ${response.statusText}`);
  }

  return response.body;
}

async function* readStreamDeltas(stream: ReadableStream<Uint8Array>) {
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

export function WorkflowTaskPage() {
  const {
    runId,
    runStatus,
    currentNodeId,
    currentNodeStatus,
    addMessage,
    setCurrentNode,
    setRunId,
    setRunStatus,
    setWsConnected,
  } = useWorkflowStore();
  const { runs, loading, error, refresh } = useGlobalRuns({ limit: 50 });
  const [selectedRunId, setSelectedRunId] = useState<string | null>(
    runIdFromPath()
  );
  const [notifications, setNotifications] = useState<ToastNotification[]>([]);
  const [ptySessions, setPtySessions] = useState<PTYSessionInfo[]>([]);
  const [phaseStatus, setPhaseStatus] = useState<{
    phase: string;
    gate_status: string;
    next_action: string;
    next_action_prompt?: string;
    missing_items?: string[];
    failure_class?: string;
    updated_at?: string;
  } | null>(null);
  const conversationRef = useRef<HTMLDivElement>(null);

  const allTasks = runs as TaskRun[];
  const tasks = useMemo(
    () =>
      allTasks.filter(
        (task) => isActiveTask(task) || task.run_id === selectedRunId
      ),
    [allTasks, selectedRunId]
  );
  const selectedTask = useMemo(
    () => tasks.find((task) => task.run_id === selectedRunId) || null,
    [selectedRunId, tasks]
  );
  const effectiveRunId = selectedRunId || runId;
  const currentNode = runStatus?.nodes?.find((node) => node.id === currentNodeId);
  const isWaitingForInput =
    currentNodeStatus === 'WAITING_USER_INPUT' ||
    currentNodeStatus === 'waiting_user_input';

  useEffect(() => {
    if (!selectedRunId && tasks.length > 0) {
      setSelectedRunId(tasks[0].run_id);
    }
  }, [selectedRunId, tasks]);

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

  const refreshPhaseStatus = useCallback(async (id: string) => {
    try {
      const response = await fetch(`/api/runs/${id}/phase-status`);
      if (!response.ok) {
        setPhaseStatus(null);
        return;
      }
      setPhaseStatus(await response.json());
    } catch {
      setPhaseStatus(null);
    }
  }, []);

  const refreshPtySessions = useCallback(async (id: string) => {
    try {
      const response = await fetch(`/api/runs/${id}/pty-sessions`);
      if (!response.ok) {
        setPtySessions([]);
        return;
      }
      const body = (await response.json()) as { sessions?: PTYSessionInfo[] };
      setPtySessions(body.sessions ?? []);
    } catch {
      setPtySessions([]);
    }
  }, []);

  useEffect(() => {
    if (effectiveRunId) {
      void refreshRunStatus(effectiveRunId);
      void refreshPhaseStatus(effectiveRunId);
      void refreshPtySessions(effectiveRunId);
    } else {
      setPhaseStatus(null);
      setPtySessions([]);
    }
  }, [effectiveRunId, refreshPhaseStatus, refreshPtySessions, refreshRunStatus]);

  const selectTask = useCallback(
    (task: TaskRun) => {
      setSelectedRunId(task.run_id);
      setRunId(task.run_id);
      void refreshRunStatus(task.run_id);
      void refreshPhaseStatus(task.run_id);
      void refreshPtySessions(task.run_id);
    },
    [refreshPhaseStatus, refreshPtySessions, refreshRunStatus, setRunId]
  );

  const handleStatusChange = useCallback(
    (change: StepStatusChange) => {
      if (!effectiveRunId) {
        return;
      }
      void refreshRunStatus(effectiveRunId);
      void refreshPhaseStatus(effectiveRunId);
      void refreshPtySessions(effectiveRunId);
      void refresh();
      if (change.to === 'waiting_user_input' || change.to === 'WAITING_USER_INPUT') {
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
    [effectiveRunId, refresh, refreshPhaseStatus, refreshPtySessions, refreshRunStatus]
  );

  useRunStatusSSE(effectiveRunId, handleStatusChange, setWsConnected);

  const handleSendMessage = useCallback(
    (message: string) => {
      addMessage(makeMessage('user', message));

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
    <div className="workflow-page">
      <section className="workflow-shell">
        <TaskTabBar
          tasks={tasks}
          loading={loading}
          error={error}
          activeRunId={effectiveRunId}
          onSelectTask={selectTask}
          onRefresh={refresh}
        />
        <WorkflowStatusBar />
        <EvidencePanel
          key={`${effectiveRunId || 'none'}:${phaseStatus?.phase || ''}:${phaseStatus?.gate_status || ''}:${phaseStatus?.next_action || ''}`}
          runId={effectiveRunId}
        />
        <main className="workflow-main">
          <section className="task-detail">
            <div className="task-detail-header">
              <div>
                <h1>{selectedTask ? taskTitle(selectedTask) : '未选择任务'}</h1>
                <p>
                  当前节点: {currentNodeId}
                  {currentNode?.name ? ` (${currentNode.name})` : ''}
                </p>
              </div>
              <div className="task-status-card">
                <span>节点状态</span>
                <strong>{currentNodeStatus}</strong>
              </div>
            </div>
            <PtyPanel
              sessions={ptySessions}
              fallbackNodes={runStatus?.nodes ?? []}
              gateStatus={phaseStatus?.gate_status}
            />
            <div className="task-workspace">
              <ArtifactPanel runId={effectiveRunId} nodeId={currentNodeId} />
              <div ref={conversationRef} className="conversation-region task-conversation">
                <ConversationPanel
                  onSendMessage={handleSendMessage}
                  disabled={!isWaitingForInput}
                />
              </div>
            </div>
          </section>
        </main>
      </section>

      <ChatbotSidebar
        runId={effectiveRunId}
        nodeId={currentNodeId}
        nodeStatus={currentNodeStatus}
      />

      <NotificationToast
        notifications={notifications}
        onDismiss={(id) =>
          setNotifications((items) => items.filter((item) => item.id !== id))
        }
        onOpen={(notification) => {
          setNotifications((items) =>
            items.filter((item) => item.id !== notification.id)
          );
          if (notification.runId !== effectiveRunId) {
            setSelectedRunId(notification.runId);
          }
          conversationRef.current?.scrollIntoView({ behavior: 'smooth' });
        }}
      />
    </div>
  );
}

function TaskTabBar({
  tasks,
  loading,
  error,
  activeRunId,
  onSelectTask,
  onRefresh,
}: {
  tasks: TaskRun[];
  loading: boolean;
  error: string | null;
  activeRunId: string | null;
  onSelectTask: (task: TaskRun) => void;
  onRefresh: () => void;
}) {
  return (
    <nav className="task-tab-bar" aria-label="任务列表">
      <div className="task-tabs">
        {tasks.map((task) => (
          <button
            key={task.run_id}
            type="button"
            className={`task-tab ${task.run_id === activeRunId ? 'active' : ''}`}
            onClick={() => onSelectTask(task)}
            title={taskTitle(task)}
          >
            <span className="task-tab-title">{taskTitle(task)}</span>
            <span className="task-tab-status">{task.status}</span>
          </button>
        ))}
        {!loading && tasks.length === 0 && (
          <span className="task-tab-empty">暂无运行中任务</span>
        )}
      </div>
      <div className="task-tab-actions">
        {loading && <span className="task-tab-meta">加载中...</span>}
        {error && <span className="task-tab-error">{error}</span>}
        <button type="button" onClick={onRefresh}>
          刷新
        </button>
      </div>
    </nav>
  );
}

function ChatbotSidebar({
  runId,
  nodeId,
  nodeStatus,
}: {
  runId: string | null;
  nodeId: string;
  nodeStatus: string;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [mode, setMode] = useState<ChatMode>('global');
  const [messages, setMessages] = useState<ConversationMessage[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const key = storageKey(mode, runId);

  useEffect(() => {
    setMessages(loadMessages(key));
  }, [key]);

  useEffect(() => {
    saveMessages(key, messages);
  }, [key, messages]);

  const sendMessage = useCallback(
    async (content: string) => {
      const systemMessage =
        mode === 'task'
          ? makeMessage(
              'system',
              buildSystemPrompt('ming-agents workflow', nodeId, nodeStatus, 3)
            )
          : null;
      const userMessage = makeMessage('user', content);
      const assistantMessage = makeMessage('assistant', '');
      const nextMessages = [
        ...(systemMessage ? [systemMessage] : []),
        ...messages,
        userMessage,
        assistantMessage,
      ];

      setMessages((items) => [...items, userMessage, assistantMessage]);
      setIsStreaming(true);

      try {
        const stream = await streamAssistantReply({
          messages: nextMessages,
          sessionId:
            mode === 'global'
              ? 'workflow-global-chat'
              : `workflow-task-chat:${runId || 'none'}:${nodeId}`,
        });

        let contentSoFar = '';
        for await (const delta of readStreamDeltas(stream)) {
          contentSoFar += delta;
          setMessages((items) =>
            items.map((message) =>
              message.id === assistantMessage.id
                ? { ...message, content: contentSoFar }
                : message
            )
          );
        }
      } catch (error) {
        setMessages((items) =>
          items.map((message) =>
            message.id === assistantMessage.id
              ? {
                  ...message,
                  content: `请求失败：${error instanceof Error ? error.message : String(error)}`,
                }
              : message
          )
        );
      } finally {
        setIsStreaming(false);
      }
    },
    [messages, mode, nodeId, nodeStatus, runId]
  );

  return (
    <aside className={`chatbot-sidebar ${collapsed ? 'collapsed' : ''}`}>
      <header className="chatbot-header">
        {!collapsed && <h2>Chatbot</h2>}
        <button type="button" onClick={() => setCollapsed((value) => !value)}>
          {collapsed ? '展开' : '收起'}
        </button>
      </header>
      {!collapsed && (
        <>
          <div className="chatbot-mode-tabs" role="tablist" aria-label="对话模式">
            <button
              type="button"
              className={mode === 'global' ? 'active' : ''}
              onClick={() => setMode('global')}
            >
              全局对话
            </button>
            <button
              type="button"
              className={mode === 'task' ? 'active' : ''}
              onClick={() => setMode('task')}
            >
              任务对话
            </button>
          </div>
          <ConversationPanel
            messages={messages}
            onSendMessage={sendMessage}
            disabled={false}
            isStreaming={isStreaming}
            emptyTitle={mode === 'global' ? '开始全局对话' : '开始任务对话'}
            emptyHint={
              mode === 'global'
                ? '可询问跨任务问题、总体进展或工作流使用方式。'
                : '当前对话会带上所选任务与节点上下文。'
            }
          />
        </>
      )}
    </aside>
  );
}
