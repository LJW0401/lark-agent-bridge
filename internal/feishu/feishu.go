package feishu

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/LJW0401/lark-agent-bridge/internal/config"
)

// Client 封装所有 lark-cli 调用
type Client struct {
	cfg       *config.Config
	logger    *config.Logger
	mu        sync.Mutex // 保护 subCmd
	subCmd    *exec.Cmd  // 事件订阅子进程
	botOpenID string     // 机器人的 open_id，用于判断群聊 @mention
}

// NewClient 创建飞书客户端
func NewClient(cfg *config.Config, logger *config.Logger) *Client {
	c := &Client{cfg: cfg, logger: logger}
	c.fetchBotOpenID()
	return c
}

// fetchBotOpenID 获取机器人的 open_id
func (c *Client) fetchBotOpenID() {
	out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "api", "GET",
		"/open-apis/bot/v3/info", "--as", "bot").CombinedOutput()
	if err != nil {
		c.logger.Log("获取机器人信息失败: %v | %s", err, truncOut(out))
		return
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		return
	}
	if bot, ok := resp["bot"].(map[string]any); ok {
		if openID, ok := bot["open_id"].(string); ok {
			c.botOpenID = openID
			c.logger.Log("机器人 open_id: %s", openID)
		}
	}
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
	ChatType  string // "p2p" 或 "group"
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
	args = append(args, "--as", "bot")

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

	// 捕获 stderr：记录错误和关键状态，过滤 SDK 内部噪音
	go func() {
		defer c.logger.Recover("feishu-stderr-scanner")
		errScanner := bufio.NewScanner(stderr)
		for errScanner.Scan() {
			line := errScanner.Text()
			// 去掉 ANSI 颜色码后判断
			plain := stripAnsi(line)
			switch {
			// JSON 错误响应（如 not configured）
			case strings.HasPrefix(strings.TrimSpace(plain), "{"),
				strings.HasPrefix(strings.TrimSpace(plain), "}"),
				strings.Contains(plain, `"ok": false`),
				strings.Contains(plain, `"error"`):
				c.logger.Log("lark-cli error: %s", plain)
			// 连接状态（重要，需要知道是否连上）
			case strings.Contains(plain, "Connected"),
				strings.Contains(plain, "Connecting"),
				strings.Contains(plain, "disconnected"),
				strings.Contains(plain, "reconnect"),
				strings.Contains(plain, "terminated"),
				strings.Contains(plain, "shutting down"):
				c.logger.Log("lark-cli: %s", plain)
			// SDK Error 中的 "not found handler" 是正常的，静默
			// 其他 SDK Info 也静默
			}
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
	chatType := jsonPath(data, "event.message.chat_type", "chat_type", "message.chat_type")
	contentRaw := jsonPath(data, "event.message.content", "content", "message.content")

	if chatID == "" || messageID == "" {
		c.logger.Log("跳过: 缺少 chat_id 或 message_id")
		return nil
	}

	// 群聊中只处理 @机器人 的消息
	if chatType == "group" {
		if !c.isBotMentioned(data) {
			return nil
		}
	}

	// 提取文本内容
	var text string
	switch msgType {
	case "text":
		var content map[string]any
		if err := json.Unmarshal([]byte(contentRaw), &content); err != nil {
			c.logger.Log("解析 content JSON 失败: %v", err)
			return nil
		}
		text, _ = content["text"].(string)

	case "post":
		text = extractPostText(contentRaw)

	default:
		c.logger.Log("跳过不支持的消息类型 (type: %s)", msgType)
		return nil
	}

	// 清理 @mention 占位符（如 @_user_1）
	text = cleanMentionPlaceholders(text)

	if text == "" {
		c.logger.Log("跳过: 空文本 (type: %s)", msgType)
		return nil
	}

	return &Event{
		ChatID:    chatID,
		MessageID: messageID,
		MsgType:   msgType,
		ChatType:  chatType,
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
			c.logger.Log("Add reaction 命令失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
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
			c.logger.Log("Remove reaction 命令失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
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
			c.logger.Log("Reply 命令失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
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
			c.logger.Log("Send 命令失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
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
			c.logger.Log("Update message 命令失败 (attempt %d/%d): %v | %s", attempt, c.cfg.Retry.MaxRetries, err, truncOut(out))
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

// UpdateMessageOnce 更新消息（不重试，适用于状态消息等非关键更新）
func (c *Client) UpdateMessageOnce(msgID, text string, markdown bool) error {
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

	out, err := exec.Command(c.cfg.Feishu.LarkCliCmd, "api", "PUT", apiPath,
		"--data", string(reqBytes), "--as", "bot").CombinedOutput()
	if err != nil {
		return fmt.Errorf("update message 失败: %v | %s", err, truncOut(out))
	}

	var resp map[string]any
	if json.Unmarshal(out, &resp) == nil {
		if code, ok := resp["code"].(float64); ok && code != 0 {
			return fmt.Errorf("update message 失败: code=%d", int(code))
		}
	}
	return nil
}

// ReplyError 发送错误反馈
func (c *Client) ReplyError(chatID, messageID, errMsg string) {
	c.AddReaction(messageID, c.cfg.Feishu.ErrorEmoji)
	c.SendMessage(chatID, "[错误] "+errMsg, false)
}

// --- @mention 相关 ---

// isBotMentioned 检查消息的 mentions 中是否包含机器人
func (c *Client) isBotMentioned(data map[string]any) bool {
	if c.botOpenID == "" {
		// 未获取到 bot open_id，放行所有消息避免功能不可用
		return true
	}

	// mentions 可能在多个路径下
	mentionsRaw := getNestedAny(data, []string{"event", "message", "mentions"})
	if mentionsRaw == nil {
		mentionsRaw = getNestedAny(data, []string{"message", "mentions"})
	}

	mentions, ok := mentionsRaw.([]any)
	if !ok || len(mentions) == 0 {
		return false
	}

	for _, m := range mentions {
		mention, ok := m.(map[string]any)
		if !ok {
			continue
		}
		// id 可能是嵌套对象 {"open_id": "..."} 或直接字符串
		if idObj, ok := mention["id"].(map[string]any); ok {
			if openID, ok := idObj["open_id"].(string); ok && openID == c.botOpenID {
				return true
			}
		}
	}
	return false
}

// getNestedAny 从嵌套 map 中按路径取值（返回 any，不强制 string）
func getNestedAny(data map[string]any, keys []string) any {
	var current any = data
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = m[key]
		if !ok {
			return nil
		}
	}
	return current
}

// mentionPlaceholderRe 匹配 @_user_N 占位符
var mentionPlaceholderRe = regexp.MustCompile(`@_user_\d+`)

// cleanMentionPlaceholders 移除文本中的 @_user_N 占位符并清理多余空白
func cleanMentionPlaceholders(text string) string {
	text = mentionPlaceholderRe.ReplaceAllString(text, "")
	// 清理连续空格
	text = regexp.MustCompile(`\s{2,}`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
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

// stripAnsi 去掉 ANSI 转义序列（颜色码等）
func stripAnsi(s string) string {
	result := strings.Builder{}
	inEsc := false
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

