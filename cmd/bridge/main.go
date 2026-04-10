package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/LJW0401/lark-agent-bridge/internal/agent"
	"github.com/LJW0401/lark-agent-bridge/internal/commands"
	"github.com/LJW0401/lark-agent-bridge/internal/config"
	"github.com/LJW0401/lark-agent-bridge/internal/feishu"
	"github.com/LJW0401/lark-agent-bridge/internal/queue"
	"github.com/LJW0401/lark-agent-bridge/internal/session"
	"github.com/LJW0401/lark-agent-bridge/internal/task"
	"github.com/LJW0401/lark-agent-bridge/platform"
)

var version = "dev"

func main() {
	// 如果作为 Windows Service 运行，走 Service 入口
	if platform.IsWindowsService() {
		if err := platform.RunAsService(bridgeMain); err != nil {
			fmt.Fprintf(os.Stderr, "服务运行失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 检查是否有子命令
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install", "uninstall", "start", "stop", "status", "logs":
			ensureRoot()
			handleServiceCommand(os.Args[1])
			return
		case "config":
			handleConfigCommand()
			return
		case "version":
			fmt.Printf("lark-agent-bridge %s\n", version)
			return
		case "help":
			printUsage()
			return
		}
	}

	// 正常前台运行
	stopCh := make(chan struct{})
	bridgeMain(stopCh)
}

// bridgeMain 核心业务逻辑，stop channel 关闭时退出
func bridgeMain(stop <-chan struct{}) {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	if *showVersion {
		fmt.Printf("lark-agent-bridge %s\n", version)
		return
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

	for {
		select {
		case <-stop:
			logger.Log("=== lark-agent-bridge stopped (service stop) ===")
			return
		case <-sigCh:
			logger.Log("=== lark-agent-bridge stopped (signal) ===")
			return
		case ev, ok := <-events:
			if !ok {
				logger.Log("=== lark-agent-bridge stopped (event channel closed) ===")
				return
			}

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
	}
}

// handleServiceCommand 处理服务管理子命令
func handleServiceCommand(cmd string) {
	svc := platform.NewService()

	switch cmd {
	case "install":
		exePath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "获取可执行文件路径失败: %v\n", err)
			os.Exit(1)
		}
		exePath, _ = filepath.Abs(exePath)

		// 配置文件路径：优先同目录下的 config.yaml
		configPath := filepath.Join(filepath.Dir(exePath), "config.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			// 回退到当前工作目录
			cwd, _ := os.Getwd()
			configPath = filepath.Join(cwd, "config.yaml")
		}

		workDir := filepath.Dir(configPath)

		if err := svc.Install(platform.ServiceConfig{
			ExePath:    exePath,
			ConfigPath: configPath,
			WorkDir:    workDir,
			Description: platform.ServiceDescription,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "安装失败: %v\n", err)
			os.Exit(1)
		}

	case "uninstall":
		if err := svc.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "卸载失败: %v\n", err)
			os.Exit(1)
		}

	case "start":
		if err := svc.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
			os.Exit(1)
		}

	case "stop":
		if err := svc.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "停止失败: %v\n", err)
			os.Exit(1)
		}

	case "status":
		status, err := svc.Status()
		if err != nil {
			fmt.Fprintf(os.Stderr, "查询状态失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("服务状态: %s\n", status)

	case "logs":
		follow := false
		for _, arg := range os.Args[2:] {
			if arg == "-f" || arg == "--follow" {
				follow = true
			}
		}
		if err := svc.Logs(follow); err != nil {
			fmt.Fprintf(os.Stderr, "查看日志失败: %v\n", err)
			os.Exit(1)
		}
	}
}

// handleConfigCommand 处理 config 子命令
func handleConfigCommand() {
	configPath := config.GetConfigPath()

	if len(os.Args) < 3 {
		fmt.Println("用法:")
		fmt.Println("  lark-agent-bridge config init            交互式配置向导")
		fmt.Println("  lark-agent-bridge config list            列出所有配置项")
		fmt.Println("  lark-agent-bridge config get <key>       读取配置项")
		fmt.Println("  lark-agent-bridge config set <key> <val> 设置配置项")
		fmt.Println("  lark-agent-bridge config path            显示配置文件路径")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "init":
		if err := config.RunInitWizard(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "配置向导失败: %v\n", err)
			os.Exit(1)
		}

	case "list":
		fmt.Printf("配置文件: %s\n\n", configPath)
		fmt.Print(config.List(configPath))

	case "get":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "用法: lark-agent-bridge config get <key>")
			os.Exit(1)
		}
		val, err := config.Get(configPath, os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println(val)

	case "set":
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "用法: lark-agent-bridge config set <key> <value>")
			os.Exit(1)
		}
		if err := config.Set(configPath, os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s = %s\n", os.Args[3], os.Args[4])

	case "path":
		fmt.Println(configPath)

	default:
		fmt.Fprintf(os.Stderr, "未知的 config 子命令: %s\n", os.Args[2])
		os.Exit(1)
	}
}

func ensureRoot() {
	platform.EnsureRoot()
}

func printUsage() {
	fmt.Printf("lark-agent-bridge %s\n\n", version)
	fmt.Println("飞书 AI Agent 消息桥接服务")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  lark-agent-bridge [--config <path>]   前台运行服务")
	fmt.Println()
	fmt.Println("配置管理:")
	fmt.Println("  config init            交互式配置向导")
	fmt.Println("  config list            列出所有配置项")
	fmt.Println("  config get <key>       读取配置项")
	fmt.Println("  config set <key> <val> 设置配置项")
	fmt.Println("  config path            显示配置文件路径")
	fmt.Println()
	fmt.Println("服务管理:")
	fmt.Println("  install                安装为系统服务")
	fmt.Println("  uninstall              卸载系统服务")
	fmt.Println("  start                  启动服务")
	fmt.Println("  stop                   停止服务")
	fmt.Println("  status                 查看服务状态")
	fmt.Println("  logs [-f]              查看服务日志")
	fmt.Println()
	fmt.Println("其他:")
	fmt.Println("  version                显示版本信息")
	fmt.Println("  help                   显示此帮助信息")
}
