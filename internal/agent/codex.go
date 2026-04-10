package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/LJW0401/lark-agent-bridge/internal/config"
)

var threadIDRegex = regexp.MustCompile(`"thread_id":"([^"]*)"`)

// Codex 实现 Agent 接口，封装 Codex CLI 调用
type Codex struct {
	cmd    string
	logger *config.Logger
}

func NewCodex(cmd string, logger *config.Logger) *Codex {
	return &Codex{cmd: cmd, logger: logger}
}

func (c *Codex) Run(ctx context.Context, prompt, workspace, sessionID string, output io.Writer) (*Result, error) {
	result, err := c.run(ctx, prompt, workspace, sessionID, output)
	// resume 失败时回退到新会话
	if sessionID != "" && (err != nil || (result != nil && result.Output == "")) {
		c.logger.Log("Codex resume 失败，启动新会话")
		return c.run(ctx, prompt, workspace, "", output)
	}
	return result, err
}

func (c *Codex) run(ctx context.Context, prompt, workspace, sessionID string, output io.Writer) (*Result, error) {
	args := []string{"exec", "--skip-git-repo-check"}
	if sessionID != "" {
		args = append(args, "resume", sessionID, prompt)
	} else {
		args = append(args, prompt)
	}
	args = append(args, "--json")

	cmd := exec.CommandContext(ctx, c.cmd, args...)
	cmd.Dir = workspace
	cmd.Stdin = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 codex 失败: %w", err)
	}

	result := &Result{}
	var outputParts []string

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()

		var evt map[string]any
		if json.Unmarshal([]byte(line), &evt) != nil {
			continue
		}

		evtType, _ := evt["type"].(string)
		switch evtType {
		case "thread.started":
			// 提取 thread_id 作为 session_id
			if matches := threadIDRegex.FindStringSubmatch(line); len(matches) > 1 {
				result.SessionID = matches[1]
			}
		case "item.completed":
			if item, ok := evt["item"].(map[string]any); ok {
				if text, ok := item["text"].(string); ok && text != "" {
					outputParts = append(outputParts, text)
					fmt.Fprintln(output, text)
				}
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("codex 执行失败: %w", err)
	}

	result.Output = strings.Join(outputParts, "\n")
	return result, nil
}

func (c *Codex) NewSession(workspace string) (string, error) {
	args := []string{"exec", "--skip-git-repo-check", "你好", "--json"}

	cmd := exec.Command(c.cmd, args...)
	cmd.Dir = workspace
	cmd.Stdin = nil

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("创建 codex 新会话失败: %w", err)
	}

	if matches := threadIDRegex.FindStringSubmatch(string(out)); len(matches) > 1 {
		return matches[1], nil
	}
	return "", fmt.Errorf("未能从 codex 输出中提取 thread_id")
}
