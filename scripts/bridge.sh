#!/usr/bin/env bash
# lark-agent-bridge: Listen for Feishu bot messages and forward to AI agent
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LIB_DIR="$SCRIPT_DIR/lib"

# Load config
if [[ -f "$PROJECT_DIR/.env" ]]; then
    source "$PROJECT_DIR/.env"
fi

# Load modules (order matters: config first, then dependencies before dependents)
source "$LIB_DIR/config.sh"    # 环境变量、日志、cleanup
source "$LIB_DIR/feishu.sh"    # 飞书 API（被 session/agent/queue/commands 依赖）
source "$LIB_DIR/task.sh"      # 显式任务状态机
source "$LIB_DIR/session.sh"   # 会话管理（被 agent/commands 依赖）
source "$LIB_DIR/agent.sh"     # Agent 调用（被 queue 依赖）
source "$LIB_DIR/queue.sh"     # 消息队列和处理
source "$LIB_DIR/commands.sh"  # 斜杠命令

# Main loop: subscribe to bot message events
main() {
    local latest_change
    latest_change=$(project_latest_change 2>/dev/null || true)

    log "=== lark-agent-bridge started ==="
    log "Workspace: $(pwd)"
    log "Agent type: $AGENT_TYPE"
    log "Working emoji: $WORKING_EMOJI"
    if [[ -n "$latest_change" ]]; then
        log "Latest project change: $latest_change"
    fi
    log "Listening for Feishu bot messages..."

    lark-cli event +subscribe \
        --event-types "im.message.receive_v1" \
        --as bot 2>/dev/null | while IFS= read -r event; do

        # Debug: log raw event
        log "Raw event: $(echo "$event" | head -c 500)"

        # Parse event fields (try multiple possible paths)
        chat_id=$(echo "$event" | jq -r '.event.message.chat_id // .chat_id // .message.chat_id // empty' 2>/dev/null)
        message_id=$(echo "$event" | jq -r '.event.message.message_id // .message_id // .message.message_id // empty' 2>/dev/null)
        msg_type=$(echo "$event" | jq -r '.event.message.message_type // .message_type // .message.message_type // empty' 2>/dev/null)
        content_raw=$(echo "$event" | jq -r '.event.message.content // .content // .message.content // empty' 2>/dev/null)

        # Only handle text messages
        if [[ "$msg_type" != "text" ]]; then
            log "Skipping non-text message (type: $msg_type)"
            continue
        fi

        # Extract text from content JSON
        prompt=$(echo "$content_raw" | jq -r '.text // empty' 2>/dev/null)

        if [[ -z "$prompt" || -z "$chat_id" || -z "$message_id" ]]; then
            log "Skipping: empty prompt, chat_id, or message_id"
            continue
        fi

        log "Received: $prompt (chat: $chat_id, msg: $message_id)"

        # Escape prefix: // → strip first / and send to agent directly
        if [[ "$prompt" == //* ]]; then
            prompt="${prompt:1}"
            log "Escaped command prefix: $prompt"
            enqueue_message "$prompt" "$chat_id" "$message_id"
            continue
        fi

        # Handle commands; if not a command, enqueue for agent processing
        if ! handle_command "$prompt" "$chat_id" "$message_id"; then
            enqueue_message "$prompt" "$chat_id" "$message_id"
        fi

    done

    log "=== lark-agent-bridge stopped ==="
}

main "$@"
