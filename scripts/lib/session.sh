#!/usr/bin/env bash
# session.sh — 会话管理（session、workspace、任务取消）

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

# Cancel running agent for a chat
cancel_agent() {
    local chat_id="$1"
    local task_id state pid reply_msg_id
    task_id=$(task_get_current "$chat_id") || return 1
    state=$(task_read_field "$task_id" state)

    case "$state" in
        queued|starting|running|cancelling)
            ;;
        *)
            return 1
            ;;
    esac

    task_transition "$task_id" cancelling "用户请求取消" || return 1

    pid=$(task_read_field "$task_id" agent_pid)
    reply_msg_id=$(task_read_field "$task_id" reply_message_id)

    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        kill -- -"$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
    else
        task_transition "$task_id" cancelled "任务在启动前被取消" || true
        task_clear_current "$chat_id" "$task_id"
    fi

    if [[ -n "$reply_msg_id" ]]; then
        update_message "$reply_msg_id" "[已取消] 请求已被用户中断。"
    fi

    return 0
}
