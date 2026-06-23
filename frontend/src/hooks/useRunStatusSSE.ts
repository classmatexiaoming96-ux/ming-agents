import { useEffect, useRef } from 'react';

export interface StepStatusChange {
  type: 'node_status_change';
  node: string;
  step_id: string;
  from: string;
  to: string;
  timestamp: string;
}

export function useRunStatusSSE(
  runId: string | null,
  onStatusChange: (change: StepStatusChange) => void,
  onConnectionChange?: (connected: boolean) => void
) {
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (!runId) {
      onConnectionChange?.(false);
      return;
    }

    const es = new EventSource(`/api/runs/${runId}/events`);
    esRef.current = es;

    es.onopen = () => {
      onConnectionChange?.(true);
    };

    es.onmessage = (event) => {
      try {
        const change = JSON.parse(event.data) as StepStatusChange;
        onStatusChange(change);
      } catch {
        // Ignore malformed events; the stream remains usable for later events.
      }
    };

    es.onerror = () => {
      onConnectionChange?.(false);
      es.close();
    };

    return () => {
      es.close();
      esRef.current = null;
      onConnectionChange?.(false);
    };
  }, [runId, onStatusChange, onConnectionChange]);
}
