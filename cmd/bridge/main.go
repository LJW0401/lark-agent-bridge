package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/LJW0401/lark-agent-bridge/internal/agent"
	"github.com/LJW0401/lark-agent-bridge/internal/commands"
	"github.com/LJW0401/lark-agent-bridge/internal/config"
	"github.com/LJW0401/lark-agent-bridge/internal/feishu"
	"github.com/LJW0401/lark-agent-bridge/internal/queue"
	"github.com/LJW0401/lark-agent-bridge/internal/session"
	"github.com/LJW0401/lark-agent-bridge/internal/task"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lark-agent-bridge %s\n", version)
		os.Exit(0)
	}

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	logger, err := config.NewLogger(cfg.Log.File)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	// 清理上次遗留状态
	cfg.CleanStaleState()

	// 初始化各模块
	fc := feishu.NewClient(cfg, logger)
	sm := session.NewManager(cfg)
	tm := task.NewManager(cfg, logger)

	agents := map[string]agent.Agent{
		"codex":  agent.NewCodex(cfg.Agent.CodexCmd, logger),
		"claude": agent.NewClaude(cfg.Agent.ClaudeCmd, logger),
	}

	qp := queue.NewProcessor(cfg, logger, fc, sm, tm, agents)
	ch := commands.NewHandler(cfg, logger, fc, sm, qp, agents)

	// 启动事件订阅
	events := make(chan feishu.Event, 100)
	if err := fc.Subscribe(events); err != nil {
		logger.Log("启动事件订阅失败: %v", err)
		os.Exit(1)
	}

	logger.Log("=== lark-agent-bridge started ===")
	logger.Log("Version: %s", version)
	logger.Log("Workspace: %s", cfg.Workspace.Dir)
	logger.Log("Agent type: %s", cfg.Agent.Type)
	logger.Log("Listening for Feishu bot messages...")

	// 信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Log("=== lark-agent-bridge stopping ===")
		os.Exit(0)
	}()

	// 主循环
	for ev := range events {
		logger.Log("收到消息: %s (chat: %s, msg: %s)", ev.Text, ev.ChatID, ev.MessageID)

		prompt := ev.Text

		// 转义前缀: // → 去掉第一个 / 直接发给 Agent
		if strings.HasPrefix(prompt, "//") {
			prompt = prompt[1:]
			logger.Log("转义命令前缀: %s", prompt)
			qp.Enqueue(prompt, ev.ChatID, ev.MessageID)
			continue
		}

		// 命令处理；非命令则入队
		if !ch.Handle(prompt, ev.ChatID, ev.MessageID) {
			qp.Enqueue(prompt, ev.ChatID, ev.MessageID)
		}
	}

	logger.Log("=== lark-agent-bridge stopped ===")
}
