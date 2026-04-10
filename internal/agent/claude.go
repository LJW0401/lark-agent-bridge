package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/LJW0401/lark-agent-bridge/internal/config"
)

// Claude 实现 Agent 接口，封装 Claude Code CLI 调用
type Claude struct {
	cmd    string
	logger *config.Logger
}

func NewClaude(cmd string, logger *config.Logger) *Claude {
	return &Claude{cmd: cmd, logger: logger}
}

func (c *Claude) Run(ctx context.Context, prompt, workspace, sessionID string, output io.Writer) (*Result, error) {
	result, err := c.run(ctx, prompt, workspace, sessionID, output)
	// resume 失败时回退到新会话
	if sessionID != "" && (err != nil || (result != nil && result.Output == "")) {
		c.logger.Log("Claude resume 失败，启动新会话")
		return c.run(ctx, prompt, workspace, "", output)
	}
	return result, err
}

func (c *Claude) run(ctx context.Context, prompt, workspace, sessionID string, output io.Writer) (*Result, error) {
	args := []string{"-p", prompt, "--output-format", "json"}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.CommandContext(ctx, c.cmd, args...)
	cmd.Dir = workspace
	cmd.Stdin = nil
	// 取消时发 SIGINT 让 Agent 优雅退出并保存会话，而非 SIGKILL 暴力杀死
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 10 * time.Second

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("claude 执行失败: %w", err)
	}

	var resp struct {
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("解析 claude 输出失败: %w", err)
	}

	if resp.Result != "" {
		fmt.Fprint(output, resp.Result)
	}

	return &Result{
		Output:    resp.Result,
		SessionID: resp.SessionID,
	}, nil
}

func (c *Claude) NewSession(workspace string) (string, error) {
	cmd := exec.Command(c.cmd, "-p", "你好", "--output-format", "json")
	cmd.Dir = workspace
	cmd.Stdin = nil

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("创建 claude 新会话失败: %w", err)
	}

	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("解析 claude 输出失败: %w", err)
	}

	if resp.SessionID == "" {
		return "", fmt.Errorf("未能从 claude 输出中提取 session_id")
	}
	return resp.SessionID, nil
}
