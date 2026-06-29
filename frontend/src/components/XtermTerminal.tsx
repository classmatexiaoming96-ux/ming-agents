import { useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import '@xterm/xterm/css/xterm.css';

interface XtermTerminalProps {
  sessionId: string;
  onConnectionChange?: (connected: boolean) => void;
}

function ptyWebSocketUrl(sessionId: string) {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.host}/ws/pty/${encodeURIComponent(
    sessionId
  )}`;
}

function decodeBase64(data: string) {
  try {
    return atob(data);
  } catch {
    return '';
  }
}

export function XtermTerminal({
  sessionId,
  onConnectionChange,
}: XtermTerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const searchRef = useRef<SearchAddon | null>(null);
  const [search, setSearch] = useState('');

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return undefined;
    }

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        black: '#1e1e1e',
        brightBlack: '#3a3a3a',
        red: '#f14c4c',
        green: '#4ec9b0',
        yellow: '#dcdcaa',
        blue: '#569cd6',
        magenta: '#c586c0',
        cyan: '#9cdcfe',
        white: '#d4d4d4',
        brightWhite: '#ffffff',
      },
      convertEol: true,
      scrollback: 10000,
    });
    const fitAddon = new FitAddon();
    const searchAddon = new SearchAddon();
    const webLinksAddon = new WebLinksAddon();

    term.loadAddon(fitAddon);
    term.loadAddon(searchAddon);
    term.loadAddon(webLinksAddon);
    term.open(container);
    fitAddon.fit();

    termRef.current = term;
    fitRef.current = fitAddon;
    searchRef.current = searchAddon;

    const ws = new WebSocket(ptyWebSocketUrl(sessionId));
    wsRef.current = ws;

    const sendResize = () => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows })
        );
      }
    };

    ws.addEventListener('open', () => {
      onConnectionChange?.(true);
      sendResize();
    });

    ws.addEventListener('message', (event) => {
      if (typeof event.data !== 'string') {
        return;
      }
      try {
        const msg = JSON.parse(event.data) as {
          type?: string;
          data?: string;
          error?: string;
        };
        if ((msg.type === 'snapshot' || msg.type === 'delta') && msg.data) {
          term.write(decodeBase64(msg.data));
          return;
        }
        if (msg.type === 'error' && msg.error) {
          term.write(`\r\n\x1b[31m${msg.error}\x1b[0m\r\n`);
          return;
        }
      } catch {
        term.write(event.data);
      }
    });

    ws.addEventListener('error', () => {
      term.write('\r\n\x1b[31m[PTY connection error]\x1b[0m\r\n');
      onConnectionChange?.(false);
    });

    ws.addEventListener('close', () => {
      term.write('\r\n\x1b[33m[PTY disconnected]\x1b[0m\r\n');
      onConnectionChange?.(false);
    });

    const dataDisposable = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'input', data }));
      }
    });

    const resizeDisposable = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });

    const resizeObserver = new ResizeObserver(() => {
      try {
        fitAddon.fit();
        sendResize();
      } catch {
        // The terminal can be hidden during tab switches; retry on next resize.
      }
    });
    resizeObserver.observe(container);

    return () => {
      resizeObserver.disconnect();
      dataDisposable.dispose();
      resizeDisposable.dispose();
      ws.close();
      term.dispose();
      termRef.current = null;
      wsRef.current = null;
      fitRef.current = null;
      searchRef.current = null;
      onConnectionChange?.(false);
    };
  }, [onConnectionChange, sessionId]);

  return (
    <div className="xterm-shell">
      <div className="xterm-toolbar">
        <input
          type="search"
          value={search}
          onChange={(event) => {
            const value = event.target.value;
            setSearch(value);
            if (value) {
              searchRef.current?.findNext(value);
            }
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && search) {
              event.preventDefault();
              searchRef.current?.findNext(search);
              termRef.current?.focus();
            }
          }}
          placeholder="Search"
          aria-label="Search terminal"
        />
      </div>
      <div ref={containerRef} className="xterm-container" />
    </div>
  );
}
