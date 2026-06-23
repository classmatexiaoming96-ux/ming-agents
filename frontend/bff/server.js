import express from 'express';
import cors from 'cors';
import fetch from 'node-fetch';

const app = express();
const PORT = 3001;

app.use(cors());
app.use(express.json());

// Minimax API configuration
const MINIMAX_API_URL = 'https://api.minimax.chat/v1/text/chatcompletion_v2';
const MINIMAX_MODEL = 'MiniMax-Text-01';

// Get API key from environment
const API_KEY = process.env.MINIMAX_API_KEY || '';

// LLM streaming proxy - forwards to Minimax
app.post('/api/llm/stream', async (req, res) => {
  const { messages, options } = req.body;

  if (!API_KEY) {
    return res.status(500).json({ error: 'MINIMAX_API_KEY not configured' });
  }

  try {
    const upstreamResponse = await fetch(MINIMAX_API_URL, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${API_KEY}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        model: MINIMAX_MODEL,
        messages,
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

    // Stream the response
    upstreamResponse.body.pipe(res);
  } catch (error) {
    console.error('Proxy error:', error);
    res.status(500).json({ error: 'Proxy error' });
  }
});

app.get('/health', (req, res) => {
  res.json({ status: 'ok' });
});

app.listen(PORT, () => {
  console.log(`BFF proxy listening on port ${PORT}`);
  console.log(`Minimax API: ${MINIMAX_API_URL}`);
});
