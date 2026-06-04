// Typed client for the SHRIMP daemon REST + WebSocket API.

export const DAEMON_URL =
  process.env.NEXT_PUBLIC_DAEMON_URL || "http://localhost:8080";

export type TaskStatus =
  | "pending"
  | "claimed"
  | "running"
  | "completed"
  | "failed"
  | "canceled";

export interface Task {
  id: number;
  agent_id: number;
  status: TaskStatus;
  priority: number; // 1 high, 2 medium, 3 low
  prompt: string;
  result?: string;
  error?: string;
  worker_id?: string;
  attempts: number;
  cancel_requested: boolean;
  created_at: string;
  claimed_at?: string;
  started_at?: string;
  heartbeat_at?: string;
  completed_at?: string;
}

export interface Agent {
  id: number;
  name: string;
  runtime_mode: string;
  max_concurrent_tasks: number;
  model: string;
  thinking_level: string;
}

export interface WsEvent {
  type: string; // task.created | task.running | task.chunk | ...
  task_id: number;
  task?: Task;
  stream?: "stdout" | "stderr";
  chunk?: string;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${DAEMON_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return res.json() as Promise<T>;
}

export const api = {
  listTasks: () => req<Task[]>("/api/tasks"),
  getTask: (id: number) => req<Task>(`/api/tasks/${id}`),
  listAgents: () => req<Agent[]>("/api/agents"),
  createTask: (body: { agent?: string; prompt: string; priority: number }) =>
    req<Task>("/api/tasks", { method: "POST", body: JSON.stringify(body) }),
  cancelTask: (id: number) =>
    req<Task>(`/api/tasks/${id}/cancel`, { method: "POST" }),
};

export function wsURL(): string {
  return DAEMON_URL.replace(/^http/, "ws") + "/ws";
}

export const PRIORITY_LABEL: Record<number, string> = {
  1: "High",
  2: "Medium",
  3: "Low",
};
