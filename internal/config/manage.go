package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// configField 描述一个可配置字段
type configField struct {
	Key          string // 点分路径，如 agent.type
	Description  string // 中文说明
	DefaultValue string // 默认值文本
}

// AllFields 返回所有可配置字段的描述
func AllFields() []configField {
	return []configField{
		{"agent.type", "AI Agent 类型 (codex | claude)", "codex"},
		{"agent.codex_cmd", "Codex CLI 命令路径", "codex"},
		{"agent.claude_cmd", "Claude Code 命令路径", "claude"},
		{"feishu.lark_cli_cmd", "lark-cli 命令路径", "lark-cli"},
		{"feishu.working_emoji", "处理中表情", "OnIt"},
		{"feishu.error_emoji", "错误表情", "Frown"},
		{"stream.interval", "流式更新间隔（秒）", "3"},
		{"stream.message_limit", "单条消息字符上限", "4000"},
		{"session.timeout", "会话超时（秒），0=永不超时", "0"},
		{"workspace.dir", "默认工作目录", "."},
		{"log.file", "日志文件路径", "./logs/bridge.log"},
		{"retry.max_retries", "飞书 API 最大重试次数", "3"},
	}
}

// Get 从配置文件中读取指定字段的值
func Get(configPath, key string) (string, error) {
	data, err := loadRawYAML(configPath)
	if err != nil {
		return "", err
	}

	val := getFromMap(data, key)
	if val == "" {
		// 返回默认值
		for _, f := range AllFields() {
			if f.Key == key {
				return f.DefaultValue, nil
			}
		}
		return "", fmt.Errorf("未知的配置项: %s", key)
	}
	return val, nil
}

// Set 设置配置文件中的指定字段
func Set(configPath, key, value string) error {
	// 验证 key 是否合法
	valid := false
	for _, f := range AllFields() {
		if f.Key == key {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("未知的配置项: %s\n使用 'config list' 查看所有可用配置", key)
	}

	data, _ := loadRawYAML(configPath)
	if data == nil {
		data = make(map[string]any)
	}

	setInMap(data, key, autoType(value))

	return saveRawYAML(configPath, data)
}

// List 列出所有配置项及当前值
func List(configPath string) string {
	data, _ := loadRawYAML(configPath)
	fields := AllFields()

	var sb strings.Builder
	for _, f := range fields {
		current := getFromMap(data, f.Key)
		if current == "" {
			current = f.DefaultValue
		}
		sb.WriteString(fmt.Sprintf("%-25s = %-15s  # %s\n", f.Key, current, f.Description))
	}
	return sb.String()
}

// GetConfigPath 返回配置文件的默认搜索路径
func GetConfigPath() string {
	// 优先当前目录
	if _, err := os.Stat("config.yaml"); err == nil {
		abs, _ := os.Getwd()
		return filepath.Join(abs, "config.yaml")
	}

	// 然后可执行文件同目录
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// 默认当前目录（将会创建）
	abs, _ := os.Getwd()
	return filepath.Join(abs, "config.yaml")
}

// --- 内部工具函数 ---

func loadRawYAML(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	return data, nil
}

func saveRawYAML(path string, data map[string]any) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	return os.WriteFile(path, out, 0644)
}

// getFromMap 通过点分路径从 map 中取值
func getFromMap(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	parts := strings.SplitN(key, ".", 2)
	val, ok := data[parts[0]]
	if !ok {
		return ""
	}
	if len(parts) == 1 {
		return fmt.Sprintf("%v", val)
	}
	sub, ok := val.(map[string]any)
	if !ok {
		return ""
	}
	return getFromMap(sub, parts[1])
}

// setInMap 通过点分路径在 map 中设值
func setInMap(data map[string]any, key string, value any) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 1 {
		data[parts[0]] = value
		return
	}
	sub, ok := data[parts[0]].(map[string]any)
	if !ok {
		sub = make(map[string]any)
		data[parts[0]] = sub
	}
	setInMap(sub, parts[1], value)
}

// autoType 自动推断值类型（int / bool / string）
func autoType(s string) any {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}

