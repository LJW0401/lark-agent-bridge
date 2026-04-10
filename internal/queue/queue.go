package queue

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LJW0401/lark-agent-bridge/internal/agent"
	"github.com/LJW0401/lark-agent-bridge/internal/config"
	"github.com/LJW0401/lark-agent-bridge/internal/feishu"
	"github.com/LJW0401/lark-agent-bridge/internal/session"
	"github.com/LJW0401/lark-agent-bridge/internal/task"
)

// Processor 消息队列处理器
type Processor struct {
	cfg     *config.Config
	logger  *config.Logger
	feishu  *feishu.Client
	session *session.Manager
	tasks   *task.Manager
	agents  map[string]agent.Agent

	// per-chat 互斥锁，替代 flock
	// 注意：条目不会主动清理，但 chat 数量通常有限，不会无限增长
	chatLocks sync.Map // map[chatID]*sync.Mutex
	// per-chat 队列深度
	chatDepth sync.Map // map[chatID]*int32
}

func NewProcessor(
	cfg *config.Config,
	logger *config.Logger,
	fc *feishu.Client,
	sm *session.Manager,
	tm *task.Manager,
	agents map[string]agent.Agent,
) *Processor {
	return &Processor{
		cfg:     cfg,
		logger:  logger,
		feishu:  fc,
		session: sm,
		tasks:   tm,
		agents:  agents,
	}
}

func (p *Processor) getChatLock(chatID string) *sync.Mutex {
	v, _ := p.chatLocks.LoadOrStore(chatID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (p *Processor) incrDepth(chatID string) int {
	v, _ := p.chatDepth.LoadOrStore(chatID, new(int32))
	ptr := v.(*int32)
	old := atomic.AddInt32(ptr, 1) - 1
	return int(old)
}

func (p *Processor) decrDepth(chatID string) {
	v, ok := p.chatDepth.Load(chatID)
	if !ok {
		return
	}
	ptr := v.(*int32)
	if atomic.AddInt32(ptr, -1) < 0 {
		atomic.StoreInt32(ptr, 0)
	}
}

// Enqueue 将消息放入队列，per-chat 串行、跨 chat 并行
func (p *Processor) Enqueue(prompt, chatID, messageID string) {
	taskID, err := p.tasks.Create(chatID, messageID)
	if err != nil {
		p.logger.Log("创建任务失败: %v", err)
		return
	}

	depth := p.incrDepth(chatID)
	isQueued := depth > 0

	go func() {
		defer p.logger.Recover("queue-processor")
		mu := p.getChatLock(chatID)

		// 排队时显示等待表情
		var queuedReactionID string
		if isQueued {
			queuedReactionID, _ = p.feishu.AddReaction(messageID, "OneSecond")
			p.tasks.SetField(taskID, "note", "等待前序任务完成")
			p.logger.Log("消息已排队 chat=%s (depth: %d)", chatID, depth+1)
		}

		mu.Lock()
		defer mu.Unlock()
		defer p.decrDepth(chatID)

		// 移除排队表情
		if queuedReactionID != "" {
			p.feishu.RemoveReaction(messageID, queuedReactionID)
		}

		p.processMessage(prompt, chatID, messageID, taskID)
	}()
}

func (p *Processor) processMessage(prompt, chatID, messageID, taskID string) {
	p.tasks.SetCurrent(chatID, taskID)
	p.tasks.Transition(taskID, task.StateStarting, "开始处理消息")

	// 获取 Agent
	agentType := p.session.GetAgentType(chatID)
	ag, ok := p.agents[agentType]
	if !ok {
		p.tasks.Transition(taskID, task.StateFailed, "未知的 Agent 类型: "+agentType)
		p.feishu.ReplyError(chatID, messageID, "未知的 Agent 类型: "+agentType)
		p.tasks.ClearCurrent(chatID, taskID)
		return
	}

	workspace := p.session.GetWorkspace(chatID)
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		p.tasks.Transition(taskID, task.StateFailed, "工作目录不存在: "+workspace)
		p.feishu.ReplyError(chatID, messageID, "工作目录不存在: "+workspace)
		p.tasks.ClearCurrent(chatID, taskID)
		return
	}

	sessionID := p.session.GetSessionID(chatID)

	// 记录 Agent 启动信息
	sessionDisplay := sessionID
	if len(sessionDisplay) > 16 {
		sessionDisplay = sessionDisplay[:16] + "..."
	}
	if sessionDisplay == "" {
		sessionDisplay = "(新会话)"
	}
	promptDisplay := prompt
	if len([]rune(promptDisplay)) > 100 {
		promptDisplay = string([]rune(promptDisplay)[:100]) + "..."
	}
	p.logger.Log("Agent 启动: type=%s, workspace=%s, session=%s, prompt=%s",
		agentType, workspace, sessionDisplay, promptDisplay)

	// 添加工作表情 + 创建占位回复
	reactionID, _ := p.feishu.AddReaction(messageID, p.cfg.Feishu.WorkingEmoji)
	p.tasks.SetField(taskID, "reaction_id", reactionID)

	replyMsgID, _ := p.feishu.ReplyMessage(messageID, "⏳ 正在处理...", false)
	p.tasks.SetField(taskID, "reply_message_id", replyMsgID)

	// 启动 Agent（带 cancel 支持）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 存储 cancel 函数供 /cancel 命令使用
	p.storeCancelFunc(chatID, taskID, cancel)
	defer p.clearCancelFunc(chatID, taskID)

	p.tasks.Transition(taskID, task.StateRunning, "Agent 正在处理")

	// 流式输出缓冲（startTime 在后面流式轮询中也用到）
	startTime := time.Now()
	var outputBuf bytes.Buffer
	resultCh := make(chan *agent.Result, 1)
	errCh := make(chan error, 1)

	go func() {
		result, err := ag.Run(ctx, prompt, workspace, sessionID, &outputBuf)
		if err != nil {
			errCh <- err
			return
		}
		if result == nil {
			errCh <- fmt.Errorf("Agent 返回了空结果")
			return
		}
		resultCh <- result
	}()

	// 流式轮询更新
	ticker := time.NewTicker(time.Duration(p.cfg.Stream.Interval) * time.Second)
	defer ticker.Stop()
	lastContent := ""

	for {
		select {
		case result := <-resultCh:
			p.logger.Log("Agent 耗时: %.1f 秒", time.Since(startTime).Seconds())
			p.handleResult(result, chatID, messageID, taskID, reactionID, replyMsgID)
			return
		case err := <-errCh:
			elapsed := time.Since(startTime).Seconds()
			if ctx.Err() != nil {
				// 被取消（Agent 已收到 SIGINT 优雅退出）
				// 清除会话：即使优雅退出，服务端会话也可能处于不可恢复的中间状态
				p.session.ClearSession(chatID)
				p.logger.Log("Agent 取消: 耗时 %.1f 秒, 已清除会话", elapsed)
				p.tasks.Transition(taskID, task.StateCancelled, "任务已取消")
				if replyMsgID != "" {
					p.feishu.UpdateMessage(replyMsgID, "[已取消] 请求已被用户中断。", false)
				}
			} else {
				p.logger.Log("Agent 失败: 耗时 %.1f 秒, 错误: %v", elapsed, err)
				p.tasks.Transition(taskID, task.StateFailed, fmt.Sprintf("Agent 执行失败: %v", err))
				if replyMsgID != "" {
					p.feishu.UpdateMessage(replyMsgID, "[错误] Agent 执行失败，请稍后重试", false)
				}
				p.feishu.ReplyError(chatID, messageID, fmt.Sprintf("Agent 执行失败: %v", err))
			}
			p.cleanupTask(chatID, messageID, taskID, reactionID)
			return
		case <-ctx.Done():
			// 取消后立即响应，清除会话避免下次 resume 失败
			p.session.ClearSession(chatID)
			p.logger.Log("Agent 取消: 已清除会话, 下次将启动新会话")
			p.tasks.Transition(taskID, task.StateCancelled, "任务已取消")
			if replyMsgID != "" {
				p.feishu.UpdateMessage(replyMsgID, "[已取消] 请求已被用户中断。", false)
			}
			p.cleanupTask(chatID, messageID, taskID, reactionID)
			return
		case <-ticker.C:
			current := outputBuf.String()
			if current != "" && current != lastContent && replyMsgID != "" {
				truncated := feishu.TruncateMessage(current, p.cfg.Stream.MessageLimit)
				p.feishu.UpdateMessage(replyMsgID, truncated, false)
				lastContent = current
			} else if current == "" && replyMsgID != "" {
				elapsed := int(time.Since(startTime).Seconds())
				p.feishu.UpdateMessage(replyMsgID, fmt.Sprintf("⏳ 正在处理...（已等待 %d 秒）", elapsed), false)
			}
		}
	}
}

func (p *Processor) handleResult(result *agent.Result, chatID, messageID, taskID, reactionID, replyMsgID string) {
	outputLen := len([]rune(result.Output))
	sessionDisplay := result.SessionID
	if len(sessionDisplay) > 16 {
		sessionDisplay = sessionDisplay[:16] + "..."
	}
	p.logger.Log("Agent 完成: output=%d 字符, session=%s", outputLen, sessionDisplay)

	// 保存 session ID
	if result.SessionID != "" {
		p.session.SaveSessionID(chatID, result.SessionID)
	}

	if result.Output == "" {
		p.tasks.Transition(taskID, task.StateFailed, "Agent 未返回任何结果")
		if replyMsgID != "" {
			p.feishu.UpdateMessage(replyMsgID, "[错误] Agent 未返回任何结果，请稍后重试", false)
		}
		p.feishu.ReplyError(chatID, messageID, "Agent 未返回任何结果，请稍后重试")
	} else if replyMsgID != "" {
		p.tasks.Transition(taskID, task.StateCompleted, "任务处理完成")
		chunks := feishu.ChunkMessage(result.Output, p.cfg.Stream.MessageLimit)
		// 第一片用 update 更新占位消息
		if err := p.feishu.UpdateMessage(replyMsgID, chunks[0], true); err != nil {
			// 更新失败，降级为普通发送
			p.feishu.SendMessage(chatID, result.Output, true)
		} else {
			// 后续分片用 reply 发送
			for i := 1; i < len(chunks); i++ {
				if _, err := p.feishu.ReplyMessage(messageID, chunks[i], true); err != nil {
					p.logger.Log("分片 %d/%d 发送失败: %v", i+1, len(chunks), err)
					p.feishu.SendMessage(chatID, "[错误] 长回复的后续分片发送失败", false)
					break
				}
			}
		}
		p.logger.Log("回复已发送 chat=%s chunks=%d", chatID, len(chunks))
	} else {
		// 占位回复创建失败，降级为普通消息
		p.tasks.Transition(taskID, task.StateCompleted, "占位回复创建失败，已降级为普通消息发送结果")
		if err := p.feishu.SendMessage(chatID, result.Output, true); err != nil {
			p.tasks.Transition(taskID, task.StateFailed, "回复发送失败")
			p.feishu.ReplyError(chatID, messageID, "回复发送失败，请稍后重试")
		}
	}

	p.cleanupTask(chatID, messageID, taskID, reactionID)
}

func (p *Processor) cleanupTask(chatID, messageID, taskID, reactionID string) {
	if reactionID != "" {
		p.feishu.RemoveReaction(messageID, reactionID)
		p.tasks.ClearField(taskID, "reaction_id")
	}
	p.tasks.ClearField(taskID, "agent_pid")
	p.tasks.ClearCurrent(chatID, taskID)
}

// --- Cancel 支持 ---

var (
	cancelFuncs sync.Map // map[chatID]cancelEntry
)

type cancelEntry struct {
	taskID string
	cancel context.CancelFunc
}

func (p *Processor) storeCancelFunc(chatID, taskID string, cancel context.CancelFunc) {
	cancelFuncs.Store(chatID, cancelEntry{taskID: taskID, cancel: cancel})
}

func (p *Processor) clearCancelFunc(chatID, taskID string) {
	if v, ok := cancelFuncs.Load(chatID); ok {
		if e, ok := v.(cancelEntry); ok && e.taskID == taskID {
			cancelFuncs.Delete(chatID)
		}
	}
}

// CancelAgent 取消 chat 当前正在运行的 Agent
func (p *Processor) CancelAgent(chatID string) bool {
	v, ok := cancelFuncs.Load(chatID)
	if !ok {
		return false
	}
	e, ok := v.(cancelEntry)
	if !ok {
		return false
	}

	p.tasks.Transition(e.taskID, task.StateCancelling, "用户请求取消")
	e.cancel()
	return true
}
