"use client";

import { useState } from "react";
import { Agent, api } from "@/lib/api";

export default function NewTaskForm({
  agents,
  onError,
}: {
  agents: Agent[];
  onError: (msg: string | null) => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [agent, setAgent] = useState("");
  const [priority, setPriority] = useState(2);
  const [submitting, setSubmitting] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!prompt.trim()) return;
    setSubmitting(true);
    onError(null);
    try {
      await api.createTask({
        agent: agent || undefined,
        prompt: prompt.trim(),
        priority,
      });
      setPrompt("");
    } catch (err: any) {
      onError(err?.message || "failed to submit task");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form className="newtask" onSubmit={submit}>
      <textarea
        placeholder="Describe the task for the agent…"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
      />
      <select value={agent} onChange={(e) => setAgent(e.target.value)}>
        <option value="">
          {agents.length ? "Default agent" : "(no agents)"}
        </option>
        {agents.map((a) => (
          <option key={a.id} value={a.name}>
            {a.name} ({a.model})
          </option>
        ))}
      </select>
      <select
        value={priority}
        onChange={(e) => setPriority(Number(e.target.value))}
      >
        <option value={1}>High</option>
        <option value={2}>Medium</option>
        <option value={3}>Low</option>
      </select>
      <button type="submit" disabled={submitting || !prompt.trim()}>
        {submitting ? "Submitting…" : "Submit"}
      </button>
    </form>
  );
}
