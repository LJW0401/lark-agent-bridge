package feishu

import (
	"fmt"
	"strings"
)

// BuildPrompt 将用户消息和上下文信息组合为 Agent 的 prompt
// 如果有被 @提及 的用户，追加用户信息和 lark-cli 工具说明
func BuildPrompt(text string, mentions []Mention, larkCliCmd string) string {
	if len(mentions) == 0 {
		return text
	}

	var sb strings.Builder
	sb.WriteString(text)

	// 追加被提及用户信息
	sb.WriteString("\n\n---\n")
	sb.WriteString("消息中提及的用户：\n")
	for _, m := range mentions {
		sb.WriteString(fmt.Sprintf("- %s (open_id: %s)\n", m.Name, m.OpenID))
	}

	// 追加 lark-cli 工具说明
	sb.WriteString("\n")
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

# 查询用户信息
%s api GET /open-apis/contact/v3/users/:user_id --params '{"user_id_type": "open_id"}' --as bot

注意：如果不确定 API 用法，可以先运行 %s --help 或 %s api --help 查看帮助。
`, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd, larkCliCmd))

	return sb.String()
}
