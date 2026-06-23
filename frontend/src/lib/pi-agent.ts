import type { AgentTool } from './pi-agent-core';
import { useWorkflowStore } from '../stores/workflowStore';

// API base URL
const API_BASE = '/api';

// Helper to create fetch with timeout
async function fetchWithTimeout(
  url: string,
  options: RequestInit & { timeout?: number } = {}
): Promise<Response> {
  const { timeout = 10000, ...fetchOptions } = options;
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), timeout);

  try {
    const response = await fetch(url, {
      ...fetchOptions,
      signal: controller.signal,
    });
    return response;
  } finally {
    clearTimeout(timeoutId);
  }
}

// Tool 1: get_run_status
export const getRunStatusTool: AgentTool = {
  name: 'get_run_status',
  description: '获取当前工作流运行的完整状态，包括所有节点的状态、当前节点ID、运行进度等信息。在需要了解整体进度或当前所处节点时使用。',
  parameters: {
    type: 'object',
    properties: {
      runId: {
        type: 'string',
        description: '工作流运行ID，从上下文获取',
      },
    },
    required: ['runId'],
  },
  execute: async (_toolCallId, params) => {
    const { runId } = params as { runId: string };
    const store = useWorkflowStore.getState();

    try {
      const response = await fetchWithTimeout(`${API_BASE}/runs/${runId}/status`);
      const data = await response.json();

      if (!response.ok) {
        return {
          content: [{ type: 'text', text: `获取状态失败：${data.error}` }],
          isError: true,
        };
      }

      // Update store with new status
      store.setRunStatus(data);
      if (data.current_node) {
        const currentNode = data.nodes.find(
          (n: { id: string }) => n.id === data.current_node
        );
        if (currentNode) {
          store.setCurrentNode(data.current_node, currentNode.status);
        }
      }

      return {
        content: [
          {
            type: 'text',
            text: JSON.stringify(data, null, 2),
          },
        ],
        details: {
          currentNode: data.current_node,
          totalNodes: data.nodes?.length || 0,
          runStatus: data.status,
        },
      };
    } catch (error) {
      return {
        content: [{ type: 'text', text: `获取状态失败：${error}` }],
        isError: true,
      };
    }
  },
};

// Tool 2: read_node_artifact
export const readNodeArtifactTool: AgentTool = {
  name: 'read_node_artifact',
  description:
    '读取指定节点的产出文件内容。节点产出是该节点执行完成后生成的工作成果，可能是 Markdown 文档、代码文件或其他格式。',
  parameters: {
    type: 'object',
    properties: {
      runId: {
        type: 'string',
        description: '工作流运行ID',
      },
      nodeId: {
        type: 'string',
        description: "节点ID，如 'node_1', 'node_2', 'node_3'",
      },
    },
    required: ['runId', 'nodeId'],
  },
  execute: async (_toolCallId, params) => {
    const { runId, nodeId } = params as { runId: string; nodeId: string };

    try {
      const response = await fetchWithTimeout(
        `${API_BASE}/runs/${runId}/nodes/${nodeId}/artifact`
      );
      const data = await response.json();

      if (!response.ok) {
        return {
          content: [{ type: 'text', text: `无法读取节点产出：${data.error}` }],
          isError: true,
        };
      }

      return {
        content: [
          {
            type: 'text',
            text: `=== 节点 ${nodeId} 产出 ===\n\n${data.content}`,
          },
        ],
        details: {
          nodeId,
          artifactType: data.artifact_type,
          size: data.content?.length || 0,
        },
      };
    } catch (error) {
      return {
        content: [{ type: 'text', text: `读取产出失败：${error}` }],
        isError: true,
      };
    }
  },
};

// Tool 3: submit_feedback
export const submitFeedbackTool: AgentTool = {
  name: 'submit_feedback',
  description:
    '将用户对节点产出的反馈提交到服务器。反馈会被记录并用于节点重跑时的修正依据。',
  parameters: {
    type: 'object',
    properties: {
      runId: {
        type: 'string',
        description: '工作流运行ID',
      },
      nodeId: {
        type: 'string',
        description: '节点ID',
      },
      feedback: {
        type: 'string',
        description: '用户的反馈内容，应清晰说明需要修改什么',
      },
      feedbackType: {
        type: 'string',
        enum: ['correction', 'question', 'approval_pending'],
        description: '反馈类型',
      },
    },
    required: ['runId', 'nodeId', 'feedback'],
  },
  execute: async (toolCallId, params) => {
    const { runId, nodeId, feedback, feedbackType = 'correction' } = params as {
      runId: string;
      nodeId: string;
      feedback: string;
      feedbackType?: string;
    };

    try {
      const response = await fetchWithTimeout(
        `${API_BASE}/runs/${runId}/nodes/${nodeId}/feedback`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            feedback,
            feedbackType,
            toolCallId,
          }),
        }
      );
      const data = await response.json();

      if (!response.ok) {
        return {
          content: [{ type: 'text', text: `反馈提交失败：${data.error}` }],
          isError: true,
        };
      }

      return {
        content: [
          {
            type: 'text',
            text: `反馈已提交成功。反馈ID: ${data.feedback_id}。\n现在可以调用 retry_node 工具重跑该节点。`,
          },
        ],
        details: {
          feedbackId: data.feedback_id,
          nodeId,
        },
      };
    } catch (error) {
      return {
        content: [{ type: 'text', text: `反馈提交失败：${error}` }],
        isError: true,
      };
    }
  },
};

// Tool 4: approve_node
export const approveNodeTool: AgentTool = {
  name: 'approve_node',
  description:
    '确认当前节点的产出符合用户要求，工作流将进入下一个节点。如果用户明确表示满意或说"可以了"、"没问题"等，应调用此工具。',
  parameters: {
    type: 'object',
    properties: {
      runId: {
        type: 'string',
        description: '工作流运行ID',
      },
      nodeId: {
        type: 'string',
        description: '节点ID',
      },
      comment: {
        type: 'string',
        description: '可选的确认备注',
      },
    },
    required: ['runId', 'nodeId'],
  },
  execute: async (toolCallId, params) => {
    const { runId, nodeId, comment } = params as {
      runId: string;
      nodeId: string;
      comment?: string;
    };

    try {
      const response = await fetchWithTimeout(
        `${API_BASE}/runs/${runId}/nodes/${nodeId}/approve`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            comment: comment || '用户确认通过',
            toolCallId,
          }),
        }
      );
      const data = await response.json();

      if (!response.ok) {
        return {
          content: [{ type: 'text', text: `确认失败：${data.error}` }],
          isError: true,
        };
      }

      let message = `节点 ${nodeId} 已确认通过。\n工作流即将进入下一节点。`;
      if (data.next_node_id) {
        message += `\n下一节点ID: ${data.next_node_id}`;
      }

      return {
        content: [{ type: 'text', text: message }],
        details: {
          nodeId,
          nextNodeId: data.next_node_id,
          runStatus: data.run_status,
        },
      };
    } catch (error) {
      return {
        content: [{ type: 'text', text: `确认失败：${error}` }],
        isError: true,
      };
    }
  },
};

// Tool 5: retry_node
export const retryNodeTool: AgentTool = {
  name: 'retry_node',
  description:
    '触发指定节点重新执行。通常在用户提交反馈后调用，让工作流基于用户反馈重新生成节点产出。',
  parameters: {
    type: 'object',
    properties: {
      runId: {
        type: 'string',
        description: '工作流运行ID',
      },
      nodeId: {
        type: 'string',
        description: '节点ID',
      },
      reason: {
        type: 'string',
        description: "重跑原因，如'用户反馈修正'",
      },
    },
    required: ['runId', 'nodeId'],
  },
  execute: async (toolCallId, params) => {
    const { runId, nodeId, reason } = params as {
      runId: string;
      nodeId: string;
      reason?: string;
    };

    try {
      const response = await fetchWithTimeout(
        `${API_BASE}/runs/${runId}/nodes/${nodeId}/retry`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            reason: reason || '用户要求重跑',
            toolCallId,
          }),
        }
      );
      const data = await response.json();

      if (!response.ok) {
        return {
          content: [{ type: 'text', text: `重跑失败：${data.error}` }],
          isError: true,
        };
      }

      return {
        content: [
          {
            type: 'text',
            text: `节点 ${nodeId} 重跑已触发。\n请等待节点执行完成。`,
          },
        ],
        details: {
          nodeId,
          jobId: data.job_id,
          status: 'RETRYING',
        },
      };
    } catch (error) {
      return {
        content: [{ type: 'text', text: `重跑失败：${error}` }],
        isError: true,
      };
    }
  },
};

// Tool 6: get_node_history
export const getNodeHistoryTool: AgentTool = {
  name: 'get_node_history',
  description:
    '获取指定节点的执行历史记录，包括之前所有重跑记录和反馈历史。用于了解节点迭代过程。',
  parameters: {
    type: 'object',
    properties: {
      runId: {
        type: 'string',
        description: '工作流运行ID',
      },
      nodeId: {
        type: 'string',
        description: '节点ID',
      },
    },
    required: ['runId', 'nodeId'],
  },
  execute: async (_toolCallId, params) => {
    const { runId, nodeId } = params as { runId: string; nodeId: string };

    try {
      const response = await fetchWithTimeout(
        `${API_BASE}/runs/${runId}/nodes/${nodeId}/history`
      );
      const data = await response.json();

      if (!response.ok) {
        return {
          content: [{ type: 'text', text: `获取历史失败：${data.error}` }],
          isError: true,
        };
      }

      return {
        content: [
          {
            type: 'text',
            text: `=== 节点 ${nodeId} 执行历史 ===\n\n${JSON.stringify(data, null, 2)}`,
          },
        ],
        details: {
          totalRuns: data.runs?.length || 0,
          latestRunId: data.runs?.[0]?.run_id,
        },
      };
    } catch (error) {
      return {
        content: [{ type: 'text', text: `获取历史失败：${error}` }],
        isError: true,
      };
    }
  },
};

// Export all tools
export const mingAgentTools: AgentTool[] = [
  getRunStatusTool,
  readNodeArtifactTool,
  submitFeedbackTool,
  approveNodeTool,
  retryNodeTool,
  getNodeHistoryTool,
];
