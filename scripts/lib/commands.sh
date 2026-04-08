#!/usr/bin/env bash
# commands.sh — 斜杠命令处理

# Handle slash commands. Returns 0 if command was handled, 1 if not a command.
handle_command() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"

    case "$prompt" in
        /help|帮助)
            send_to_feishu "$chat_id" "$(cat <<'HELP'
📋 可用命令：
/help       — 显示此帮助信息
/status     — 查看当前会话状态
/agent      — 查看当前 Agent 类型
/agent codex|claude — 切换 Agent 类型
/workspace  — 查看当前工作目录
/workspace <path> — 切换工作目录
/cancel     — 取消正在进行的请求
/new        — 清除上下文，开始新对话
新对话       — 同 /new
HELP
)" || true
            log "Command: /help"
            return 0
            ;;
        /status)
            local session_id status_session status_timeout
            session_id=$(get_session_id "$chat_id") || true
            if [[ -n "$session_id" ]]; then
                local session_file="$SESSION_DIR/$chat_id"
                local ts
                ts=$(cut -d' ' -f1 "$session_file")
                local elapsed=$(( $(date +%s) - ts ))
                status_session="活跃（已持续 ${elapsed} 秒）\n会话 ID: ${session_id:0:16}..."
            else
                status_session="无活跃会话"
            fi
            if (( SESSION_TIMEOUT == 0 )); then
                status_timeout="永不超时"
            else
                status_timeout="${SESSION_TIMEOUT} 秒"
            fi
            local workspace
            workspace=$(get_workspace "$chat_id")
            send_to_feishu "$chat_id" "$(printf '📊 当前状态：\nAgent 类型: %s\n工作目录: %s\n会话状态: %b\n超时设置: %s' \
                "$AGENT_TYPE" "$workspace" "$status_session" "$status_timeout")" || true
            log "Command: /status"
            return 0
            ;;
        /agent)
            send_to_feishu "$chat_id" "$(printf '当前 Agent 类型: %s（可用: codex, claude）\n用法: /agent codex 或 /agent claude' "$AGENT_TYPE")" || true
            log "Command: /agent (query)"
            return 0
            ;;
        "/agent codex"|"/agent claude")
            local new_agent="${prompt##/agent }"
            if [[ "$new_agent" == "$AGENT_TYPE" ]]; then
                send_to_feishu "$chat_id" "当前已经是 $AGENT_TYPE，无需切换。" || true
            else
                AGENT_TYPE="$new_agent"
                clear_session "$chat_id"
                send_to_feishu "$chat_id" "已切换到 $AGENT_TYPE，会话上���文已清除。" || true
                log "Agent switched to $AGENT_TYPE by chat $chat_id"
            fi
            return 0
            ;;
        /workspace)
            local workspace
            workspace=$(get_workspace "$chat_id")
            send_to_feishu "$chat_id" "$(printf '当前工作目录: %s\n用法: /workspace <path>' "$workspace")" || true
            log "Command: /workspace (query)"
            return 0
            ;;
        /workspace\ *)
            local new_workspace="${prompt##/workspace }"
            if [[ -d "$new_workspace" ]]; then
                set_workspace "$chat_id" "$new_workspace"
                send_to_feishu "$chat_id" "工作目录已切换到: $new_workspace" || true
                log "Workspace set to $new_workspace for chat $chat_id"
            else
                send_to_feishu "$chat_id" "目录不存在: $new_workspace" || true
                log "Invalid workspace: $new_workspace"
            fi
            return 0
            ;;
        /cancel)
            if cancel_agent "$chat_id"; then
                send_to_feishu "$chat_id" "已取消当前请求。" || true
                log "Agent cancelled for chat $chat_id"
            else
                send_to_feishu "$chat_id" "���前没有正在进行的请求。" || true
            fi
            return 0
            ;;
        /new|新对话)
            log "Command: /new - creating new session for $chat_id"
            local new_sid
            new_sid=$(create_new_session "$chat_id") || true
            if [[ -n "$new_sid" ]]; then
                send_to_feishu "$chat_id" "✅ 已创建新会话（ID: ${new_sid:0:16}...）" || true
                log "New session created for $chat_id: $new_sid"
            else
                send_to_feishu "$chat_id" "❌ 创建新会话失��，请稍后重试。" || true
                log "Failed to create new session for $chat_id"
            fi
            return 0
            ;;
        /*)
            send_to_feishu "$chat_id" "$(printf '未知命令: %s\n输入 /help 查看可用命令列表。' "$prompt")" || true
            log "Unknown command: $prompt"
            return 0
            ;;
    esac

    return 1
}
