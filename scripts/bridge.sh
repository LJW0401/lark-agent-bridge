#!/usr/bin/env bash
# lark-agent-bridge: Listen for Feishu bot messages and forward to AI agent
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Load config
if [[ -f "$PROJECT_DIR/.env" ]]; then
    source "$PROJECT_DIR/.env"
fi

AGENT_TYPE="${AGENT_TYPE:-codex}"
CODEX_CMD="${CODEX_CMD:-codex}"
CLAUDE_CMD="${CLAUDE_CMD:-claude}"
WORKING_EMOJI="${WORKING_EMOJI:-OnIt}"
ERROR_EMOJI="${ERROR_EMOJI:-Frown}"
MAX_RETRIES="${MAX_RETRIES:-3}"
SESSION_TIMEOUT="${SESSION_TIMEOUT:-0}"
LOG_FILE="${LOG_FILE:-$PROJECT_DIR/logs/bridge.log}"
SESSION_DIR="${PROJECT_DIR}/.sessions"

mkdir -p "$(dirname "$LOG_FILE")" "$SESSION_DIR"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

# Add emoji reaction to a message, returns reaction_id (with retry)
add_reaction() {
    local message_id="$1"
    local emoji_type="$2"
    local attempt=0

    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli im reactions create \
            --params "{\"message_id\":\"$message_id\"}" \
            --data "{\"reaction_type\":{\"emoji_type\":\"$emoji_type\"}}" \
            --as bot 2>&1) || true

        local rid
        rid=$(echo "$response" | jq -r '.data.reaction_id // empty' 2>/dev/null)

        if [[ -n "$rid" ]]; then
            echo "$rid"
            return 0
        fi

        attempt=$((attempt + 1))
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Add reaction failed (attempt $attempt/$MAX_RETRIES)" >> "$LOG_FILE"
        sleep 2
    done
}

# Remove emoji reaction from a message (with retry)
remove_reaction() {
    local message_id="$1"
    local reaction_id="$2"
    local attempt=0

    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli im reactions delete \
            --params "{\"message_id\":\"$message_id\",\"reaction_id\":\"$reaction_id\"}" \
            --as bot 2>&1)

        if echo "$response" | jq -e '.code == 0' &>/dev/null; then
            return 0
        fi

        attempt=$((attempt + 1))
        log "Remove reaction failed (attempt $attempt/$MAX_RETRIES)"
        sleep 2
    done

    log "Failed to remove reaction after $MAX_RETRIES attempts"
}

# Get or create session for a chat
get_session_id() {
    local chat_id="$1"
    local session_file="$SESSION_DIR/$chat_id"

    if [[ -f "$session_file" ]]; then
        local timestamp session_id
        timestamp=$(cut -d' ' -f1 "$session_file")
        session_id=$(cut -d' ' -f2 "$session_file")
        local now
        now=$(date +%s)

        # Check if session is still valid (0 = no timeout)
        if (( SESSION_TIMEOUT == 0 || now - timestamp < SESSION_TIMEOUT )) && [[ -n "$session_id" ]]; then
            echo "$session_id"
            return 0
        fi
    fi
    return 1
}

# Save session id for a chat
save_session_id() {
    local chat_id="$1"
    local session_id="$2"
    echo "$(date +%s) $session_id" > "$SESSION_DIR/$chat_id"
}

# Clear session for a chat
clear_session() {
    local chat_id="$1"
    rm -f "$SESSION_DIR/$chat_id"
}

# Call AI agent with the given prompt
call_agent() {
    local prompt="$1"
    local chat_id="$2"
    local result=""

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Calling $AGENT_TYPE..." >> "$LOG_FILE"

    case "$AGENT_TYPE" in
        codex)
            local tmpfile
            tmpfile=$(mktemp /tmp/codex_out.XXXXXX)

            local session_id
            session_id=$(get_session_id "$chat_id") || true

            local jsonfile
            jsonfile=$(mktemp /tmp/codex_json.XXXXXX)

            if [[ -n "$session_id" ]]; then
                # Resume existing session
                echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resuming codex session: $session_id" >> "$LOG_FILE"
                $CODEX_CMD exec resume "$session_id" "$prompt" -o "$tmpfile" --json > "$jsonfile" 2>/dev/null || true
            else
                # New session
                $CODEX_CMD exec "$prompt" -o "$tmpfile" --json > "$jsonfile" 2>/dev/null || true
            fi

            result=$(cat "$tmpfile" 2>/dev/null)

            # Extract session id (thread_id) from JSONL output
            local new_session_id
            new_session_id=$(grep -o '"thread_id":"[^"]*"' "$jsonfile" 2>/dev/null | head -1 | cut -d'"' -f4 || true)

            if [[ -n "$new_session_id" ]]; then
                save_session_id "$chat_id" "$new_session_id"
                echo "[$(date '+%Y-%m-%d %H:%M:%S')] Saved session: $new_session_id for chat: $chat_id" >> "$LOG_FILE"
            fi

            rm -f "$tmpfile" "$jsonfile"
            ;;
        claude)
            local session_id
            session_id=$(get_session_id "$chat_id") || true

            if [[ -n "$session_id" ]]; then
                result=$($CLAUDE_CMD -p "$prompt" --resume "$session_id" 2>/dev/null) || true
            else
                result=$($CLAUDE_CMD -p "$prompt" 2>/dev/null) || true
            fi
            ;;
        *)
            result="Unknown agent type: $AGENT_TYPE"
            ;;
    esac

    echo "$result"
}

# Reply to a Feishu chat (with retry)
reply_to_feishu() {
    local chat_id="$1"
    local message="$2"

    # Truncate if too long (Feishu message limit)
    if [[ ${#message} -gt 4000 ]]; then
        message="${message:0:3997}..."
    fi

    local attempt=0
    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli im +messages-send \
            --chat-id "$chat_id" \
            --text "$message" \
            --as bot 2>&1)

        if echo "$response" | jq -e '.ok == true' &>/dev/null; then
            return 0
        fi

        attempt=$((attempt + 1))
        log "Reply failed (attempt $attempt/$MAX_RETRIES): $(echo "$response" | head -c 200)"
        sleep 2
    done

    log "Reply failed after $MAX_RETRIES attempts"
    return 1
}

# Send error feedback to Feishu
reply_error_to_feishu() {
    local chat_id="$1"
    local message_id="$2"
    local error_msg="$3"

    # Add error emoji
    add_reaction "$message_id" "$ERROR_EMOJI" >/dev/null

    # Send error message
    reply_to_feishu "$chat_id" "[错误] $error_msg" || true
}

# Main loop: subscribe to bot message events
main() {
    log "=== lark-agent-bridge started ==="
    log "Workspace: $(pwd)"
    log "Agent type: $AGENT_TYPE"
    log "Working emoji: $WORKING_EMOJI"
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

        # Handle special commands
        case "$prompt" in
            /help|帮助)
                reply_to_feishu "$chat_id" "$(cat <<'HELP'
📋 可用命令：
/help    — 显示此帮助信息
/status  — 查看当前会话状态
/agent   — 查看当前 Agent 类型
/agent codex|claude — 切换 Agent 类型
/new     — 清除上下文，开始新对话
新对话    — 同 /new
HELP
)" || true
                log "Command: /help"
                continue
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
                reply_to_feishu "$chat_id" "$(printf '📊 当前状态：\nAgent 类型: %s\n会话状态: %b\n超时设置: %s' \
                    "$AGENT_TYPE" "$status_session" "$status_timeout")" || true
                log "Command: /status"
                continue
                ;;
            /agent)
                reply_to_feishu "$chat_id" "$(printf '当前 Agent 类型: %s（可用: codex, claude）\n用法: /agent codex 或 /agent claude' "$AGENT_TYPE")" || true
                log "Command: /agent (query)"
                continue
                ;;
            "/agent codex"|"/agent claude")
                local new_agent="${prompt##/agent }"
                if [[ "$new_agent" == "$AGENT_TYPE" ]]; then
                    reply_to_feishu "$chat_id" "当前已经是 $AGENT_TYPE，无需切换。" || true
                else
                    AGENT_TYPE="$new_agent"
                    clear_session "$chat_id"
                    reply_to_feishu "$chat_id" "已切换到 $AGENT_TYPE，会话上下文已清除。" || true
                    log "Agent switched to $AGENT_TYPE by chat $chat_id"
                fi
                continue
                ;;
            /new|新对话)
                clear_session "$chat_id"
                reply_to_feishu "$chat_id" "已开始新对话，之前的上下文已清除。" || true
                log "Session cleared for $chat_id"
                continue
                ;;
            /*)
                reply_to_feishu "$chat_id" "$(printf '未知命令: %s\n输入 /help 查看可用命令列表。' "$prompt")" || true
                log "Unknown command: $prompt"
                continue
                ;;
        esac

        # Step 1: Add emoji reaction (working indicator)
        reaction_id=$(add_reaction "$message_id" "$WORKING_EMOJI")
        log "Added reaction: $reaction_id"

        # Step 2: Call AI agent (with session context)
        result=$(call_agent "$prompt" "$chat_id")
        log "Agent response: ${result:0:200}..."

        # Step 3: Check result and reply
        if [[ -z "$result" ]]; then
            log "Error: Agent returned empty result"
            reply_error_to_feishu "$chat_id" "$message_id" "Agent 未返回任何结果，请稍后重试"
        elif ! reply_to_feishu "$chat_id" "$result"; then
            log "Error: Failed to send reply to Feishu"
            reply_error_to_feishu "$chat_id" "$message_id" "回复发送失败，请稍后重试"
        else
            log "Reply sent to $chat_id"
        fi

        # Step 4: Remove working emoji (task complete)
        if [[ -n "$reaction_id" ]]; then
            remove_reaction "$message_id" "$reaction_id"
            log "Removed reaction: task complete"
        fi

    done

    log "=== lark-agent-bridge stopped ==="
}

main "$@"
