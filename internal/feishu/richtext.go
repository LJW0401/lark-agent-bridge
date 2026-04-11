package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
)

// extractPostText 从 post（富文本）消息的 content JSON 中提取纯文本
// post content 结构: {"zh_cn": {"title": "...", "content": [[{tag, ...}], ...]}}
// 支持多语言 key（zh_cn, en_us, ja_jp 等），优先 zh_cn，其次取第一个
func extractPostText(contentRaw string) string {
	var post map[string]any
	if err := json.Unmarshal([]byte(contentRaw), &post); err != nil {
		return ""
	}

	// 选择语言版本：优先 zh_cn，否则取第一个
	var body map[string]any
	if v, ok := post["zh_cn"].(map[string]any); ok {
		body = v
	} else {
		for _, v := range post {
			if m, ok := v.(map[string]any); ok {
				body = m
				break
			}
		}
	}
	if body == nil {
		return ""
	}

	var sb strings.Builder

	// 标题
	if title, ok := body["title"].(string); ok && title != "" {
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}

	// 段落列表
	paragraphs, ok := body["content"].([]any)
	if !ok {
		return sb.String()
	}

	for i, para := range paragraphs {
		elements, ok := para.([]any)
		if !ok {
			continue
		}
		if i > 0 {
			sb.WriteString("\n")
		}
		for _, elem := range elements {
			m, ok := elem.(map[string]any)
			if !ok {
				continue
			}
			tag, _ := m["tag"].(string)
			sb.WriteString(renderPostElement(tag, m))
		}
	}

	return strings.TrimSpace(sb.String())
}

// renderPostElement 将单个富文本元素转换为可读文本
func renderPostElement(tag string, m map[string]any) string {
	switch tag {
	case "text":
		text, _ := m["text"].(string)
		return text

	case "a":
		text, _ := m["text"].(string)
		href, _ := m["href"].(string)
		if href != "" {
			return fmt.Sprintf("[%s](%s)", text, href)
		}
		return text

	case "at":
		userName, _ := m["user_name"].(string)
		userID, _ := m["user_id"].(string)
		if userName != "" {
			return "@" + userName
		}
		if userID == "all" {
			return "@所有人"
		}
		return "@" + userID

	case "emotion":
		emojiType, _ := m["emoji_type"].(string)
		return "[" + emojiType + "]"

	case "code_block":
		text, _ := m["text"].(string)
		lang, _ := m["language"].(string)
		if lang != "" {
			return fmt.Sprintf("\n```%s\n%s\n```\n", strings.ToLower(lang), text)
		}
		return fmt.Sprintf("\n```\n%s\n```\n", text)

	case "md":
		text, _ := m["text"].(string)
		return text

	case "hr":
		return "\n---\n"

	case "img", "media":
		// 暂不处理图片和媒体
		return ""

	default:
		// 未知 tag，尝试提取 text 字段
		if text, ok := m["text"].(string); ok {
			return text
		}
		return ""
	}
}
