import express from 'express';
import cors from 'cors';
import fetch from 'node-fetch';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const PORT = 3001;
const SESSION_DIR = '/tmp/ming-agents/sessions';
const SESSION_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;
const SESSION_CLEANUP_INTERVAL_MS = 60 * 60 * 1000;

// Minimax API configuration
const MINIMAX_API_URL = 'https://api.minimax.chat/v1/text/chatcompletion_v2';
const MINIMAX_MODEL = 'MiniMax-Text-01';

// Get API key from environment
const API_KEY = process.env.MINIMAX_API_KEY || '';

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function assertValidSessionId(sessionId) {
  if (!UUID_RE.test(sessionId)) {
    throw new Error('Invalid sessionId');
  }
}

function sessionPath(sessionDir, sessionId) {
  assertValidSessionId(sessionId);
  return path.join(sessionDir, sessionId);
}

function messagesPath(sessionDir, sessionId) {
  return path.join(sessionPath(sessionDir, sessionId), 'messages.jsonl');
}

export function loadSessionMessages(sessionDir, sessionId) {
  const file = messagesPath(sessionDir, sessionId);
  if (!fs.existsSync(file)) {
    return [];
  }

  const content = fs.readFileSync(file, 'utf-8').trim();
  if (!content) {
    return [];
  }

  return content
    .split('\n')
    .filter(Boolean)
    .map((line) => JSON.parse(line));
}

export function appendSessionMessage(sessionDir, sessionId, message) {
  const dir = sessionPath(sessionDir, sessionId);
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, 'messages.jsonl');
  const entry = {
    id: message.id,
    role: message.role,
    content: message.content || '',
    toolCalls: message.toolCalls || [],
    created_at: new Date().toISOString(),
  };

  fs.appendFileSync(file, `${JSON.stringify(entry)}\n`);
  const now = new Date();
  fs.utimesSync(dir, now, now);
}

function estimateTokens(messages) {
  return messages.reduce((sum, m) => sum + Math.ceil((m.content || '').length / 4), 0);
}

async function runCompaction(sessionDir, sessionId, apiKey, fetchImpl) {
  const messages = loadSessionMessages(sessionDir, sessionId);
  if (estimateTokens(messages) < 20000) return;

  const historyText = messages
    .filter(m => m.role !== 'compactionSummary')
    .map(m => `${m.role}: ${m.content}`)
    .join('\n');

  const summaryPrompt = `You are a context summarization assistant. Create a structured summary of this conversation for a developer. Use this EXACT format:\n\n## Goal\n[What is the user trying to accomplish?]\n\n## Progress\n### Done\n- [x] [Completed tasks]\n\n### In Progress\n- [ ] [Current work]\n\n## Key Decisions\n- [Any decisions made]\n\n## Next Steps\n1. [What should happen next]\n\n## Critical Context\n- [Important file paths, error messages, etc.]\n\nConversation to summarize:\n${historyText}`;

  try {
    const resp = await fetchImpl(MINIMAX_API_URL, {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${apiKey}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: MINIMAX_MODEL, messages: [{ role: 'user', content: summaryPrompt }], stream: false }),
    });
    const data = await resp.json();
    const summary = data.choices?.[0]?.message?.content || '';

    // Replace all messages with a single compactionSummary entry
    const file = messagesPath(sessionDir, sessionId);
    const compactionEntry = {
      id: `compact-${Date.now()}`,
      role: 'compactionSummary',
      content: summary,
      created_at: new Date().toISOString(),
    };
    fs.writeFileSync(file, JSON.stringify(compactionEntry) + '\n');
    console.log(`Compaction completed for session ${sessionId}, summary length: ${summary.length}`);
  } catch (err) {
    console.error('Compaction failed:', err.message);
  }
}

export function cleanupOldSessions(sessionDir = SESSION_DIR) {
  const now = Date.now();
  if (!fs.existsSync(sessionDir)) {
    return;
  }

  for (const sessionId of fs.readdirSync(sessionDir)) {
    const currentSessionPath = path.join(sessionDir, sessionId);
    const stat = fs.statSync(currentSessionPath);
    if (stat.isDirectory() && now - stat.mtimeMs > SESSION_MAX_AGE_MS) {
      fs.rmSync(currentSessionPath, { recursive: true, force: true });
    }
  }
}

function getSessionId(req) {
  return req.header('x-session-id') || req.body?.sessionId || '';
}

function latestUserMessage(messages) {
  return [...messages].reverse().find((message) => message.role === 'user');
}

function buildUpstreamMessages({ requestMessages, sessionDir, sessionId }) {
  if (!sessionId) {
    return requestMessages;
  }

  const history = loadSessionMessages(sessionDir, sessionId);
  const currentUserMessage = latestUserMessage(requestMessages);
  if (!currentUserMessage) {
    return history;
  }

  return [...history, currentUserMessage];
}

function parseStreamLine(line) {
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
    return '';
  }
}

function collectAssistantDelta(chunk, state) {
  state.buffer += chunk.toString('utf-8');
  const lines = state.buffer.split('\n');
  state.buffer = lines.pop() || '';

  for (const line of lines) {
    state.content += parseStreamLine(line);
  }
}

function finishAssistantDelta(state) {
  state.content += parseStreamLine(state.buffer);
  state.buffer = '';
}

export function createApp({
  apiKey = API_KEY,
  fetchImpl = fetch,
  sessionDir = SESSION_DIR,
} = {}) {
  const app = express();

  app.use(cors());
  app.use(express.json());

  // LLM streaming proxy - forwards to Minimax
  app.post('/api/llm/stream', async (req, res) => {
    const { messages = [], options } = req.body;
    const sessionId = getSessionId(req);

    if (!apiKey) {
      return res.status(500).json({ error: 'MINIMAX_API_KEY not configured' });
    }

    let upstreamMessages;
    try {
      upstreamMessages = buildUpstreamMessages({
        requestMessages: messages,
        sessionDir,
        sessionId,
      });
    } catch (error) {
      return res.status(400).json({ error: error.message });
    }

    try {
      const upstreamResponse = await fetchImpl(MINIMAX_API_URL, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${apiKey}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          model: MINIMAX_MODEL,
          messages: upstreamMessages,
          stream: true,
          ...options,
        }),
      });

      if (!upstreamResponse.ok) {
        const error = await upstreamResponse.text();
        return res.status(upstreamResponse.status).json({ error });
      }

      // Set headers for streaming
      res.setHeader('Content-Type', 'text/event-stream');
      res.setHeader('Cache-Control', 'no-cache');
      res.setHeader('Connection', 'keep-alive');

      const currentUserMessage = sessionId ? latestUserMessage(messages) : null;
      const assistantState = { buffer: '', content: '' };

      upstreamResponse.body.on('data', (chunk) => {
        collectAssistantDelta(chunk, assistantState);
        res.write(chunk);
      });
      upstreamResponse.body.on('end', () => {
        finishAssistantDelta(assistantState);
        if (sessionId && currentUserMessage) {
          appendSessionMessage(sessionDir, sessionId, currentUserMessage);
          appendSessionMessage(sessionDir, sessionId, {
            id: `assistant-${Date.now()}`,
            role: 'assistant',
            content: assistantState.content,
            toolCalls: [],
          });
          // Trigger compaction after appending (fire-and-forget)
          runCompaction(sessionDir, sessionId, apiKey, fetchImpl);
        }
        res.end();
      });
      upstreamResponse.body.on('error', (error) => {
        console.error('Upstream stream error:', error);
        res.end();
      });
    } catch (error) {
      console.error('Proxy error:', error);
      res.status(500).json({ error: 'Proxy error' });
    }
  });

  app.get('/health', (req, res) => {
    res.json({ status: 'ok' });
  });

  return app;
}

const isMainModule = process.argv[1] === fileURLToPath(import.meta.url);

if (isMainModule) {
  cleanupOldSessions();
  const cleanupTimer = setInterval(cleanupOldSessions, SESSION_CLEANUP_INTERVAL_MS);
  cleanupTimer.unref?.();

  const app = createApp();
  app.listen(PORT, () => {
    console.log(`BFF proxy listening on port ${PORT}`);
    console.log(`Minimax API: ${MINIMAX_API_URL}`);
  });
}
