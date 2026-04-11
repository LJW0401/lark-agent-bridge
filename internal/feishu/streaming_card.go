package feishu

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// StreamingCard 流式卡片实例
type StreamingCard struct {
	CardID    string // 卡片实体 ID
	ElementID string // 文本组件 ID
	MessageID string // 发送后的消息 ID
	sequence  int    // 流式更新序号（递增）
}

// buildStreamingCardJSON 构造启用流式模式的卡片 JSON
func buildStreamingCardJSON(elementID, initialContent string) string {
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
					"content":    initialContent,
					"element_id": elementID,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

// ReplyStreamingCard 创建流式卡片实体并作为回复发送，返回 StreamingCard
func (c *Client) ReplyStreamingCard(messageID, initialContent string) (*StreamingCard, error) {
	elementID := "streaming_content"

	// 步骤 1：通过 cardkit API 创建卡片实体
	cardJSON := buildStreamingCardJSON(elementID, initialContent)
	createReq, _ := json.Marshal(map[string]string{
		"type": "card_json",
		"data": cardJSON,
	})

	var cardID string
	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "api", "POST",
			"/open-apis/cardkit/v1/cards",
			"--data", string(createReq), "--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("创建流式卡片失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
			time.Sleep(2 * time.Second)
			continue
		}

		var resp map[string]any
		if json.Unmarshal(out, &resp) == nil {
			cardID = jsonPath(resp, "data.card_id", "card_id")
			if cardID != "" {
				break
			}
		}
		c.logger.Log("创建流式卡片未返回 card_id (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
		time.Sleep(2 * time.Second)
	}
	if cardID == "" {
		return nil, fmt.Errorf("创建流式卡片失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
	}
	c.logger.Log("流式卡片已创建: card_id=%s", cardID)

	// 步骤 2：用 type=card 引用 card_id 发送消息
	sendContent, _ := json.Marshal(map[string]any{
		"type": "card",
		"data": map[string]string{"card_id": cardID},
	})

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "im", "+messages-reply",
			"--message-id", messageID,
			"--msg-type", "interactive",
			"--content", string(sendContent),
			"--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("卡片回复失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
			time.Sleep(2 * time.Second)
			continue
		}

		var resp map[string]any
		if json.Unmarshal(out, &resp) == nil {
			replyMsgID := jsonPath(resp, "data.message_id", "message_id")
			if replyMsgID != "" {
				c.logger.Log("流式卡片已发送: message_id=%s", replyMsgID)
				return &StreamingCard{
					CardID:    cardID,
					ElementID: elementID,
					MessageID: replyMsgID,
					sequence:  0,
				}, nil
			}
		}
		c.logger.Log("卡片回复未返回 message_id (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("卡片回复失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// UpdateStreamingContent 流式更新卡片文本（打字机效果）
// content 为完整累积文本，新文本是旧文本前缀的追加时触发打字机动画
func (c *Client) UpdateStreamingContent(card *StreamingCard, content string) error {
	card.sequence++

	reqBody, _ := json.Marshal(map[string]any{
		"content":  content,
		"sequence": card.sequence,
	})

	apiPath := fmt.Sprintf("/open-apis/cardkit/v1/cards/%s/elements/%s/content",
		card.CardID, card.ElementID)

	out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "api", "PUT", apiPath,
		"--data", string(reqBody), "--as", "bot").CombinedOutput()
	if err != nil {
		c.logger.Log("流式更新失败 (seq=%d): %v | %s", card.sequence, err, truncOut(out))
		return fmt.Errorf("流式更新失败: %w", err)
	}

	var resp map[string]any
	if json.Unmarshal(out, &resp) == nil {
		if code, ok := resp["code"].(float64); ok && code != 0 {
			msg := jsonPath(resp, "msg")
			c.logger.Log("流式更新失败 (seq=%d): code=%d, msg=%s", card.sequence, int(code), msg)
			return fmt.Errorf("流式更新失败: code=%d", int(code))
		}
	}

	return nil
}
