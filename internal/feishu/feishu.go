package feishu

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/LJW0401/lark-agent-bridge/internal/config"
)

// Client 封装所有 lark-cli 调用
type Client struct {
	cfg    *config.Config
	logger *config.Logger
	mu     sync.Mutex // 保护 subCmd
	subCmd *exec.Cmd  // 事件订阅子进程
}

// NewClient 创建飞书客户端
func NewClient(cfg *config.Config, logger *config.Logger) *Client {
	return &Client{cfg: cfg, logger: logger}
}

// Close 清理子进程，并发安全，可多次调用
func (c *Client) Close() {
	c.mu.Lock()
	cmd := c.subCmd
	c.subCmd = nil
	c.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	c.logger.Log("终止 lark-cli 事件订阅进程 (PID: %d)", cmd.Process.Pid)
	cmd.Process.Signal(os.Interrupt)
	// 给进程 3 秒优雅退出
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

// Event 表示飞书事件中解析出的消息
type Event struct {
	ChatID    string
	MessageID string
	MsgType   string
	Text      string
}

// Subscribe 订阅飞书事件，逐条解析后通过 channel 发送
func (c *Client) Subscribe(events chan<- Event) error {
	// 如果已有订阅进程，先关闭
	c.Close()

	args := []string{"event", "+subscribe"}
	for _, t := range c.cfg.Feishu.EventTypes {
		args = append(args, "--event-types", t)
	}
	args = append(args, "--as", "bot", "--force")

	c.logger.Log("执行命令: %s %s", c.cfg.Feishu.LarkCliCmd, strings.Join(args, " "))
	cmd := exec.Command(c.cfg.Feishu.LarkCliCmd, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建 stderr 管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 lark-cli event subscribe 失败: %w", err)
	}

	c.mu.Lock()
	c.subCmd = cmd
	c.mu.Unlock()

	// 捕获 stderr：仅记录真正的错误（JSON 格式），忽略 lark-cli 状态信息和 SDK 内部日志
	go func() {
		defer c.logger.Recover("feishu-stderr-scanner")
		errScanner := bufio.NewScanner(stderr)
		for errScanner.Scan() {
			line := errScanner.Text()
			// JSON 错误响应（如 not configured）需要记录
			if strings.HasPrefix(strings.TrimSpace(line), "{") ||
				strings.HasPrefix(strings.TrimSpace(line), "}") ||
				strings.Contains(line, `"ok": false`) ||
				strings.Contains(line, `"error"`) ||
				strings.Contains(line, `"message"`) ||
				strings.Contains(line, `"hint"`) {
				c.logger.Log("lark-cli error: %s", line)
			}
			// 状态信息、SDK Info/Error、连接信息等静默丢弃
		}
	}()

	go func() {
		defer c.logger.Recover("feishu-event-scanner")
		defer close(events)
		scanner := bufio.NewScanner(stdout)
		// 飞书事件可能较大，增大缓冲区
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}

			c.logger.Log("Raw event: %.500s", line)
			ev := c.parseEvent(line)
			if ev != nil {
				events <- *ev
			}
		}

		if err := cmd.Wait(); err != nil {
			c.logger.Log("lark-cli event subscribe 退出: %v", err)
		}
	}()

	return nil
}

func (c *Client) parseEvent(raw string) *Event {
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		c.logger.Log("解析事件 JSON 失败: %v", err)
		return nil
	}

	chatID := jsonPath(data, "event.message.chat_id", "chat_id", "message.chat_id")
	messageID := jsonPath(data, "event.message.message_id", "message_id", "message.message_id")
	msgType := jsonPath(data, "event.message.message_type", "message_type", "message.message_type")
	contentRaw := jsonPath(data, "event.message.content", "content", "message.content")

	if chatID == "" || messageID == "" {
		c.logger.Log("跳过: 缺少 chat_id 或 message_id")
		return nil
	}

	if msgType != "text" {
		c.logger.Log("跳过非文本消息 (type: %s)", msgType)
		return nil
	}

	// content 是一个 JSON 字符串，需要二次解析
	var content map[string]any
	if err := json.Unmarshal([]byte(contentRaw), &content); err != nil {
		c.logger.Log("解析 content JSON 失败: %v", err)
		return nil
	}

	text, _ := content["text"].(string)
	if text == "" {
		c.logger.Log("跳过: 空文本")
		return nil
	}

	return &Event{
		ChatID:    chatID,
		MessageID: messageID,
		MsgType:   msgType,
		Text:      text,
	}
}

// AddReaction 给消息添加表情，返回 reaction_id
func (c *Client) AddReaction(messageID, emojiType string) (string, error) {
	paramsBytes, _ := json.Marshal(map[string]string{"message_id": messageID})
	params := string(paramsBytes)
	dataBytes, _ := json.Marshal(map[string]any{"reaction_type": map[string]string{"emoji_type": emojiType}})
	data := string(dataBytes)

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "im", "reactions", "create",
			"--params", params, "--data", data, "--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("Add reaction 命令失败 (attempt %d/%d): %v", attempt, c.cfg.Retry.MaxRetries, err)
		} else {
			var resp map[string]any
			if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
				c.logger.Log("Add reaction 响应解析失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			} else if rid := jsonPath(resp, "data.reaction_id"); rid != "" {
				return rid, nil
			} else {
				c.logger.Log("Add reaction 未返回 reaction_id (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("add reaction 失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// RemoveReaction 移除消息表情
func (c *Client) RemoveReaction(messageID, reactionID string) error {
	paramsBytes, _ := json.Marshal(map[string]string{"message_id": messageID, "reaction_id": reactionID})
	params := string(paramsBytes)

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "im", "reactions", "delete",
			"--params", params, "--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("Remove reaction 命令失败 (attempt %d/%d): %v", attempt, c.cfg.Retry.MaxRetries, err)
		} else {
			var resp map[string]any
			if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
				c.logger.Log("Remove reaction 响应解析失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			} else if code, ok := resp["code"].(float64); ok && code == 0 {
				return nil
			} else {
				c.logger.Log("Remove reaction 失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("remove reaction 失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// ReplyMessage 回复一条消息，返回回复消息的 message_id
func (c *Client) ReplyMessage(messageID, text string, markdown bool) (string, error) {
	chunks := ChunkMessage(text, c.cfg.Stream.MessageLimit)
	firstID := ""

	for i, chunk := range chunks {
		replyID, err := c.replyOnce(messageID, chunk, markdown)
		if err != nil {
			return firstID, fmt.Errorf("回复第 %d/%d 片失败: %w", i+1, len(chunks), err)
		}
		if i == 0 {
			firstID = replyID
		}
	}

	return firstID, nil
}

func (c *Client) replyOnce(messageID, text string, markdown bool) (string, error) {
	msgFlag := "--text"
	if markdown {
		msgFlag = "--markdown"
	}

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "im", "+messages-reply",
			"--message-id", messageID, msgFlag, text, "--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("Reply 命令失败 (attempt %d/%d): %v", attempt, c.cfg.Retry.MaxRetries, err)
		} else {
			var resp map[string]any
			if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
				c.logger.Log("Reply 响应解析失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			} else if rid := jsonPath(resp, "data.message_id", "message_id"); rid != "" {
				return rid, nil
			} else {
				c.logger.Log("Reply 未返回 message_id (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("reply 失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// SendMessage 发送消息到聊天（非引用回复）
func (c *Client) SendMessage(chatID, text string, markdown bool) error {
	chunks := ChunkMessage(text, c.cfg.Stream.MessageLimit)

	for i, chunk := range chunks {
		if err := c.sendOnce(chatID, chunk, markdown); err != nil {
			return fmt.Errorf("发送第 %d/%d 片失败: %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (c *Client) sendOnce(chatID, text string, markdown bool) error {
	msgFlag := "--text"
	if markdown {
		msgFlag = "--markdown"
	}

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "im", "+messages-send",
			"--chat-id", chatID, msgFlag, text, "--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("Send 命令失败 (attempt %d/%d): %v", attempt, c.cfg.Retry.MaxRetries, err)
		} else {
			var resp map[string]any
			if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
				c.logger.Log("Send 响应解析失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			} else {
				ok := false
				if v, exists := resp["ok"]; exists {
					ok, _ = v.(bool)
				}
				if code, exists := resp["code"]; exists {
					if cd, _ := code.(float64); cd == 0 {
						ok = true
					}
				}
				if ok {
					return nil
				}
				c.logger.Log("Send 失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("send 失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// UpdateMessage 更新已有消息（流式更新用）
func (c *Client) UpdateMessage(msgID, text string, markdown bool) error {
	text = TruncateMessage(text, c.cfg.Stream.MessageLimit)

	var msgType, content string
	if markdown {
		msgType = "post"
		body := map[string]any{
			"zh_cn": map[string]any{
				"content": []any{
					[]any{
						map[string]any{"tag": "md", "text": text},
					},
				},
			},
		}
		b, _ := json.Marshal(body)
		content = string(b)
	} else {
		msgType = "text"
		body := map[string]string{"text": text}
		b, _ := json.Marshal(body)
		content = string(b)
	}

	reqBody := map[string]string{
		"msg_type": msgType,
		"content":  content,
	}
	reqBytes, _ := json.Marshal(reqBody)

	apiPath := fmt.Sprintf("/open-apis/im/v1/messages/%s", msgID)

	for attempt := 1; attempt <= c.cfg.Retry.MaxRetries; attempt++ {
		out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "api", "PUT", apiPath,
			"--data", string(reqBytes), "--as", "bot").CombinedOutput()
		if err != nil {
			c.logger.Log("Update message 命令失败 (attempt %d/%d): %v", attempt, c.cfg.Retry.MaxRetries, err)
		} else {
			var resp map[string]any
			if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
				c.logger.Log("Update message 响应解析失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			} else if code, ok := resp["code"].(float64); ok && code == 0 {
				return nil
			} else if msg, ok := resp["msg"].(string); ok && msg == "success" {
				return nil
			} else {
				c.logger.Log("Update message 失败 (attempt %d/%d): %s", attempt, c.cfg.Retry.MaxRetries, truncOut(out))
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("update message 失败，已重试 %d 次", c.cfg.Retry.MaxRetries)
}

// ReplyError 发送错误反馈
func (c *Client) ReplyError(chatID, messageID, errMsg string) {
	c.AddReaction(messageID, c.cfg.Feishu.ErrorEmoji)
	c.SendMessage(chatID, "[错误] "+errMsg, false)
}

// --- 工具函数 ---

// jsonPath 从嵌套 map 中按多个路径尝试取值
func jsonPath(data map[string]any, paths ...string) string {
	for _, path := range paths {
		val := getNestedValue(data, strings.Split(path, "."))
		if val != "" {
			return val
		}
	}
	return ""
}

func getNestedValue(data map[string]any, keys []string) string {
	var current any = data
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = m[key]
		if !ok {
			return ""
		}
	}
	switch v := current.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func truncOut(out []byte) string {
	s := strings.ReplaceAll(string(out), "\n", " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

