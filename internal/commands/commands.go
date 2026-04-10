package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/LJW0401/lark-agent-bridge/internal/agent"
	"github.com/LJW0401/lark-agent-bridge/internal/config"
	"github.com/LJW0401/lark-agent-bridge/internal/feishu"
	"github.com/LJW0401/lark-agent-bridge/internal/queue"
	"github.com/LJW0401/lark-agent-bridge/internal/session"
)

// Handler 斜杠命令处理器
type Handler struct {
	cfg     *config.Config
	logger  *config.Logger
	feishu  *feishu.Client
	session *session.Manager
	queue   *queue.Processor
	agents  map[string]agent.Agent
}

func NewHandler(
	cfg *config.Config,
	logger *config.Logger,
	fc *feishu.Client,
	sm *session.Manager,
	qp *queue.Processor,
	agents map[string]agent.Agent,
) *Handler {
	return &Handler{
		cfg:     cfg,
		logger:  logger,
		feishu:  fc,
		session: sm,
		queue:   qp,
		agents:  agents,
	}
}

// Handle 处理命令，返回 true 表示已处理，false 表示不是命令
func (h *Handler) Handle(prompt, chatID, messageID string) bool {
	switch {
	case prompt == "/help" || prompt == "帮助":
		return h.handleHelp(chatID)
	case prompt == "/status":
		return h.handleStatus(chatID)
	case prompt == "/agent":
		return h.handleAgentQuery(chatID)
	case prompt == "/agent codex" || prompt == "/agent claude":
		return h.handleAgentSwitch(prompt, chatID)
	case prompt == "/workspace":
		return h.handleWorkspaceQuery(chatID)
	case strings.HasPrefix(prompt, "/workspace "):
		return h.handleWorkspaceSet(prompt, chatID)
	case prompt == "/cancel":
		return h.handleCancel(chatID)
	case prompt == "/new" || prompt == "新对话":
		return h.handleNew(chatID)
	case strings.HasPrefix(prompt, "/"):
		h.feishu.SendMessage(chatID, fmt.Sprintf("未知命令: %s\n输入 /help 查看可用命令列表。", prompt), false)
		h.logger.Log("未知命令: %s", prompt)
		return true
	}
	return false
}

func (h *Handler) handleHelp(chatID string) bool {
	help := `可用命令：
/help       -- 显示此帮助信息
/status     -- 查看当前会话状态
/agent      -- 查看当前 Agent 类型
/agent codex|claude -- 切换 Agent 类型
/workspace  -- 查看当前工作目录
/workspace <path> -- 切换工作目录
/cancel     -- 取消正在进行的请求
/new        -- 清除上下文，开始新对话
新对话       -- 同 /new

以 // 开头可将 / 命令发给 Agent，如 //help`

	h.feishu.SendMessage(chatID, help, false)
	h.logger.Log("命令: /help")
	return true
}

func (h *Handler) handleStatus(chatID string) bool {
	sessionID := h.session.GetSessionID(chatID)
	var statusSession string
	if sessionID != "" {
		// 近似计算会话持续时间
		statusSession = fmt.Sprintf("活跃\n会话 ID: %s...", sessionID[:min(16, len(sessionID))])
	} else {
		statusSession = "无活跃会话"
	}

	var statusTimeout string
	if h.cfg.Session.Timeout == 0 {
		statusTimeout = "永不超时"
	} else {
		statusTimeout = fmt.Sprintf("%d 秒", h.cfg.Session.Timeout)
	}

	workspace := h.session.GetWorkspace(chatID)
	currentAgent := h.session.GetAgentType(chatID)

	msg := fmt.Sprintf("当前状态：\nAgent 类型: %s\n工作目录: %s\n会话状态: %s\n超时设置: %s",
		currentAgent, workspace, statusSession, statusTimeout)

	h.feishu.SendMessage(chatID, msg, false)
	h.logger.Log("命令: /status")
	return true
}

func (h *Handler) handleAgentQuery(chatID string) bool {
	current := h.session.GetAgentType(chatID)
	h.feishu.SendMessage(chatID,
		fmt.Sprintf("当前 Agent 类型: %s（可用: codex, claude）\n用法: /agent codex 或 /agent claude", current), false)
	h.logger.Log("命令: /agent (query)")
	return true
}

func (h *Handler) handleAgentSwitch(prompt, chatID string) bool {
	newAgent := strings.TrimPrefix(prompt, "/agent ")
	current := h.session.GetAgentType(chatID)
	if newAgent == current {
		h.feishu.SendMessage(chatID, fmt.Sprintf("当前已经是 %s，无需切换。", current), false)
	} else {
		h.session.SetAgentType(chatID, newAgent)
		h.session.ClearSession(chatID)
		h.feishu.SendMessage(chatID, fmt.Sprintf("已切换到 %s，会话上下文已清除。", newAgent), false)
		h.logger.Log("Agent 切换为 %s (chat: %s)", newAgent, chatID)
	}
	return true
}

func (h *Handler) handleWorkspaceQuery(chatID string) bool {
	workspace := h.session.GetWorkspace(chatID)
	h.feishu.SendMessage(chatID,
		fmt.Sprintf("当前工作目录: %s\n用法: /workspace <path>", workspace), false)
	h.logger.Log("命令: /workspace (query)")
	return true
}

func (h *Handler) handleWorkspaceSet(prompt, chatID string) bool {
	newWorkspace := strings.TrimPrefix(prompt, "/workspace ")
	info, err := os.Stat(newWorkspace)
	if err != nil || !info.IsDir() {
		h.feishu.SendMessage(chatID, "目录不存在: "+newWorkspace, false)
		h.logger.Log("无效工作目录: %s", newWorkspace)
	} else {
		h.session.SetWorkspace(chatID, newWorkspace)
		h.feishu.SendMessage(chatID, "工作目录已切换到: "+newWorkspace, false)
		h.logger.Log("工作目录切换为 %s (chat: %s)", newWorkspace, chatID)
	}
	return true
}

func (h *Handler) handleCancel(chatID string) bool {
	if h.queue.CancelAgent(chatID) {
		h.feishu.SendMessage(chatID, "已取消当前请求。", false)
		h.logger.Log("Agent 已取消 (chat: %s)", chatID)
	} else {
		h.feishu.SendMessage(chatID, "当前没有正在进行的请求。", false)
	}
	return true
}

func (h *Handler) handleNew(chatID string) bool {
	h.logger.Log("命令: /new - 创建新会话 (chat: %s)", chatID)

	agentType := h.session.GetAgentType(chatID)
	ag, ok := h.agents[agentType]
	if !ok {
		h.feishu.SendMessage(chatID, "创建新会话失败：未知的 Agent 类型", false)
		return true
	}

	workspace := h.session.GetWorkspace(chatID)
	newSID, err := ag.NewSession(workspace)
	if err != nil {
		h.feishu.SendMessage(chatID, "创建新会话失败，请稍后重试。", false)
		h.logger.Log("创建新会话失败: %v", err)
		return true
	}

	h.session.SaveSessionID(chatID, newSID)
	display := newSID
	if len(display) > 16 {
		display = display[:16] + "..."
	}
	h.feishu.SendMessage(chatID, fmt.Sprintf("已创建新会话（ID: %s）", display), false)
	h.logger.Log("新会话已创建 chat=%s sid=%s", chatID, newSID)
	return true
}

