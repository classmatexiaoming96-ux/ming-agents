package api

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"

	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
)

type feishuNotifier interface {
	NotifyWaitingUserInput(change store.StepStatusChange, targetURL string) error
}

type serverStepNotifier struct {
	sse          *SSEManager
	feishu       feishuNotifier
	frontendBase string
}

func (n *serverStepNotifier) OnStepStatusChanged(change store.StepStatusChange) {
	if n.sse != nil {
		n.sse.Broadcast(change)
	}

	if change.To == domain.StepStatusWaitingUserInput && n.feishu != nil {
		targetURL := fmt.Sprintf("%s/runs/%s", n.frontendBase, change.RunID.String())
		go func() {
			if err := n.feishu.NotifyWaitingUserInput(change, targetURL); err != nil {
				log.Printf("[feishu] waiting_user_input notification failed: %v", err)
			}
		}()
	}
}

type bytedcliFeishuNotifier struct {
	chatID string
}

func newBytedcliFeishuNotifier() *bytedcliFeishuNotifier {
	return &bytedcliFeishuNotifier{
		chatID: firstNonEmpty(os.Getenv("CC_LEAD_CHAT_ID"), os.Getenv("FEISHU_CC_LEAD_CHAT_ID")),
	}
}

func (n *bytedcliFeishuNotifier) NotifyWaitingUserInput(change store.StepStatusChange, targetURL string) error {
	chatID, err := n.resolveChatID()
	if err != nil {
		return err
	}
	text := fmt.Sprintf("节点 %s 已进入等待确认状态\n当前状态：%s\n点击处理：%s", change.StepName, change.To, targetURL)
	cmd := exec.Command("bytedcli", "lark", "im", "messages-send", "--chat-id", chatID, "--text", text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("messages-send: %w: %s", err, string(out))
	}
	return nil
}

func (n *bytedcliFeishuNotifier) resolveChatID() (string, error) {
	if n.chatID != "" {
		return n.chatID, nil
	}
	cmd := exec.Command("bytedcli", "lark", "im", "chat-search", "cc lead", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("chat-search: %w: %s", err, string(out))
	}
	chatID := extractChatID(out)
	if chatID == "" {
		return "", fmt.Errorf("cc lead chat_id not found")
	}
	n.chatID = chatID
	return chatID, nil
}

func extractChatID(out []byte) string {
	var data any
	if err := json.Unmarshal(out, &data); err == nil {
		if id := findStringKey(data, "chat_id"); id != "" {
			return id
		}
		if id := findStringKey(data, "open_chat_id"); id != "" {
			return id
		}
	}
	re := regexp.MustCompile(`(?:chat_id|open_chat_id)["']?\s*[:=]\s*["']([^"'\s,}]+)`)
	match := re.FindSubmatch(out)
	if len(match) == 2 {
		return string(match[1])
	}
	return ""
}

func findStringKey(data any, key string) string {
	switch v := data.(type) {
	case map[string]any:
		for k, value := range v {
			if k == key {
				if s, ok := value.(string); ok {
					return s
				}
			}
			if s := findStringKey(value, key); s != "" {
				return s
			}
		}
	case []any:
		for _, value := range v {
			if s := findStringKey(value, key); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
