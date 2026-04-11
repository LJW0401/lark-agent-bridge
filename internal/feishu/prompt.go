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
	sb.WriteString(fmt.Sprintf(`你可以通过 %s 命令行工具操作飞书（发消息、创建日程、查询用户等）。
用法：%s api <METHOD> <PATH> [--params <json>] [--data <json>] [--as bot|user]
运行 %s --help 查看完整用法，用户 ID 类型统一使用 open_id。
`, larkCliCmd, larkCliCmd, larkCliCmd))

	return sb.String()
}
