package feishu

import (
	"testing"
)

func TestParseMentions(t *testing.T) {
	botID := "ou_bot_123"
	c := &Client{botOpenID: botID}

	tests := []struct {
		name     string
		data     map[string]any
		wantLen  int
		wantBot  int // 期望 IsBot=true 的数量
	}{
		{
			name:    "无 mentions",
			data:    map[string]any{"event": map[string]any{"message": map[string]any{}}},
			wantLen: 0,
		},
		{
			name: "包含机器人和用户",
			data: map[string]any{"event": map[string]any{"message": map[string]any{
				"mentions": []any{
					map[string]any{
						"key":  "@_user_1",
						"name": "TestBot",
						"id":   map[string]any{"open_id": botID},
					},
					map[string]any{
						"key":  "@_user_2",
						"name": "张三",
						"id":   map[string]any{"open_id": "ou_user_456"},
					},
				},
			}}},
			wantLen: 2,
			wantBot: 1,
		},
		{
			name: "botOpenID 为空时全部非 bot",
			data: map[string]any{"event": map[string]any{"message": map[string]any{
				"mentions": []any{
					map[string]any{
						"key":  "@_user_1",
						"name": "SomeBot",
						"id":   map[string]any{"open_id": "ou_any"},
					},
				},
			}}},
			wantLen: 1,
			wantBot: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 第三个测试用空 botOpenID
			client := c
			if tt.name == "botOpenID 为空时全部非 bot" {
				client = &Client{}
			}

			mentions := client.parseMentions(tt.data)
			if len(mentions) != tt.wantLen {
				t.Errorf("got %d mentions, want %d", len(mentions), tt.wantLen)
			}
			botCount := 0
			for _, m := range mentions {
				if m.IsBot {
					botCount++
				}
			}
			if botCount != tt.wantBot {
				t.Errorf("got %d bot mentions, want %d", botCount, tt.wantBot)
			}
		})
	}
}

func TestReplaceMentionPlaceholders(t *testing.T) {
	c := &Client{botOpenID: "ou_bot"}

	tests := []struct {
		name     string
		text     string
		mentions []Mention
		expected string
	}{
		{
			name:     "删除本机器人占位符",
			text:     "@_user_1 你好世界",
			mentions: []Mention{{Key: "@_user_1", Name: "Bot", OpenID: "ou_bot", IsBot: true}},
			expected: "你好世界",
		},
		{
			name:     "替换用户为真实姓名",
			text:     "@_user_1 帮 @_user_2 创建日程",
			mentions: []Mention{
				{Key: "@_user_1", Name: "Bot", OpenID: "ou_bot", IsBot: true},
				{Key: "@_user_2", Name: "张三", OpenID: "ou_user"},
			},
			expected: "帮 @张三 创建日程",
		},
		{
			name:     "保留其他机器人",
			text:     "@_user_1 转发给 @_user_2",
			mentions: []Mention{
				{Key: "@_user_1", Name: "Bot", OpenID: "ou_bot", IsBot: true},
				{Key: "@_user_2", Name: "另一个机器人", OpenID: "ou_other_bot"},
			},
			expected: "转发给 @另一个机器人",
		},
		{
			name:     "无 name 的占位符保留原样",
			text:     "@_user_1 看看 @_user_2",
			mentions: []Mention{
				{Key: "@_user_1", Name: "Bot", OpenID: "ou_bot", IsBot: true},
				{Key: "@_user_2", OpenID: "ou_unknown"},
			},
			expected: "看看 @_user_2",
		},
		{
			name:     "无 mentions",
			text:     "普通消息",
			mentions: nil,
			expected: "普通消息",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.replaceMentionPlaceholders(tt.text, tt.mentions)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		ev          Event
		chatInfo    *ChatInfo
		wantContain []string
		wantExclude []string
	}{
		{
			name: "私聊无 mention 返回原文",
			text: "你好",
			ev:   Event{ChatType: "p2p"},
			wantContain: []string{"你好"},
			wantExclude: []string{"群聊信息", "lark-cli"},
		},
		{
			name: "群聊无 mention 只注入群聊信息",
			text: "你好",
			ev:   Event{ChatType: "group", ChatID: "oc_123"},
			chatInfo: &ChatInfo{Name: "测试群", UserCount: "5", BotCount: "1"},
			wantContain: []string{"你好", "测试群", "5 人"},
			wantExclude: []string{"lark-cli"},
		},
		{
			name: "有 mention 注入用户信息和工具说明",
			text: "帮 @张三 创建日程",
			ev: Event{
				ChatType: "group",
				ChatID:   "oc_123",
				Mentions: []Mention{{Name: "张三", OpenID: "ou_456"}},
			},
			chatInfo: &ChatInfo{Name: "测试群", UserCount: "5", BotCount: "1"},
			wantContain: []string{"张三", "ou_456", "lark-cli", "测试群"},
		},
		{
			name: "群描述非空时显示",
			text: "你好",
			ev:   Event{ChatType: "group", ChatID: "oc_123"},
			chatInfo: &ChatInfo{Name: "项目群", Description: "用于项目讨论", UserCount: "10", BotCount: "2"},
			wantContain: []string{"项目群", "用于项目讨论"},
		},
		{
			name: "群描述为空时不显示",
			text: "你好",
			ev:   Event{ChatType: "group", ChatID: "oc_123"},
			chatInfo: &ChatInfo{Name: "项目群", UserCount: "10", BotCount: "2"},
			wantExclude: []string{"群描述"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPrompt(tt.text, tt.ev, tt.chatInfo, "lark-cli")
			for _, s := range tt.wantContain {
				if !contains(got, s) {
					t.Errorf("result should contain %q, got:\n%s", s, got)
				}
			}
			for _, s := range tt.wantExclude {
				if contains(got, s) {
					t.Errorf("result should NOT contain %q, got:\n%s", s, got)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
