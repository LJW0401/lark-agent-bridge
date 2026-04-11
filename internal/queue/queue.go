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

	// 添加工作表情
	reactionID, _ := p.feishu.AddReaction(messageID, p.cfg.Feishu.WorkingEmoji)
	p.tasks.SetField(taskID, "reaction_id", reactionID)

	// 发送状态消息（处理时间信息）
	statusMsgID, _ := p.feishu.ReplyMessage(messageID, "⏳ 正在处理...", false)

	// 创建流式卡片（Agent 输出结果，降级则复用状态消息）
	card, cardErr := p.feishu.ReplyStreamingCard(messageID, "")
	var replyMsgID string
	if cardErr != nil {
		p.logger.Log("流式卡片创建失败，降级为普通消息: %v", cardErr)
		// 降级时状态消息兼作结果消息
		replyMsgID = statusMsgID
		statusMsgID = ""
	} else {
		replyMsgID = card.MessageID
	}
	p.tasks.SetField(taskID, "reply_message_id", replyMsgID)

	// 启动 Agent（带 cancel 支持）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.storeCancelFunc(chatID, taskID, cancel)
	defer p.clearCancelFunc(chatID, taskID)

	p.tasks.Transition(taskID, task.StateRunning, "Agent 正在处理")

	// 流式输出缓冲
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

	// updateStatus 更新状态消息
	updateStatus := func(text string) {
		if statusMsgID != "" {
			p.feishu.UpdateMessage(statusMsgID, text, false)
		}
	}

	for {
		select {
		case result := <-resultCh:
			elapsed := time.Since(startTime).Seconds()
			p.logger.Log("Agent 耗时: %.1f 秒", elapsed)
			// 最终结果写入卡片
			if card != nil && result.Output != "" {
				p.feishu.UpdateStreamingContent(card, result.Output)
			}
			// 状态消息更新为完成
			updateStatus(fmt.Sprintf("✅ 处理完成（耗时 %.1f 秒）", elapsed))
			p.handleResult(result, chatID, messageID, taskID, reactionID, replyMsgID, card)
			return
		case err := <-errCh:
			elapsed := time.Since(startTime).Seconds()
			if ctx.Err() != nil {
				p.session.ClearSession(chatID)
				p.logger.Log("Agent 取消: 耗时 %.1f 秒, 已清除会话", elapsed)
				p.tasks.Transition(taskID, task.StateCancelled, "任务已取消")
				updateStatus(fmt.Sprintf("🚫 已取消（耗时 %.1f 秒）", elapsed))
				if card != nil {
					p.feishu.UpdateStreamingContent(card, "[已取消] 请求已被用户中断。")
				} else if replyMsgID != "" {
					p.feishu.UpdateMessage(replyMsgID, "[已取消] 请求已被用户中断。", false)
				}
			} else {
				p.logger.Log("Agent 失败: 耗时 %.1f 秒, 错误: %v", elapsed, err)
				p.tasks.Transition(taskID, task.StateFailed, fmt.Sprintf("Agent 执行失败: %v", err))
				updateStatus(fmt.Sprintf("❌ 执行失败（耗时 %.1f 秒）", elapsed))
				if card != nil {
					p.feishu.UpdateStreamingContent(card, "[错误] Agent 执行失败，请稍后重试")
				} else if replyMsgID != "" {
					p.feishu.UpdateMessage(replyMsgID, "[错误] Agent 执行失败，请稍后重试", false)
				}
				p.feishu.ReplyError(chatID, messageID, fmt.Sprintf("Agent 执行失败: %v", err))
			}
			p.cleanupTask(chatID, messageID, taskID, reactionID, false)
			return
		case <-ctx.Done():
			elapsed := time.Since(startTime).Seconds()
			p.session.ClearSession(chatID)
			p.logger.Log("Agent 取消: 已清除会话, 下次将启动新会话")
			p.tasks.Transition(taskID, task.StateCancelled, "任务已取消")
			updateStatus(fmt.Sprintf("🚫 已取消（耗时 %.1f 秒）", elapsed))
			if card != nil {
				p.feishu.UpdateStreamingContent(card, "[已取消] 请求已被用户中断。")
			} else if replyMsgID != "" {
				p.feishu.UpdateMessage(replyMsgID, "[已取消] 请求已被用户中断。", false)
			}
			p.cleanupTask(chatID, messageID, taskID, reactionID, false)
			return
		case <-ticker.C:
			current := outputBuf.String()
			elapsed := int(time.Since(startTime).Seconds())
			// 更新状态消息（处理时间）
			updateStatus(fmt.Sprintf("⏳ 正在处理...（%d 秒）", elapsed))
			// 更新结果卡片（纯 Agent 输出）
			if card != nil {
				if current != "" && current != lastContent {
					p.feishu.UpdateStreamingContent(card, current)
					lastContent = current
				}
			} else if replyMsgID != "" {
				// 降级：普通消息更新（状态和内容合一）
				if current != "" {
					progress := fmt.Sprintf("\n\n⏳ 正在处理...（%d 秒）", elapsed)
					display := feishu.TruncateMessage(current, p.cfg.Stream.MessageLimit-len([]rune(progress)))
					p.feishu.UpdateMessage(replyMsgID, display+progress, false)
				}
			}
		}
	}
}

func (p *Processor) handleResult(result *agent.Result, chatID, messageID, taskID, reactionID, replyMsgID string, card *feishu.StreamingCard) {
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
		if card != nil {
			p.feishu.UpdateStreamingContent(card, "[错误] Agent 未返回任何结果，请稍后重试")
		} else if replyMsgID != "" {
			p.feishu.UpdateMessage(replyMsgID, "[错误] Agent 未返回任何结果，请稍后重试", false)
		}
		p.feishu.ReplyError(chatID, messageID, "Agent 未返回任何结果，请稍后重试")
	} else if card != nil {
		// 流式卡片：最终内容已在 resultCh case 中写入
		p.tasks.Transition(taskID, task.StateCompleted, "任务处理完成")
		p.logger.Log("回复已发送(流式卡片) chat=%s", chatID)
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
		p.logger.Log("回复已发送(降级) chat=%s chunks=%d", chatID, len(chunks))
	} else {
		// 占位回复创建失败，降级为普通消息
		p.tasks.Transition(taskID, task.StateCompleted, "占位回复创建失败，已降级为普通消息发送结果")
		if err := p.feishu.SendMessage(chatID, result.Output, true); err != nil {
			p.tasks.Transition(taskID, task.StateFailed, "回复发送失败")
			p.feishu.ReplyError(chatID, messageID, "回复发送失败，请稍后重试")
		}
	}

	p.cleanupTask(chatID, messageID, taskID, reactionID, true)
}

func (p *Processor) cleanupTask(chatID, messageID, taskID, reactionID string, done bool) {
	if reactionID != "" {
		p.feishu.RemoveReaction(messageID, reactionID)
		p.tasks.ClearField(taskID, "reaction_id")
	}
	if done {
		p.feishu.AddReaction(messageID, p.cfg.Feishu.DoneEmoji)
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
