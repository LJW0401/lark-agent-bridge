package feishu

import "testing"

func TestExtractPostText(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name: "基本文本段落",
			content: `{
				"zh_cn": {
					"title": "测试标题",
					"content": [
						[{"tag": "text", "text": "第一段文字"}],
						[{"tag": "text", "text": "第二段文字"}]
					]
				}
			}`,
			expected: "测试标题\n\n第一段文字\n第二段文字",
		},
		{
			name: "无标题",
			content: `{
				"zh_cn": {
					"content": [
						[{"tag": "text", "text": "纯文字内容"}]
					]
				}
			}`,
			expected: "纯文字内容",
		},
		{
			name: "包含链接",
			content: `{
				"zh_cn": {
					"content": [
						[
							{"tag": "text", "text": "请访问 "},
							{"tag": "a", "text": "飞书", "href": "https://feishu.cn"}
						]
					]
				}
			}`,
			expected: "请访问 [飞书](https://feishu.cn)",
		},
		{
			name: "包含@提及",
			content: `{
				"zh_cn": {
					"content": [
						[
							{"tag": "text", "text": "请 "},
							{"tag": "at", "user_id": "ou_xxx", "user_name": "张三"},
							{"tag": "text", "text": " 处理"}
						]
					]
				}
			}`,
			expected: "请 @张三 处理",
		},
		{
			name: "包含@所有人",
			content: `{
				"zh_cn": {
					"content": [
						[
							{"tag": "at", "user_id": "all"},
							{"tag": "text", "text": " 注意"}
						]
					]
				}
			}`,
			expected: "@所有人 注意",
		},
		{
			name: "包含表情",
			content: `{
				"zh_cn": {
					"content": [
						[
							{"tag": "text", "text": "不错 "},
							{"tag": "emotion", "emoji_type": "THUMBSUP"}
						]
					]
				}
			}`,
			expected: "不错 [THUMBSUP]",
		},
		{
			name: "包含代码块",
			content: `{
				"zh_cn": {
					"content": [
						[{"tag": "text", "text": "示例代码："}],
						[{"tag": "code_block", "language": "GO", "text": "fmt.Println(\"hello\")"}]
					]
				}
			}`,
			expected: "示例代码：\n\n```go\nfmt.Println(\"hello\")\n```",
		},
		{
			name: "跳过图片",
			content: `{
				"zh_cn": {
					"content": [
						[{"tag": "text", "text": "看这张图："}],
						[{"tag": "img", "image_key": "img_xxx"}],
						[{"tag": "text", "text": "以上是效果图"}]
					]
				}
			}`,
			expected: "看这张图：\n\n以上是效果图",
		},
		{
			name: "英文内容回退",
			content: `{
				"en_us": {
					"title": "Hello",
					"content": [
						[{"tag": "text", "text": "English content"}]
					]
				}
			}`,
			expected: "Hello\n\nEnglish content",
		},
		{
			name: "包含分割线",
			content: `{
				"zh_cn": {
					"content": [
						[{"tag": "text", "text": "上面"}],
						[{"tag": "hr"}],
						[{"tag": "text", "text": "下面"}]
					]
				}
			}`,
			expected: "上面\n\n---\n\n下面",
		},
		{
			name: "包含md标签",
			content: `{
				"zh_cn": {
					"content": [
						[{"tag": "md", "text": "**加粗** 和 *斜体*"}]
					]
				}
			}`,
			expected: "**加粗** 和 *斜体*",
		},
		{
			name: "扁平结构（实际飞书客户端格式）",
			content: `{"title":"","content":[[{"tag":"text","text":"介绍下","style":[]},{"tag":"text","text":"飞书 CLI","style":[]}]]}`,
			expected: "介绍下飞书 CLI",
		},
		{
			name: "扁平结构带标题",
			content: `{"title":"公告","content":[[{"tag":"text","text":"明天放假"}]]}`,
			expected: "公告\n\n明天放假",
		},
		{
			name: "无效JSON",
			content:  `not json`,
			expected: "",
		},
		{
			name:     "空内容",
			content:  `{"zh_cn": {"content": []}}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPostText(tt.content)
			if got != tt.expected {
				t.Errorf("extractPostText() =\n%q\nwant:\n%q", got, tt.expected)
			}
		})
	}
}

func TestRenderPostElement(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		m        map[string]any
		expected string
	}{
		{"text", "text", map[string]any{"text": "hello"}, "hello"},
		{"link", "a", map[string]any{"text": "click", "href": "https://example.com"}, "[click](https://example.com)"},
		{"link without href", "a", map[string]any{"text": "click"}, "click"},
		{"at user", "at", map[string]any{"user_id": "ou_xxx", "user_name": "张三"}, "@张三"},
		{"at all", "at", map[string]any{"user_id": "all"}, "@所有人"},
		{"at no name", "at", map[string]any{"user_id": "ou_xxx"}, "@ou_xxx"},
		{"emotion", "emotion", map[string]any{"emoji_type": "SMILE"}, "[SMILE]"},
		{"img skip", "img", map[string]any{"image_key": "xxx"}, ""},
		{"media skip", "media", map[string]any{"file_key": "xxx"}, ""},
		{"hr", "hr", map[string]any{}, "\n---\n"},
		{"unknown with text", "unknown", map[string]any{"text": "fallback"}, "fallback"},
		{"unknown without text", "unknown", map[string]any{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderPostElement(tt.tag, tt.m)
			if got != tt.expected {
				t.Errorf("renderPostElement(%q) = %q, want %q", tt.tag, got, tt.expected)
			}
		})
	}
}
