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
CODEX_SKIP_CHECK="--skip-git-repo-check"
CLAUDE_CMD="${CLAUDE_CMD:-claude}"
WORKING_EMOJI="${WORKING_EMOJI:-OnIt}"
ERROR_EMOJI="${ERROR_EMOJI:-Frown}"
MAX_RETRIES="${MAX_RETRIES:-3}"
SESSION_TIMEOUT="${SESSION_TIMEOUT:-0}"
STREAM_INTERVAL="${STREAM_INTERVAL:-3}"
LOG_FILE="$(cd "$PROJECT_DIR" && realpath -m "${LOG_FILE:-./logs/bridge.log}")"
SESSION_DIR="${PROJECT_DIR}/.sessions"
WORKSPACE_DIR="${WORKSPACE_DIR:-$PROJECT_DIR}"
PID_DIR="${PROJECT_DIR}/.pids"
QUEUE_DIR="${PROJECT_DIR}/.queue"

mkdir -p "$(dirname "$LOG_FILE")" "$SESSION_DIR" "$PID_DIR" "$QUEUE_DIR"

# Reset stale state from previous run (depth counters, PID files)
rm -f "$QUEUE_DIR"/*.depth "$PID_DIR"/*

# Clean up all child processes on exit (runs only once)
cleanup() {
    trap - EXIT TERM INT  # Prevent re-entry from cascading signals
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Cleaning up child processes..." >> "$LOG_FILE"
    kill 0 2>/dev/null || true
}
trap cleanup EXIT TERM INT

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

# Get workspace directory for a chat
get_workspace() {
    local chat_id="$1"
    local ws_file="$SESSION_DIR/${chat_id}.workspace"
    if [[ -f "$ws_file" ]]; then
        cat "$ws_file"
    else
        echo "$WORKSPACE_DIR"
    fi
}

# Set workspace directory for a chat
set_workspace() {
    local chat_id="$1"
    local dir="$2"
    echo "$dir" > "$SESSION_DIR/${chat_id}.workspace"
}

# Save agent PID and reply message id for a chat (for /cancel)
save_agent_pid() {
    local chat_id="$1"
    local pid="$2"
    local reply_msg_id="${3:-}"
    echo "$pid $reply_msg_id" > "$PID_DIR/$chat_id"
}

# Clear agent PID for a chat
clear_agent_pid() {
    local chat_id="$1"
    rm -f "$PID_DIR/$chat_id"
}

# Cancel running agent for a chat
cancel_agent() {
    local chat_id="$1"
    local pid_file="$PID_DIR/$chat_id"
    if [[ -f "$pid_file" ]]; then
        local pid reply_msg_id
        pid=$(cut -d' ' -f1 "$pid_file")
        reply_msg_id=$(cut -d' ' -f2 "$pid_file")
        clear_agent_pid "$chat_id"
        # Kill the agent process
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill -- -"$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
        fi
        # Update the streaming reply to show cancellation
        if [[ -n "$reply_msg_id" ]]; then
            update_message "$reply_msg_id" "[已取消] 请求已被用户中断。"
        fi
        return 0
    fi
    return 1
}

# Create a new agent session for a chat (synchronous, used by /new)
create_new_session() {
    local chat_id="$1"
    local workspace
    workspace=$(get_workspace "$chat_id")

    case "$AGENT_TYPE" in
        codex)
            local tmpfile jsonfile
            tmpfile=$(mktemp /tmp/codex_out.XXXXXX)
            jsonfile=$(mktemp /tmp/codex_json.XXXXXX)

            (cd "$workspace" && $CODEX_CMD exec $CODEX_SKIP_CHECK "你好" -o "$tmpfile" --json > "$jsonfile") 2>>"$LOG_FILE" || true

            echo "[$(date '+%Y-%m-%d %H:%M:%S')] create_new_session: jsonfile=$(head -c 200 "$jsonfile" 2>/dev/null)" >> "$LOG_FILE"

            local new_session_id
            new_session_id=$(grep -o '"thread_id":"[^"]*"' "$jsonfile" 2>/dev/null | head -1 | cut -d'"' -f4 || true)

            echo "[$(date '+%Y-%m-%d %H:%M:%S')] create_new_session: thread_id=$new_session_id" >> "$LOG_FILE"

            rm -f "$tmpfile" "$jsonfile"

            if [[ -n "$new_session_id" ]]; then
                save_session_id "$chat_id" "$new_session_id"
                echo "$new_session_id"
                return 0
            fi
            return 1
            ;;
        claude)
            local tmpfile
            tmpfile=$(mktemp /tmp/claude_out.XXXXXX)

            (cd "$workspace" && $CLAUDE_CMD -p "你好" > "$tmpfile") 2>>"$LOG_FILE" || true

            local session_id
            session_id=$($CLAUDE_CMD --session-id 2>/dev/null || true)

            rm -f "$tmpfile"

            if [[ -n "$session_id" ]]; then
                save_session_id "$chat_id" "$session_id"
                echo "$session_id"
                return 0
            fi
            return 0
            ;;
    esac
}

# Start AI agent in background, writing output to tmpfile. Sets AGENT_PID.
start_agent() {
    local prompt="$1"
    local chat_id="$2"
    local outfile="$3"

    local workspace
    workspace=$(get_workspace "$chat_id")

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Calling $AGENT_TYPE in $workspace ..." >> "$LOG_FILE"

    case "$AGENT_TYPE" in
        codex)
            local session_id
            session_id=$(get_session_id "$chat_id") || true

            local jsonfile="${outfile}.json"

            (
                trap - EXIT TERM INT
                cd "$workspace"

                run_codex_json() {
                    # Run codex with --json, parse JSONL events in real-time:
                    # - Extract text from item.completed events → outfile (for streaming display)
                    # - Save thread_id from thread.started → jsonfile (for session tracking)
                    "$@" --json 2>/dev/null | while IFS= read -r line; do
                        local evt_type
                        evt_type=$(echo "$line" | jq -r '.type // empty' 2>/dev/null) || continue
                        case "$evt_type" in
                            thread.started)
                                echo "$line" >> "$jsonfile"
                                ;;
                            item.completed)
                                local text
                                text=$(echo "$line" | jq -r '.item.text // empty' 2>/dev/null) || true
                                if [[ -n "$text" ]]; then
                                    printf '%s\n' "$text" >> "$outfile"
                                fi
                                ;;
                        esac
                    done || true
                }

                if [[ -n "$session_id" ]]; then
                    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resuming codex session: $session_id" >> "$LOG_FILE"
                    run_codex_json $CODEX_CMD exec $CODEX_SKIP_CHECK resume "$session_id" "$prompt"
                    # Fallback to new session if resume produced empty output
                    if [[ ! -s "$outfile" ]]; then
                        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resume failed, starting new codex session" >> "$LOG_FILE"
                        rm -f "$outfile" "$jsonfile"
                        run_codex_json $CODEX_CMD exec $CODEX_SKIP_CHECK "$prompt"
                    fi
                else
                    run_codex_json $CODEX_CMD exec $CODEX_SKIP_CHECK "$prompt"
                fi
            ) &
            AGENT_PID=$!
            ;;
        claude)
            local session_id
            session_id=$(get_session_id "$chat_id") || true

            (
                trap - EXIT TERM INT
                cd "$workspace"
                if [[ -n "$session_id" ]]; then
                    stdbuf -oL $CLAUDE_CMD -p "$prompt" --resume "$session_id" > "$outfile" 2>/dev/null || true
                    # Fallback to new session if resume produced empty output
                    if [[ ! -s "$outfile" ]]; then
                        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resume failed, starting new claude session" >> "$LOG_FILE"
                        rm -f "$outfile"
                        stdbuf -oL $CLAUDE_CMD -p "$prompt" > "$outfile" 2>/dev/null || true
                    fi
                else
                    stdbuf -oL $CLAUDE_CMD -p "$prompt" > "$outfile" 2>/dev/null || true
                fi
            ) &
            AGENT_PID=$!
            ;;
        *)
            echo "Unknown agent type: $AGENT_TYPE" > "$outfile"
            AGENT_PID=""
            ;;
    esac
}

# Save codex session id from JSON output
save_codex_session() {
    local chat_id="$1"
    local jsonfile="$2"
    if [[ -f "$jsonfile" ]]; then
        local new_session_id
        new_session_id=$(grep -o '"thread_id":"[^"]*"' "$jsonfile" 2>/dev/null | head -1 | cut -d'"' -f4 || true)
        if [[ -n "$new_session_id" ]]; then
            save_session_id "$chat_id" "$new_session_id"
            echo "[$(date '+%Y-%m-%d %H:%M:%S')] Saved session: $new_session_id for chat: $chat_id" >> "$LOG_FILE"
        fi
        rm -f "$jsonfile"
    fi
}

# Truncate message to Feishu limit
truncate_message() {
    local message="$1"
    if [[ ${#message} -gt 4000 ]]; then
        echo "${message:0:3997}..."
    else
        echo "$message"
    fi
}

# Reply to a Feishu message by quoting it (with retry), prints message_id on success
# Usage: reply_to_feishu <message_id> <text> [--markdown]
reply_to_feishu() {
    local message_id="$1"
    local message
    message=$(truncate_message "$2")
    local use_markdown="${3:-}"

    local msg_flag="--text"
    if [[ "$use_markdown" == "--markdown" ]]; then
        msg_flag="--markdown"
    fi

    local attempt=0
    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli im +messages-reply \
            --message-id "$message_id" \
            $msg_flag "$message" \
            --as bot 2>&1)

        local reply_msg_id
        reply_msg_id=$(echo "$response" | jq -r '.data.message_id // .message_id // empty' 2>/dev/null)
        if [[ -n "$reply_msg_id" ]]; then
            echo "$reply_msg_id"
            return 0
        fi

        attempt=$((attempt + 1))
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Reply failed (attempt $attempt/$MAX_RETRIES): $(echo "$response" | head -c 200)" >> "$LOG_FILE"
        sleep 2
    done

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Reply failed after $MAX_RETRIES attempts" >> "$LOG_FILE"
    return 1
}

# Send a message to a Feishu chat (not quoting, for commands)
send_to_feishu() {
    local chat_id="$1"
    local message
    message=$(truncate_message "$2")

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
        log "Send failed (attempt $attempt/$MAX_RETRIES): $(echo "$response" | head -c 200)"
        sleep 2
    done

    log "Send failed after $MAX_RETRIES attempts"
    return 1
}

# Update an existing Feishu message (for streaming)
# Usage: update_message <msg_id> <text> [--markdown]
update_message() {
    local msg_id="$1"
    local message
    message=$(truncate_message "$2")
    local use_markdown="${3:-}"

    local msg_type content
    if [[ "$use_markdown" == "--markdown" ]]; then
        msg_type="post"
        content=$(jq -n --arg md "$message" '{"zh_cn":{"content":[[{"tag":"md","text":$md}]]}}' | jq -c .)
    else
        msg_type="text"
        content=$(jq -n --arg text "$message" '{"text":$text}' | jq -c .)
    fi

    lark-cli api PUT "/open-apis/im/v1/messages/$msg_id" \
        --data "$(jq -n --arg t "$msg_type" --arg c "$content" '{msg_type:$t,content:$c}')" \
        --as bot 2>/dev/null || true
}

# Send error feedback to Feishu
reply_error_to_feishu() {
    local chat_id="$1"
    local message_id="$2"
    local error_msg="$3"

    # Add error emoji
    add_reaction "$message_id" "$ERROR_EMOJI" >/dev/null

    # Send error message
    send_to_feishu "$chat_id" "[错误] $error_msg" || true
}

# Enqueue a message for processing (per-chat serial, cross-chat parallel)
enqueue_message() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"
    local depth_file="$QUEUE_DIR/${chat_id}.depth"

    # Track queue depth: increment before spawning (main loop is single-threaded, no race)
    local depth=0
    [[ -f "$depth_file" ]] && depth=$(cat "$depth_file" 2>/dev/null || echo 0)
    echo $((depth + 1)) > "$depth_file"

    local is_queued=$( (( depth > 0 )) && echo 1 || echo 0 )

    # Run in background with per-chat lock (serializes within same chat)
    # All API calls (reactions, etc.) happen inside the subshell to avoid blocking main loop
    (
        trap - EXIT TERM INT  # Don't inherit parent's cleanup trap

        # Show waiting indicator if queued (non-blocking to main loop)
        local queued_reaction_id=""
        if (( is_queued )); then
            queued_reaction_id=$(add_reaction "$message_id" "OneSecond")
            log "Message queued for busy chat $chat_id (depth: $((depth + 1)))"
        fi

        flock -w 600 9 || { log "Queue timeout for chat $chat_id"; return 1; }

        # Remove queued indicator now that we're starting
        if [[ -n "$queued_reaction_id" ]]; then
            remove_reaction "$message_id" "$queued_reaction_id" 2>/dev/null || true
        fi

        process_message "$prompt" "$chat_id" "$message_id"

        # Decrement queue depth (inside flock, so no race between subshells)
        local d
        d=$(cat "$depth_file" 2>/dev/null || echo 1)
        echo $((d - 1)) > "$depth_file"
    ) 9>"$QUEUE_DIR/${chat_id}.lock" &
}

# Process a message: run agent with streaming reply (runs in background)
process_message() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"

    # Step 1: Start agent immediately (don't wait for API calls)
    local outfile
    outfile=$(mktemp /tmp/agent_out.XXXXXX)
    AGENT_PID=""
    start_agent "$prompt" "$chat_id" "$outfile"

    # Step 2: Add emoji reaction and create placeholder reply (while agent is already running)
    local reaction_id
    reaction_id=$(add_reaction "$message_id" "$WORKING_EMOJI")
    log "Added reaction: $reaction_id"

    local reply_msg_id=""
    local last_content=""

    reply_msg_id=$(reply_to_feishu "$message_id" "⏳ 正在处理...")
    log "Created streaming reply: $reply_msg_id"

    if [[ -n "$AGENT_PID" ]]; then
        save_agent_pid "$chat_id" "$AGENT_PID" "$reply_msg_id"
        log "Agent started (PID: $AGENT_PID)"
    fi

    # Poll agent output and update message
    local elapsed=0
    while [[ -n "$AGENT_PID" ]] && kill -0 "$AGENT_PID" 2>/dev/null; do
        sleep "$STREAM_INTERVAL"
        elapsed=$((elapsed + STREAM_INTERVAL))
        local current_content
        current_content=$(cat "$outfile" 2>/dev/null || true)
        if [[ -n "$current_content" && "$current_content" != "$last_content" && -n "$reply_msg_id" ]]; then
            update_message "$reply_msg_id" "${current_content:0:3997}..."
            last_content="$current_content"
            log "Stream update: ${current_content:0:100}..."
        elif [[ -z "$current_content" && -n "$reply_msg_id" ]]; then
            # No output yet — show elapsed time so user knows it's still working
            update_message "$reply_msg_id" "⏳ 正在处理...（已等待 ${elapsed} 秒）"
            log "Progress: waiting ${elapsed}s (PID: $AGENT_PID)"
        fi
    done

    # Wait for process to fully exit
    wait "$AGENT_PID" 2>/dev/null || true

    # Check if cancelled (PID file already removed by /cancel)
    if [[ ! -f "$PID_DIR/$chat_id" ]]; then
        log "Agent was cancelled for chat $chat_id"
        rm -f "$outfile" "${outfile}.json"
        # Remove working emoji
        if [[ -n "$reaction_id" ]]; then
            remove_reaction "$message_id" "$reaction_id"
        fi
        return
    fi

    clear_agent_pid "$chat_id"

    # Save session for codex
    if [[ "$AGENT_TYPE" == "codex" ]]; then
        save_codex_session "$chat_id" "${outfile}.json"
    fi

    # Step 4: Final update with complete result
    local result
    result=$(cat "$outfile" 2>/dev/null)
    rm -f "$outfile" "${outfile}.json"

    if [[ -z "$result" ]]; then
        log "Error: Agent returned empty result"
        if [[ -n "$reply_msg_id" ]]; then
            update_message "$reply_msg_id" "[错误] Agent 未返回任何结果，请稍后重试"
        fi
        reply_error_to_feishu "$chat_id" "$message_id" "Agent 未返回任何结果，请稍后重试"
    elif [[ -n "$reply_msg_id" ]]; then
        update_message "$reply_msg_id" "$(truncate_message "$result")" --markdown
        log "Reply sent to $chat_id"
    else
        log "Error: Failed to create reply message"
        reply_error_to_feishu "$chat_id" "$message_id" "回复发送失败，请稍后重试"
    fi

    # Step 5: Remove working emoji (task complete)
    if [[ -n "$reaction_id" ]]; then
        remove_reaction "$message_id" "$reaction_id"
        log "Removed reaction: task complete"
    fi
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
                local workspace
                workspace=$(get_workspace "$chat_id")
                send_to_feishu "$chat_id" "$(printf '📊 当前状态：\nAgent 类型: %s\n工作目录: %s\n会话状态: %b\n超时设置: %s' \
                    "$AGENT_TYPE" "$workspace" "$status_session" "$status_timeout")" || true
                log "Command: /status"
                continue
                ;;
            /agent)
                send_to_feishu "$chat_id" "$(printf '当前 Agent 类型: %s（可用: codex, claude）\n用法: /agent codex 或 /agent claude' "$AGENT_TYPE")" || true
                log "Command: /agent (query)"
                continue
                ;;
            "/agent codex"|"/agent claude")
                local new_agent="${prompt##/agent }"
                if [[ "$new_agent" == "$AGENT_TYPE" ]]; then
                    send_to_feishu "$chat_id" "当前已经是 $AGENT_TYPE，无需切换。" || true
                else
                    AGENT_TYPE="$new_agent"
                    clear_session "$chat_id"
                    send_to_feishu "$chat_id" "已切换到 $AGENT_TYPE，会话上下文已清除。" || true
                    log "Agent switched to $AGENT_TYPE by chat $chat_id"
                fi
                continue
                ;;
            /workspace)
                local workspace
                workspace=$(get_workspace "$chat_id")
                send_to_feishu "$chat_id" "$(printf '当前工作目录: %s\n用法: /workspace <path>' "$workspace")" || true
                log "Command: /workspace (query)"
                continue
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
                continue
                ;;
            /cancel)
                if cancel_agent "$chat_id"; then
                    send_to_feishu "$chat_id" "已取消当前请求。" || true
                    log "Agent cancelled for chat $chat_id"
                else
                    send_to_feishu "$chat_id" "当前没有正在进行的请求。" || true
                fi
                continue
                ;;
            /new|新对话)
                log "Command: /new - creating new session for $chat_id"
                local new_sid
                new_sid=$(create_new_session "$chat_id") || true
                if [[ -n "$new_sid" ]]; then
                    send_to_feishu "$chat_id" "✅ 已创建新会话（ID: ${new_sid:0:16}...）" || true
                    log "New session created for $chat_id: $new_sid"
                else
                    send_to_feishu "$chat_id" "❌ 创建新会话失败，请稍后重试。" || true
                    log "Failed to create new session for $chat_id"
                fi
                continue
                ;;
            /*)
                send_to_feishu "$chat_id" "$(printf '未知命令: %s\n输入 /help 查看可用命令列表。' "$prompt")" || true
                log "Unknown command: $prompt"
                continue
                ;;
        esac

        # Enqueue message (per-chat serial, cross-chat parallel)
        enqueue_message "$prompt" "$chat_id" "$message_id"

    done

    log "=== lark-agent-bridge stopped ==="
}

main "$@"
