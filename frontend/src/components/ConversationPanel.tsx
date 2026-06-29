import React, { useState, useRef, useEffect } from 'react';
import { useWorkflowStore, type ConversationMessage } from '../stores/workflowStore';

interface ConversationPanelProps {
  onSendMessage: (message: string) => void;
  disabled?: boolean;
  isStreaming?: boolean;
  messages?: ConversationMessage[];
  emptyTitle?: string;
  emptyHint?: React.ReactNode;
}

export function ConversationPanel({
  onSendMessage,
  disabled = false,
  isStreaming = false,
  messages: providedMessages,
  emptyTitle = '开始与评审助手对话',
  emptyHint,
}: ConversationPanelProps) {
  const storeMessages = useWorkflowStore((state) => state.messages);
  const messages = providedMessages ?? storeMessages;
  const [input, setInput] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (input.trim() && !disabled && !isStreaming) {
      onSendMessage(input.trim());
      setInput('');
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  return (
    <div className="conversation-panel">
      <div className="message-list">
        {messages.length === 0 && (
          <div className="empty-state">
            <p>{emptyTitle}</p>
            <p className="hint">
              {emptyHint ?? (
                <>
                  当节点进入 WAITING_USER_INPUT 状态时，你可以：<br />
                  - 说"可以了"确认通过<br />
                  - 提出修改意见让节点重跑
                </>
              )}
            </p>
          </div>
        )}

        {messages.map((msg) => (
          <MessageBlock key={msg.id} message={msg} />
        ))}

        {isStreaming && (
          <div className="message assistant">
            <div className="message-content">
              <span className="typing-indicator">...</span>
            </div>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      <form className="input-area" onSubmit={handleSubmit}>
        <textarea
          ref={inputRef}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={
            disabled
              ? '当前节点不在 WAITING_USER_INPUT 状态'
              : '输入消息...'
          }
          disabled={disabled || isStreaming}
          rows={1}
        />
        <button
          type="submit"
          disabled={!input.trim() || disabled || isStreaming}
        >
          {isStreaming ? '...' : '发送'}
        </button>
      </form>
    </div>
  );
}

function MessageBlock({ message }: { message: ConversationMessage }) {
  return (
    <div className={`message ${message.role}`}>
      <div className="message-content">
        {message.content}
      </div>

      {message.toolCalls && message.toolCalls.length > 0 && (
        <div className="tool-calls">
          {message.toolCalls.map((tc) => (
            <div key={tc.id} className="tool-call">
              <span className="tool-name">🔧 {tc.name}</span>
              <pre className="tool-args">{tc.arguments}</pre>
            </div>
          ))}
        </div>
      )}

      {message.toolResults && message.toolResults.length > 0 && (
        <div className="tool-results">
          {message.toolResults.map((tr) => (
            <div
              key={tr.toolCallId}
              className={`tool-result ${tr.isError ? 'error' : ''}`}
            >
              <pre>{tr.result}</pre>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
