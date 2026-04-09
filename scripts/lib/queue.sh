#!/usr/bin/env bash
# queue.sh — 消息队列和消息处理

# Enqueue a message for processing (per-chat serial, cross-chat parallel)
enqueue_message() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"
    local depth_file="$QUEUE_DIR/${chat_id}.depth"
    local task_id
    task_id=$(task_create "$chat_id" "$message_id")

    # Track queue depth: increment before spawning (main loop is single-threaded, no race)
    local depth=0
    [[ -f "$depth_file" ]] && depth=$(cat "$depth_file" 2>/dev/null || echo 0)
    echo $((depth + 1)) > "$depth_file"

    local is_queued=$( (( depth > 0 )) && echo 1 || echo 0 )

    # Run in background with per-chat lock (serializes within same chat)
    # All API calls (reactions, etc.) happen inside the subshell to avoid blocking main loop
    (
        trap - EXIT TERM INT

        # Show waiting indicator if queued (non-blocking to main loop)
        local queued_reaction_id=""
        if (( is_queued )); then
            queued_reaction_id=$(add_reaction "$message_id" "OneSecond")
            task_set_field "$task_id" queued_reaction_id "$queued_reaction_id"
            task_set_field "$task_id" note "等待前序任务完成"
            log "Message queued for busy chat $chat_id (depth: $((depth + 1)))"
        fi

        if ! flock -w 600 9; then
            task_transition "$task_id" failed "队列等待超时"
            log "Queue timeout for chat $chat_id"
            return 1
        fi

        # Remove queued indicator now that we're starting
        if [[ -n "$queued_reaction_id" ]]; then
            remove_reaction "$message_id" "$queued_reaction_id" 2>/dev/null || true
            task_clear_field "$task_id" queued_reaction_id
        fi

        process_message "$prompt" "$chat_id" "$message_id" "$task_id"

        # Decrement queue depth (inside flock, so no race between subshells)
        local d
        d=$(cat "$depth_file" 2>/dev/null || echo 1)
        d=$((d - 1))
        if (( d > 0 )); then
            echo "$d" > "$depth_file"
        else
            rm -f "$depth_file"
        fi
    ) 9>"$QUEUE_DIR/${chat_id}.lock" &
}

# Process a message: run agent with streaming reply (runs in background)
process_message() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"
    local task_id="$4"

    task_set_current "$chat_id" "$task_id"
    task_transition "$task_id" starting "开始处理消息"

    # Step 1: Start agent immediately (don't wait for API calls)
    local outfile
    outfile=$(mktemp /tmp/agent_out.XXXXXX)
    AGENT_PID=""
    start_agent "$prompt" "$chat_id" "$outfile"
    if [[ -n "$AGENT_PID" ]]; then
        task_set_field "$task_id" agent_pid "$AGENT_PID"
    fi

    # Step 2: Add emoji reaction and create placeholder reply (while agent is already running)
    local reaction_id
    reaction_id=$(add_reaction "$message_id" "$WORKING_EMOJI")
    task_set_field "$task_id" reaction_id "$reaction_id"
    log "Added reaction: $reaction_id"

    local reply_msg_id=""
    local last_content=""

    reply_msg_id=$(reply_to_feishu "$message_id" "⏳ 正在处理...")
    task_set_field "$task_id" reply_message_id "$reply_msg_id"
    log "Created streaming reply: $reply_msg_id"

    if [[ -n "$AGENT_PID" ]]; then
        if [[ "$(task_read_field "$task_id" state)" != "cancelling" ]]; then
            task_transition "$task_id" running "Agent 正在处理"
        fi
        log "Agent started (PID: $AGENT_PID)"
    else
        task_transition "$task_id" failed "Agent 进程未成功启动" || true
    fi

    # Poll agent output and update message
    local elapsed=0
    local start_ts
    start_ts=$(date +%s)
    local progress_pid=""
    while [[ -n "$AGENT_PID" ]] && kill -0 "$AGENT_PID" 2>/dev/null; do
        sleep "$STREAM_INTERVAL"
        elapsed=$(( $(date +%s) - start_ts ))
        local current_content
        current_content=$(cat "$outfile" 2>/dev/null || true)
        if [[ -n "$current_content" && "$current_content" != "$last_content" && -n "$reply_msg_id" ]]; then
            # Kill any pending progress update before sending content update
            [[ -n "$progress_pid" ]] && kill "$progress_pid" 2>/dev/null || true
            progress_pid=""
            update_message "$reply_msg_id" "${current_content:0:3997}..."
            last_content="$current_content"
            log "Stream update: ${current_content:0:100}..."
        elif [[ -z "$current_content" && -n "$reply_msg_id" ]]; then
            # Kill previous progress update to prevent message rollback
            [[ -n "$progress_pid" ]] && kill "$progress_pid" 2>/dev/null || true
            update_message "$reply_msg_id" "⏳ 正在处理...（已等待 ${elapsed} 秒）" &
            progress_pid=$!
            log "Progress: waiting ${elapsed}s (PID: $AGENT_PID)"
        fi
    done
    # Clean up any remaining progress update
    [[ -n "$progress_pid" ]] && kill "$progress_pid" 2>/dev/null || true

    # Wait for process to fully exit
    wait "$AGENT_PID" 2>/dev/null || true

    local state
    state=$(task_read_field "$task_id" state)

    if [[ "$state" == "cancelling" || "$state" == "cancelled" ]]; then
        task_transition "$task_id" cancelled "任务已取消" || true
        log "Agent was cancelled for chat $chat_id"
        rm -f "$outfile" "${outfile}.json"
        if [[ -n "$reaction_id" ]]; then
            remove_reaction "$message_id" "$reaction_id"
        fi
        task_clear_field "$task_id" agent_pid
        task_clear_current "$chat_id" "$task_id"
        return
    fi

    task_clear_field "$task_id" agent_pid

    # Save session id for future resume
    local agent_type
    agent_type=$(get_agent_type "$chat_id")
    case "$agent_type" in
        codex)
            save_codex_session "$chat_id" "${outfile}.json"
            ;;
        claude)
            if [[ -f "${outfile}.json" ]]; then
                local claude_sid
                claude_sid=$(cat "${outfile}.json" 2>/dev/null)
                if [[ -n "$claude_sid" ]]; then
                    save_session_id "$chat_id" "$claude_sid"
                    log "Saved claude session: $claude_sid for chat: $chat_id"
                fi
                rm -f "${outfile}.json"
            fi
            ;;
    esac

    # Step 4: Final update with complete result
    local result
    result=$(cat "$outfile" 2>/dev/null)
    rm -f "$outfile" "${outfile}.json"

    if [[ -z "$result" ]]; then
        log "Error: Agent returned empty result"
        task_transition "$task_id" failed "Agent 未返回任何结果" || true
        if [[ -n "$reply_msg_id" ]]; then
            update_message "$reply_msg_id" "[错误] Agent 未返回任何结果，请稍后重试"
        fi
        reply_error_to_feishu "$chat_id" "$message_id" "Agent 未返回任何结果，请稍后重试"
    elif [[ -n "$reply_msg_id" ]]; then
        task_transition "$task_id" completed "任务处理完成" || true
        update_message "$reply_msg_id" "$(truncate_message "$result")" --markdown
        log "Reply sent to $chat_id"
    else
        log "Error: Failed to create reply message"
        task_transition "$task_id" failed "回复消息创建失败" || true
        reply_error_to_feishu "$chat_id" "$message_id" "回复发送失败，请稍后重试"
    fi

    # Step 5: Remove working emoji (task complete)
    if [[ -n "$reaction_id" ]]; then
        remove_reaction "$message_id" "$reaction_id"
        log "Removed reaction: task complete"
        task_clear_field "$task_id" reaction_id
    fi

    task_clear_current "$chat_id" "$task_id"
}
