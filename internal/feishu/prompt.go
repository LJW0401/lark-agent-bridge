package feishu

import (
	"fmt"
	"strings"
)

// BuildPrompt 将用户消息和上下文信息组合为 Agent 的 prompt
// 追加群聊信息、被提及用户信息和 lark-cli 工具说明
func BuildPrompt(text string, ev Event, chatInfo *ChatInfo, larkCliCmd string) string {
	// 没有额外上下文时直接返回原文
	if ev.ChatType != "group" && len(ev.Mentions) == 0 {
		return text
	}

	var sb strings.Builder
	sb.WriteString(text)
	sb.WriteString("\n\n---\n")

	// 群聊信息
	if ev.ChatType == "group" && chatInfo != nil {
		sb.WriteString("当前群聊信息：\n")
		sb.WriteString(fmt.Sprintf("- 群名：%s\n", chatInfo.Name))
		if chatInfo.Description != "" {
			sb.WriteString(fmt.Sprintf("- 群描述：%s\n", chatInfo.Description))
		}
		sb.WriteString(fmt.Sprintf("- 成员数：%s 人，机器人：%s 个\n", chatInfo.UserCount, chatInfo.BotCount))
		sb.WriteString(fmt.Sprintf("- 群聊 ID：%s\n", ev.ChatID))
		sb.WriteString("\n")
	}

	// 被提及用户信息
	if len(ev.Mentions) > 0 {
		sb.WriteString("消息中提及的用户：\n")
		for _, m := range ev.Mentions {
			sb.WriteString(fmt.Sprintf("- %s (open_id: %s)\n", m.Name, m.OpenID))
		}
		sb.WriteString("\n")
	}

	// lark-cli 工具说明（仅在有被提及用户时注入，避免普通问答浪费 token）
	if len(ev.Mentions) == 0 {
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf(`你可以使用 %s 命令行工具操作飞书，以下是常用示例：

# 创建日程（需要参与者的 open_id）
%s api POST /open-apis/calendar/v4/calendars/primary/events --data '{
  "summary": "会议标题",
  "start_time": {"timestamp": "1234567890"},
  "end_time": {"timestamp": "1234567900"},
  "attendees": [{"type": "user", "is_optional": false, "user_id": "open_id值"}]
}' --params '{"user_id_type": "open_id"}' --as user

# 发送消息给用户
%s api POST /open-apis/im/v1/messages --params '{"receive_id_type": "open_id"}' --data '{
  "receive_id": "open_id值",
  "msg_type": "text",
  "content": "{\"text\":\"消息内容\"}"
}' --as bot

# 发送消息到群聊
%s api POST /open-apis/im/v1/messages --params '{"receive_id_type": "chat_id"}' --data '{
  "receive_id": "群聊ID",
  "msg_type": "text",
  "content": "{\"text\":\"消息内容\"}"
}' --as bot

# 查询用户信息
%s api GET /open-apis/contact/v3/users/:user_id --params '{"user_id_type": "open_id"}' --as bot

注意：如果不确定 API 用法，可以先运行 %s --help 或 %s api --help 查看帮助。
`, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd))

	return sb.String()
}
