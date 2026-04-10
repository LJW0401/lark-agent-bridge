package agent

import (
	"context"
	"io"
)

// Result 表示 Agent 执行结果
type Result struct {
	Output    string // Agent 输出文本
	SessionID string // 会话 ID，用于下次 resume
	Error     string // 错误信息
}

// Agent 定义 AI Agent 的统一接口
type Agent interface {
	// Run 启动 Agent 处理 prompt，output 用于流式写入中间结果
	// sessionID 非空时尝试 resume 上次会话
	Run(ctx context.Context, prompt, workspace, sessionID string, output io.Writer) (*Result, error)

	// NewSession 创建新会话（用于 /new 命令），返回 session_id
	NewSession(workspace string) (string, error)
}
