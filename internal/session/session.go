package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LJW0401/lark-agent-bridge/internal/config"
)

// Manager 管理每个 chat 的会话状态
type Manager struct {
	cfg *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// GetSessionID 获取 chat 的会话 ID，如果超时或不存在则返回空字符串
func (m *Manager) GetSessionID(chatID string) string {
	path := filepath.Join(m.cfg.SessionDir, chatID)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	parts := strings.Fields(string(data))
	if len(parts) < 2 {
		return ""
	}

	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return ""
	}

	if m.cfg.Session.Timeout > 0 {
		elapsed := time.Now().Unix() - ts
		if elapsed >= int64(m.cfg.Session.Timeout) {
			return ""
		}
	}

	return parts[1]
}

// SaveSessionID 保存 chat 的会话 ID
func (m *Manager) SaveSessionID(chatID, sessionID string) error {
	path := filepath.Join(m.cfg.SessionDir, chatID)
	content := fmt.Sprintf("%d %s", time.Now().Unix(), sessionID)
	return os.WriteFile(path, []byte(content), 0644)
}

// ClearSession 清除 chat 的会话
func (m *Manager) ClearSession(chatID string) {
	os.Remove(filepath.Join(m.cfg.SessionDir, chatID))
}

// GetWorkspace 获取 chat 的工作目录
func (m *Manager) GetWorkspace(chatID string) string {
	path := filepath.Join(m.cfg.SessionDir, chatID+".workspace")
	data, err := os.ReadFile(path)
	if err != nil {
		return m.cfg.Workspace.Dir
	}
	ws := strings.TrimSpace(string(data))
	if ws == "" {
		return m.cfg.Workspace.Dir
	}
	return ws
}

// SetWorkspace 设置 chat 的工作目录
func (m *Manager) SetWorkspace(chatID, dir string) error {
	path := filepath.Join(m.cfg.SessionDir, chatID+".workspace")
	return os.WriteFile(path, []byte(dir), 0644)
}

// GetAgentType 获取 chat 的 Agent 类型
func (m *Manager) GetAgentType(chatID string) string {
	path := filepath.Join(m.cfg.SessionDir, chatID+".agent_type")
	data, err := os.ReadFile(path)
	if err != nil {
		return m.cfg.Agent.Type
	}
	at := strings.TrimSpace(string(data))
	if at == "" {
		return m.cfg.Agent.Type
	}
	return at
}

// SetAgentType 设置 chat 的 Agent 类型
func (m *Manager) SetAgentType(chatID, agentType string) error {
	path := filepath.Join(m.cfg.SessionDir, chatID+".agent_type")
	return os.WriteFile(path, []byte(agentType), 0644)
}
