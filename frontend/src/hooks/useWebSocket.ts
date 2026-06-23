import { useEffect, useRef, useCallback } from 'react';
import { useWorkflowStore } from '../stores/workflowStore';

interface WebSocketMessage {
  type: 'node_status_changed' | 'run_status_changed' | 'error';
  run_id: string;
  node_id?: string;
  status?: string;
  message?: string;
}

export function useWebSocket(runId: string | null) {
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<number | null>(null);
  const { setWsConnected, setCurrentNode, setRunStatus } = useWorkflowStore();

  const connect = useCallback(() => {
    if (!runId) return;

    // Build WebSocket URL - use ws:// for dev, wss:// for prod
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws/runs/${runId}`;

    try {
      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;

      ws.onopen = () => {
        console.log('WebSocket connected');
        setWsConnected(true);
      };

      ws.onmessage = (event) => {
        try {
          const data: WebSocketMessage = JSON.parse(event.data);
          handleMessage(data);
        } catch (error) {
          console.error('Failed to parse WebSocket message:', error);
        }
      };

      ws.onerror = (error) => {
        console.error('WebSocket error:', error);
      };

      ws.onclose = () => {
        console.log('WebSocket disconnected');
        setWsConnected(false);
        wsRef.current = null;

        // Reconnect after 3 seconds
        reconnectTimeoutRef.current = window.setTimeout(() => {
          if (runId) {
            connect();
          }
        }, 3000);
      };
    } catch (error) {
      console.error('Failed to create WebSocket:', error);
    }
  }, [runId, setWsConnected]);

  const handleMessage = useCallback(
    (data: WebSocketMessage) => {
      switch (data.type) {
        case 'node_status_changed': {
          if (data.node_id && data.status) {
            setCurrentNode(data.node_id, data.status);
          }
          break;
        }

        case 'run_status_changed': {
          // Refresh full status when run status changes
          fetch(`/api/runs/${data.run_id}/status`)
            .then((res) => res.json())
            .then((status) => {
              setRunStatus(status);
            })
            .catch((err) => {
              console.error('Failed to fetch run status:', err);
            });
          break;
        }

        case 'error': {
          console.error('WebSocket error event:', data.message);
          break;
        }
      }
    },
    [setCurrentNode, setRunStatus]
  );

  // Connect on mount
  useEffect(() => {
    if (runId) {
      connect();
    }

    return () => {
      if (wsRef.current) {
        wsRef.current.close();
      }
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
    };
  }, [runId, connect]);

  const disconnect = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
  }, []);

  return {
    disconnect,
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
  };
}
