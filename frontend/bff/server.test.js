import assert from 'node:assert/strict';
import { mkdtemp, rm, stat, mkdir, utimes } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Readable } from 'node:stream';
import test from 'node:test';

import {
  appendSessionMessage,
  cleanupOldSessions,
  createApp,
  loadSessionMessages,
} from './server.js';

const SESSION_ID = '123e4567-e89b-12d3-a456-426614174000';

test('appends and loads session messages as JSONL', async () => {
  const sessionDir = await mkdtemp(path.join(tmpdir(), 'ming-agent-session-'));

  try {
    appendSessionMessage(sessionDir, SESSION_ID, {
      id: 'msg-user',
      role: 'user',
      content: 'hello',
      toolCalls: [],
    });

    const messages = loadSessionMessages(sessionDir, SESSION_ID);

    assert.equal(messages.length, 1);
    assert.equal(messages[0].id, 'msg-user');
    assert.equal(messages[0].role, 'user');
    assert.equal(messages[0].content, 'hello');
    assert.match(messages[0].created_at, /^\d{4}-\d{2}-\d{2}T/);
  } finally {
    await rm(sessionDir, { recursive: true, force: true });
  }
});

test('append refreshes session directory mtime for cleanup retention', async () => {
  const sessionDir = await mkdtemp(path.join(tmpdir(), 'ming-agent-session-'));
  const currentSessionPath = path.join(sessionDir, SESSION_ID);
  const oldDate = new Date(Date.now() - 8 * 24 * 60 * 60 * 1000);

  try {
    appendSessionMessage(sessionDir, SESSION_ID, {
      id: 'first-message',
      role: 'user',
      content: 'old message',
    });
    await utimes(currentSessionPath, oldDate, oldDate);

    appendSessionMessage(sessionDir, SESSION_ID, {
      id: 'second-message',
      role: 'assistant',
      content: 'new message',
    });

    const refreshed = await stat(currentSessionPath);
    assert.ok(refreshed.mtimeMs > oldDate.getTime());
  } finally {
    await rm(sessionDir, { recursive: true, force: true });
  }
});

test('stream endpoint sends persisted history plus latest user and persists assistant response', async () => {
  const sessionDir = await mkdtemp(path.join(tmpdir(), 'ming-agent-session-'));
  const upstreamBodies = [];

  appendSessionMessage(sessionDir, SESSION_ID, {
    id: 'old-user',
    role: 'user',
    content: 'previous question',
  });
  appendSessionMessage(sessionDir, SESSION_ID, {
    id: 'old-assistant',
    role: 'assistant',
    content: 'previous answer',
  });

  const app = createApp({
    apiKey: 'test-key',
    sessionDir,
    fetchImpl: async (_url, init) => {
      upstreamBodies.push(JSON.parse(init.body));
      return {
        ok: true,
        body: Readable.from([
          'data: {"choices":[{"delta":{"content":"new "}}]}\n\n',
          'data: {"choices":[{"delta":{"content":"answer"}}]}\n\n',
          'data: [DONE]\n\n',
        ]),
      };
    },
  });
  const server = app.listen(0);

  try {
    const { port } = server.address();
    const response = await fetch(`http://127.0.0.1:${port}/api/llm/stream`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'x-session-id': SESSION_ID,
      },
      body: JSON.stringify({
        messages: [
          { id: 'stale-user', role: 'user', content: 'previous question' },
          { id: 'stale-assistant', role: 'assistant', content: 'previous answer' },
          { id: 'new-user', role: 'user', content: 'new question' },
        ],
      }),
    });
    const streamed = await response.text();

    assert.equal(response.status, 200);
    assert.match(streamed, /"new "/);
    assert.match(streamed, /"answer"/);
    assert.deepEqual(
      upstreamBodies[0].messages.map((message) => message.content),
      ['previous question', 'previous answer', 'new question']
    );

    const stored = loadSessionMessages(sessionDir, SESSION_ID);
    assert.deepEqual(
      stored.map((message) => [message.role, message.content]),
      [
        ['user', 'previous question'],
        ['assistant', 'previous answer'],
        ['user', 'new question'],
        ['assistant', 'new answer'],
      ]
    );
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(sessionDir, { recursive: true, force: true });
  }
});

test('stream endpoint compacts persisted history before appending the latest turn when over threshold', async () => {
  const sessionDir = await mkdtemp(path.join(tmpdir(), 'ming-agent-session-'));

  appendSessionMessage(sessionDir, SESSION_ID, {
    id: 'old-user',
    role: 'user',
    content: 'previous question with enough text to cross the tiny test threshold',
  });
  appendSessionMessage(sessionDir, SESSION_ID, {
    id: 'old-assistant',
    role: 'assistant',
    content: 'previous answer with enough text to cross the tiny test threshold',
  });

  const app = createApp({
    apiKey: 'test-key',
    sessionDir,
    compactionSettings: {
      enabled: true,
      reserveTokens: 0,
      keepRecentTokens: 1,
    },
    compactionModel: {
      provider: 'minimax',
      id: 'MiniMax-Text-01',
      maxTokens: 4096,
      reasoning: false,
    },
    compactImpl: async () => ({
      ok: true,
      value: {
        summary: '## Goal\nSummarized prior work',
        firstKeptEntryId: 'old-assistant',
        tokensBefore: 42,
        details: { readFiles: [], modifiedFiles: [] },
      },
    }),
    fetchImpl: async () => ({
      ok: true,
      body: Readable.from([
        'data: {"choices":[{"delta":{"content":"new answer"}}]}\n\n',
        'data: [DONE]\n\n',
      ]),
    }),
  });
  const server = app.listen(0);

  try {
    const { port } = server.address();
    const response = await fetch(`http://127.0.0.1:${port}/api/llm/stream`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'x-session-id': SESSION_ID,
      },
      body: JSON.stringify({
        messages: [
          { id: 'new-user', role: 'user', content: 'new question' },
        ],
      }),
    });

    await response.text();

    const stored = loadSessionMessages(sessionDir, SESSION_ID);
    assert.deepEqual(
      stored.map((message) => [message.role, message.content]),
      [
        ['compactionSummary', '## Goal\nSummarized prior work'],
        ['assistant', 'previous answer with enough text to cross the tiny test threshold'],
        ['user', 'new question'],
        ['assistant', 'new answer'],
      ]
    );
    assert.match(stored[0].id, /^compaction-/);
  } finally {
    await new Promise((resolve) => server.close(resolve));
    await rm(sessionDir, { recursive: true, force: true });
  }
});

test('rejects session ids that could escape the session directory', async () => {
  assert.throws(() => loadSessionMessages('/tmp/ming-agents/sessions', '../bad'), /Invalid sessionId/);
});

test('cleanup removes session directories older than seven days', async () => {
  const sessionDir = await mkdtemp(path.join(tmpdir(), 'ming-agent-session-'));
  const oldPath = path.join(sessionDir, SESSION_ID);
  const recentPath = path.join(sessionDir, '123e4567-e89b-12d3-a456-426614174001');
  const oldDate = new Date(Date.now() - 8 * 24 * 60 * 60 * 1000);

  try {
    await mkdir(oldPath, { recursive: true });
    await mkdir(recentPath, { recursive: true });
    await utimes(oldPath, oldDate, oldDate);

    cleanupOldSessions(sessionDir);

    await assert.rejects(() => stat(oldPath), /ENOENT/);
    await stat(recentPath);
  } finally {
    await rm(sessionDir, { recursive: true, force: true });
  }
});
