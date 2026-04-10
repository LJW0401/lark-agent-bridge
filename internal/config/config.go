package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Agent   AgentConfig   `yaml:"agent"`
	Feishu  FeishuConfig  `yaml:"feishu"`
	Stream  StreamConfig  `yaml:"stream"`
	Session SessionConfig `yaml:"session"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Log     LogConfig     `yaml:"log"`
	Retry   RetryConfig   `yaml:"retry"`

	// 运行时路径（非配置文件字段）
	ProjectDir string `yaml:"-"`
	SessionDir string `yaml:"-"`
	PIDDir     string `yaml:"-"`
	QueueDir   string `yaml:"-"`
	TaskDir    string `yaml:"-"`
}

type AgentConfig struct {
	Type     string `yaml:"type"`
	CodexCmd string `yaml:"codex_cmd"`
	ClaudeCmd string `yaml:"claude_cmd"`
}

type FeishuConfig struct {
	EventTypes   []string `yaml:"event_types"`
	WorkingEmoji string   `yaml:"working_emoji"`
	ErrorEmoji   string   `yaml:"error_emoji"`
}

type StreamConfig struct {
	Interval     int `yaml:"interval"`
	MessageLimit int `yaml:"message_limit"`
}

type SessionConfig struct {
	Timeout int `yaml:"timeout"`
}

type WorkspaceConfig struct {
	Dir string `yaml:"dir"`
}

type LogConfig struct {
	File string `yaml:"file"`
}

type RetryConfig struct {
	MaxRetries int `yaml:"max_retries"`
}

// Load 从指定路径加载配置文件，并设置默认值
func Load(path string) (*Config, error) {
	cfg := &Config{}
	cfg.setDefaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("配置文件 %s 不存在，使用默认配置", path)
			cfg.initDirs()
			return cfg, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	cfg.initDirs()
	return cfg, nil
}

func (c *Config) setDefaults() {
	c.Agent.Type = "codex"
	c.Agent.CodexCmd = "codex"
	c.Agent.ClaudeCmd = "claude"
	c.Feishu.EventTypes = []string{"im.message.receive_v1"}
	c.Feishu.WorkingEmoji = "OnIt"
	c.Feishu.ErrorEmoji = "Frown"
	c.Stream.Interval = 3
	c.Stream.MessageLimit = 4000
	c.Session.Timeout = 0
	c.Workspace.Dir = "."
	c.Log.File = "./logs/bridge.log"
	c.Retry.MaxRetries = 3
}

func (c *Config) initDirs() {
	if c.ProjectDir == "" {
		dir, err := os.Getwd()
		if err != nil {
			c.ProjectDir = "."
		} else {
			c.ProjectDir = dir
		}
	}

	c.SessionDir = filepath.Join(c.ProjectDir, ".sessions")
	c.PIDDir = filepath.Join(c.ProjectDir, ".pids")
	c.QueueDir = filepath.Join(c.ProjectDir, ".queue")
	c.TaskDir = filepath.Join(c.ProjectDir, ".tasks")

	// 解析日志文件为绝对路径
	if !filepath.IsAbs(c.Log.File) {
		c.Log.File = filepath.Join(c.ProjectDir, c.Log.File)
	}

	// 解析工作目录为绝对路径
	if !filepath.IsAbs(c.Workspace.Dir) {
		c.Workspace.Dir = filepath.Join(c.ProjectDir, c.Workspace.Dir)
	}

	// 创建必要目录
	dirs := []string{
		filepath.Dir(c.Log.File),
		c.SessionDir,
		c.PIDDir,
		c.QueueDir,
		c.TaskDir,
	}
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
	}
}

// CleanStaleState 清理上次运行遗留的状态文件
func (c *Config) CleanStaleState() {
	removeGlob(filepath.Join(c.QueueDir, "*.depth"))
	removeGlob(filepath.Join(c.PIDDir, "*"))
	removeGlob(filepath.Join(c.TaskDir, "*.current"))
}

func removeGlob(pattern string) {
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		os.Remove(m)
	}
}
