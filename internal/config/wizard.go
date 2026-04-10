package config

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// initStep 引导向导的每一步
type initStep struct {
	Key      string
	Prompt   string
	Default  string
	Validate func(string) error
	Options  []string // 非空时显示选项列表
}

// RunInitWizard 运行交互式配置向导
func RunInitWizard(configPath string) error {
	fmt.Println("=== lark-agent-bridge 配置向导 ===")
	fmt.Println()

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("配置文件已存在: %s\n", configPath)
		answer := prompt("是否覆盖？(y/N)", "N")
		if !strings.EqualFold(answer, "y") {
			fmt.Println("已取消。")
			return nil
		}
		fmt.Println()
	}

	// 自动探测命令路径
	larkCliDefault := detectCmd("lark-cli")
	codexDefault := detectCmd("codex")
	claudeDefault := detectCmd("claude")

	steps := []initStep{
		// --- 基础配置 ---
		{
			Key:     "feishu.lark_cli_cmd",
			Prompt:  "lark-cli 命令路径",
			Default: larkCliDefault,
			Validate: func(s string) error {
				if _, err := exec.LookPath(s); err != nil {
					if _, err := os.Stat(s); err != nil {
						return fmt.Errorf("找不到命令: %s", s)
					}
				}
				return nil
			},
		},
		{
			Key:     "agent.type",
			Prompt:  "选择 AI Agent 类型",
			Default: "codex",
			Options: []string{"codex", "claude"},
			Validate: func(s string) error {
				if s != "codex" && s != "claude" {
					return fmt.Errorf("只能选择 codex 或 claude")
				}
				return nil
			},
		},
		{
			Key:     "agent.codex_cmd",
			Prompt:  "Codex CLI 命令路径",
			Default: codexDefault,
		},
		{
			Key:     "agent.claude_cmd",
			Prompt:  "Claude Code 命令路径",
			Default: claudeDefault,
		},
		{
			Key:    "workspace.dir",
			Prompt: "默认工作目录",
			Default: ".",
			Validate: func(s string) error {
				if s == "." {
					return nil
				}
				info, err := os.Stat(s)
				if err != nil {
					return fmt.Errorf("目录不存在: %s", s)
				}
				if !info.IsDir() {
					return fmt.Errorf("不是目录: %s", s)
				}
				return nil
			},
		},
	}

	advancedSteps := []initStep{
		{
			Key:     "feishu.working_emoji",
			Prompt:  "处理中表情",
			Default: "OnIt",
		},
		{
			Key:     "feishu.error_emoji",
			Prompt:  "错误表情",
			Default: "Frown",
		},
		{
			Key:     "stream.interval",
			Prompt:  "流式更新间隔（秒）",
			Default: "3",
		},
		{
			Key:     "stream.message_limit",
			Prompt:  "单条消息字符上限",
			Default: "4000",
		},
		{
			Key:     "session.timeout",
			Prompt:  "会话超时（秒，0=永不超时）",
			Default: "0",
		},
		{
			Key:     "log.file",
			Prompt:  "日志文件路径",
			Default: "./logs/bridge.log",
		},
		{
			Key:     "retry.max_retries",
			Prompt:  "飞书 API 最大重试次数",
			Default: "3",
		},
	}

	data := make(map[string]any)

	// 基础配置
	fmt.Println("── 基础配置 ──")
	fmt.Println()
	if err := runSteps(steps, data); err != nil {
		return err
	}

	// 高级配置（可选）
	fmt.Println("── 高级配置 ──")
	answer := prompt("是否配置高级选项？(y/N)", "N")
	fmt.Println()

	if strings.EqualFold(answer, "y") {
		if err := runSteps(advancedSteps, data); err != nil {
			return err
		}
	} else {
		// 使用默认值
		for _, step := range advancedSteps {
			setInMap(data, step.Key, autoType(step.Default))
		}
	}

	if err := saveRawYAML(configPath, data); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	fmt.Printf("配置已保存到: %s\n", configPath)
	fmt.Println()
	fmt.Println("接下来你可以：")
	fmt.Println("  运行服务:   lark-agent-bridge")
	fmt.Println("  修改配置:   lark-agent-bridge config set <key> <value>")
	fmt.Println("  查看配置:   lark-agent-bridge config list")
	fmt.Println("  安装服务:   lark-agent-bridge install")
	return nil
}

func runSteps(steps []initStep, data map[string]any) error {
	for _, step := range steps {
		fmt.Printf("  %s\n", step.Prompt)
		if len(step.Options) > 0 {
			fmt.Printf("  可选: %s\n", strings.Join(step.Options, " | "))
		}

		var value string
		for {
			value = prompt(fmt.Sprintf("  [%s]", step.Default), step.Default)
			if step.Validate != nil {
				if err := step.Validate(value); err != nil {
					fmt.Printf("  错误: %v\n", err)
					continue
				}
			}
			break
		}

		setInMap(data, step.Key, autoType(value))
		fmt.Println()
	}
	return nil
}

// detectCmd 探测命令的绝对路径，找不到则返回命令名本身
func detectCmd(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return name
	}
	return path
}

func prompt(hint, defaultVal string) string {
	fmt.Printf("  %s: ", hint)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			return input
		}
	}
	return defaultVal
}
