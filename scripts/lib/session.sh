#!/usr/bin/env bash
# session.sh — 会话管理（session、workspace、PID 跟踪）

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

# Get agent type for a chat (falls back to global AGENT_TYPE)
get_agent_type() {
    local chat_id="$1"
    local at_file="$SESSION_DIR/${chat_id}.agent_type"
    if [[ -f "$at_file" ]]; then
        cat "$at_file"
    else
        echo "$AGENT_TYPE"
    fi
}

# Set agent type for a chat
set_agent_type() {
    local chat_id="$1"
    local agent_type="$2"
    echo "$agent_type" > "$SESSION_DIR/${chat_id}.agent_type"
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
