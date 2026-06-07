"use client";

import { Task, api, PRIORITY_LABEL } from "@/lib/api";

const ACTIVE = new Set(["pending", "claimed", "running"]);

function fmtTime(s?: string): string {
  if (!s) return "—";
  const d = new Date(s);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export default function TaskTable({
  tasks,
  streams,
  onError,
}: {
  tasks: Task[];
  streams: Record<number, string>;
  onError: (msg: string | null) => void;
}) {
  async function cancel(id: number) {
    onError(null);
    try {
      await api.cancelTask(id);
    } catch (err: any) {
      onError(err?.message || "cancel failed");
    }
  }

  if (!tasks.length) {
    return <div className="empty">No tasks yet — submit one above.</div>;
  }

  return (
    <table>
      <thead>
        <tr>
          <th>ID</th>
          <th>Status</th>
          <th>Prio</th>
          <th>Prompt</th>
          <th>Created</th>
          <th>Finished</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {tasks.map((t) => {
          const live = streams[t.id];
          const output = t.result || live;
          return (
            <tr key={t.id}>
              <td className="mono">#{t.id}</td>
              <td>
                <span className={`badge ${t.status}`}>{t.status}</span>
              </td>
              <td>
                <span className={`prio p${t.priority}`}>
                  {PRIORITY_LABEL[t.priority] || t.priority}
                </span>
              </td>
              <td className="prompt">
                {t.prompt}
                {t.error && <div className="err" style={{ marginTop: 6 }}>{t.error}</div>}
                {output && (
                  <details className="output" open={t.status === "running"}>
                    <summary>
                      {t.status === "running" ? "live output" : "output"}
                    </summary>
                    <pre className="stream">{output}</pre>
                  </details>
                )}
              </td>
              <td className="mono">{fmtTime(t.created_at)}</td>
              <td className="mono">{fmtTime(t.completed_at)}</td>
              <td>
                {ACTIVE.has(t.status) && (
                  <button className="ghost" onClick={() => cancel(t.id)}>
                    Cancel
                  </button>
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
