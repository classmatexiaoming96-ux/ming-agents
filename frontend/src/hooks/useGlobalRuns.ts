import { useCallback, useEffect, useState } from 'react';

export interface RunSummary {
  run_id: string;
  status: string;
  current_node: string;
  created_at: string;
}

interface UseGlobalRunsOptions {
  status?: string;
  limit?: number;
  offset?: number;
  pollMs?: number;
}

export function useGlobalRuns({
  status,
  limit = 20,
  offset = 0,
  pollMs = 30000,
}: UseGlobalRunsOptions = {}) {
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    const params = new URLSearchParams({
      limit: String(limit),
      offset: String(offset),
    });
    if (status) {
      params.set('status', status);
    }

    try {
      const response = await fetch(`/api/runs?${params.toString()}`);
      const data = await response.json();
      if (!response.ok) {
        throw new Error(data.error || response.statusText);
      }
      setRuns(data.runs || []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [limit, offset, status]);

  useEffect(() => {
    refresh();
    const id = window.setInterval(refresh, pollMs);
    return () => window.clearInterval(id);
  }, [pollMs, refresh]);

  return { runs, loading, error, refresh };
}
