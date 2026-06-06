"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Agent, Task, WsEvent, api, wsURL } from "@/lib/api";
import NewTaskForm from "@/components/NewTaskForm";
import TaskTable from "@/components/TaskTable";

const STATUSES = ["pending", "running", "completed", "failed", "canceled"] as const;
const MAX_STREAM_LINES = 10000;

export default function Page() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [streams, setStreams] = useState<Record<number, string>>({});
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Merge a single task into state (insert or replace), keeping newest first.
  const upsert = useCallback((t: Task) => {
    setTasks((prev) => {
      const idx = prev.findIndex((x) => x.id === t.id);
      if (idx === -1) return [t, ...prev];
      const next = prev.slice();
      next[idx] = t;
      return next;
    });
  }, []);

  // Initial load.
  useEffect(() => {
    (async () => {
      try {
        const [ts, ag] = await Promise.all([api.listTasks(), api.listAgents()]);
        setTasks(ts);
        setAgents(ag);
      } catch (err: any) {
        setError(err?.message || "failed to load");
      }
    })();
  }, []);

  // WebSocket with auto-reconnect.
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    let closed = false;
    let ws: WebSocket;

    function connect() {
      ws = new WebSocket(wsURL());
      ws.onopen = () => setConnected(true);
      ws.onclose = () => {
        setConnected(false);
        if (!closed) reconnectRef.current = setTimeout(connect, 1500);
      };
      ws.onerror = () => ws.close();
      ws.onmessage = (ev) => {
        let msg: WsEvent;
        try {
          msg = JSON.parse(ev.data);
        } catch {
          return;
        }
        if (msg.type === "task.chunk") {
          if (msg.chunk !== undefined) {
            setStreams((prev) => {
              const existing = prev[msg.task_id] || "";
              const newContent = existing + msg.chunk + "\n";
              // Truncate if exceeds MAX_STREAM_LINES
              const lines = newContent.split("\n");
              if (lines.length > MAX_STREAM_LINES) {
                const truncated = lines.slice(-MAX_STREAM_LINES).join("\n");
                return { ...prev, [msg.task_id]: truncated };
              }
              return { ...prev, [msg.task_id]: newContent };
            });
          }
          return;
        }
        if (msg.task) {
          upsert(msg.task);
          // clear live buffer once terminal (result is persisted server-side)
          if (["completed", "failed", "canceled"].includes(msg.task.status)) {
            setStreams((prev) => {
              const next = { ...prev };
              delete next[msg.task_id];
              return next;
            });
          }
        }
      };
    }

    connect();
    return () => {
      closed = true;
      if (reconnectRef.current) clearTimeout(reconnectRef.current);
      ws?.close();
    };
  }, [upsert]);

  const counts = STATUSES.reduce<Record<string, number>>((acc, s) => {
    acc[s] = tasks.filter((t) => t.status === s).length;
    return acc;
  }, {});
  // claimed folds into running for the dashboard summary
  counts.running += tasks.filter((t) => t.status === "claimed").length;

  return (
    <div className="container">
      <header className="app">
        <h1>🦐 SHRIMP Console</h1>
        <span className="tag">MVP Version A</span>
        <span className={`conn ${connected ? "live" : ""}`}>
          <span className="dot" />
          {connected ? "live" : "disconnected"}
        </span>
      </header>

      {error && <div className="err">{error}</div>}

      <div className="stats">
        {STATUSES.map((s) => (
          <div className="stat" key={s}>
            <div className="n">{counts[s] || 0}</div>
            <div className="l">{s}</div>
          </div>
        ))}
      </div>

      <section className="panel">
        <h2>New Task</h2>
        <NewTaskForm agents={agents} onError={setError} />
      </section>

      <section className="panel">
        <h2>Tasks</h2>
        <TaskTable tasks={tasks} streams={streams} onError={setError} />
      </section>
    </div>
  );
}
