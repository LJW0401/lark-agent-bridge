package feishu

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// StreamingCard 流式卡片实例（内联方式，不依赖 cardkit API）
type StreamingCard struct {
	MessageID string // 卡片消息 ID
	ElementID string // 文本组件 ID
}

// buildStreamingCardContent 构造启用流式模式的内联卡片 JSON
func buildStreamingCardContent(elementID, content string) string {
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"streaming_mode": true,
			"update_multi":   true,
			"streaming_config": map[string]any{
				"print_frequency_ms": map[string]any{"default": 30},
				"print_step":         map[string]any{"default": 2},
				"print_strategy":     "fast",
			},
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":        "markdown",
					"content":    content,
					"element_id": elementID,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

// ReplyStreamingCard 发送一个流式卡片作为回复，返回 StreamingCard
func (c *Client) ReplyStreamingCard(messageID, initialContent string) (*StreamingCard, error) {
	elementID := "streaming_content"
	cardContent := buildStreamingCardContent(elementID, initialContent)

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "im", "+messages-reply",
			"--message-id", messageID,
			"--msg-type", "interactive",
			"--content", cardContent,
			"--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("流式卡片回复失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
			time.Sleep(2 * time.Second)
			continue
		}

		var resp map[string]any
		if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
			c.logger.Log("流式卡片回复响应解析失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			time.Sleep(2 * time.Second)
			continue
		}

		replyMsgID := jsonPath(resp, "data.message_id", "message_id")
		if replyMsgID != "" {
			c.logger.Log("流式卡片已发送: message_id=%s", replyMsgID)
			return &StreamingCard{
				MessageID: replyMsgID,
				ElementID: elementID,
			}, nil
		}

		c.logger.Log("流式卡片回复未返回 message_id (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("流式卡片回复失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// UpdateStreamingContent 流式更新卡片文本内容
// content 为新的完整文本，新文本是旧文本前缀的追加时有打字机动画
func (c *Client) UpdateStreamingContent(card *StreamingCard, content string) error {
	cardContent := buildStreamingCardContent(card.ElementID, content)

	reqBody, _ := json.Marshal(map[string]string{
		"msg_type": "interactive",
		"content":  cardContent,
	})

	apiPath := fmt.Sprintf("/open-apis/im/v1/messages/%s", card.MessageID)

	out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "api", "PATCH", apiPath,
		"--data", string(reqBody), "--as", "bot").CombinedOutput()
	if err != nil {
		c.logger.Log("流式更新失败: %v | %s", err, truncOut(out))
		return fmt.Errorf("流式更新失败: %w", err)
	}

	var resp map[string]any
	if json.Unmarshal(out, &resp) == nil {
		if code, ok := resp["code"].(float64); ok && code != 0 {
			msg := jsonPath(resp, "msg")
			c.logger.Log("流式更新失败: code=%d, msg=%s", int(code), msg)
			return fmt.Errorf("流式更新失败: code=%d", int(code))
		}
	}

	return nil
}
