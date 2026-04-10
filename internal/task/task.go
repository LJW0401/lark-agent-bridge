package task

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LJW0401/lark-agent-bridge/internal/config"
)

// State 任务状态
type State string

const (
	StateQueued     State = "queued"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateCancelling State = "cancelling"
	StateCancelled  State = "cancelled"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
)

// Task 任务元数据
type Task struct {
	TaskID         string `json:"task_id"`
	ChatID         string `json:"chat_id"`
	MessageID      string `json:"message_id"`
	State          State  `json:"state"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	Note           string `json:"note,omitempty"`
	ReactionID     string `json:"reaction_id,omitempty"`
	ReplyMessageID string `json:"reply_message_id,omitempty"`
	AgentPID       int    `json:"agent_pid,omitempty"`
}

// Manager 任务管理器
type Manager struct {
	cfg    *config.Config
	logger *config.Logger
	mu     sync.Mutex // 替代 flock，Go 原生互斥锁
}

func NewManager(cfg *config.Config, logger *config.Logger) *Manager {
	return &Manager{cfg: cfg, logger: logger}
}

// validTransitions 合法的状态转移
var validTransitions = map[string]bool{
	"queued:starting":     true,
	"queued:cancelling":   true,
	"queued:cancelled":    true,
	"queued:failed":       true,
	"starting:running":    true,
	"starting:cancelling": true,
	"starting:cancelled":  true,
	"starting:failed":     true,
	"running:cancelling":  true,
	"running:completed":   true,
	"running:failed":      true,
	"cancelling:cancelled": true,
	"cancelling:failed":   true,
	// 幂等转换
	"cancelled:cancelled": true,
	"completed:completed": true,
	"failed:failed":       true,
}

// Create 创建新任务
func (m *Manager) Create(chatID, messageID string) (string, error) {
	now := time.Now().Unix()
	taskID := fmt.Sprintf("%s-%d-%d-%d", chatID, now, os.Getpid(), rand.Intn(10000))

	t := &Task{
		TaskID:    taskID,
		ChatID:    chatID,
		MessageID: messageID,
		State:     StateQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.save(t); err != nil {
		return "", err
	}
	return taskID, nil
}

// Transition 执行状态转移
func (m *Manager) Transition(taskID string, newState State, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.load(taskID)
	if err != nil {
		return err
	}

	if t.State == newState {
		// 幂等，只更新时间和 note
		t.UpdatedAt = time.Now().Unix()
		if reason != "" {
			t.Note = reason
		}
		return m.save(t)
	}

	key := fmt.Sprintf("%s:%s", t.State, newState)
	if !validTransitions[key] {
		return fmt.Errorf("非法状态转移 %s: %s -> %s", taskID, t.State, newState)
	}

	t.State = newState
	t.UpdatedAt = time.Now().Unix()
	if reason != "" {
		t.Note = reason
	} else {
		t.Note = ""
	}

	return m.save(t)
}

// ReadField 读取任务字段
func (m *Manager) ReadField(taskID, field string) string {
	t, err := m.load(taskID)
	if err != nil {
		return ""
	}
	switch field {
	case "state":
		return string(t.State)
	case "note":
		return t.Note
	case "reaction_id":
		return t.ReactionID
	case "reply_message_id":
		return t.ReplyMessageID
	case "agent_pid":
		if t.AgentPID == 0 {
			return ""
		}
		return fmt.Sprintf("%d", t.AgentPID)
	case "updated_at":
		return fmt.Sprintf("%d", t.UpdatedAt)
	default:
		return ""
	}
}

// SetField 设置任务字段
func (m *Manager) SetField(taskID, field, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.load(taskID)
	if err != nil {
		return err
	}

	switch field {
	case "note":
		t.Note = value
	case "reaction_id":
		t.ReactionID = value
	case "reply_message_id":
		t.ReplyMessageID = value
	case "agent_pid":
		pid := 0
		fmt.Sscanf(value, "%d", &pid)
		t.AgentPID = pid
	case "queued_reaction_id":
		// 存入 note 中作为临时存储
		t.Note = "queued_reaction:" + value
	}

	t.UpdatedAt = time.Now().Unix()
	return m.save(t)
}

// ClearField 清除任务字段
func (m *Manager) ClearField(taskID, field string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.load(taskID)
	if err != nil {
		return err
	}

	switch field {
	case "note":
		t.Note = ""
	case "reaction_id":
		t.ReactionID = ""
	case "reply_message_id":
		t.ReplyMessageID = ""
	case "agent_pid":
		t.AgentPID = 0
	}

	t.UpdatedAt = time.Now().Unix()
	return m.save(t)
}

// SetCurrent 设置 chat 当前正在处理的任务
func (m *Manager) SetCurrent(chatID, taskID string) error {
	path := filepath.Join(m.cfg.TaskDir, chatID+".current")
	return os.WriteFile(path, []byte(taskID), 0644)
}

// GetCurrent 获取 chat 当前正在处理的任务
func (m *Manager) GetCurrent(chatID string) string {
	path := filepath.Join(m.cfg.TaskDir, chatID+".current")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ClearCurrent 清除 chat 的当前任务指针
func (m *Manager) ClearCurrent(chatID, taskID string) {
	path := filepath.Join(m.cfg.TaskDir, chatID+".current")
	if taskID != "" {
		current, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(current)) != taskID {
			return
		}
	}
	os.Remove(path)
}

// RuntimeSummary 生成任务运行时摘要
func (m *Manager) RuntimeSummary(chatID string) string {
	taskID := m.GetCurrent(chatID)
	if taskID == "" {
		return "当前任务: 无运行中任务"
	}

	t, err := m.load(taskID)
	if err != nil {
		return "当前任务: 无运行中任务"
	}

	summary := fmt.Sprintf("当前任务: %s\n任务状态: %s", t.TaskID, t.State)
	if t.UpdatedAt > 0 {
		summary += fmt.Sprintf("\n最近更新: %d", t.UpdatedAt)
	}
	if t.Note != "" {
		summary += fmt.Sprintf("\n状态说明: %s", t.Note)
	}
	return summary
}

func (m *Manager) taskFile(taskID string) string {
	return filepath.Join(m.cfg.TaskDir, taskID+".json")
}

func (m *Manager) load(taskID string) (*Task, error) {
	data, err := os.ReadFile(m.taskFile(taskID))
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s 失败: %w", taskID, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("解析任务 %s 失败: %w", taskID, err)
	}
	return &t, nil
}

func (m *Manager) save(t *Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化任务 %s 失败: %w", t.TaskID, err)
	}

	path := m.taskFile(t.TaskID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("写入任务 %s 失败: %w", t.TaskID, err)
	}
	return os.Rename(tmp, path)
}
