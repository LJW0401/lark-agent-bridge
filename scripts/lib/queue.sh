#!/usr/bin/env bash
# queue.sh — 消息队列和消息处理

queue_depth_lock_file() {
    local chat_id="$1"
    echo "$QUEUE_DIR/${chat_id}.depth.lock"
}

increment_queue_depth() {
    local depth_file="$1"
    local depth_lock="$2"
    local lock_fd depth

    exec {lock_fd}>"$depth_lock"
    if ! flock -w 5 "$lock_fd"; then
        log "Failed to acquire queue depth lock: $depth_file"
        exec {lock_fd}>&-
        return 1
    fi

    depth=$(cat "$depth_file" 2>/dev/null || echo 0)
    echo $((depth + 1)) > "$depth_file"

    exec {lock_fd}>&-
    echo "$depth"
}

decrement_queue_depth() {
    local depth_file="$1"
    local depth_lock="$2"
    local lock_fd d

    exec {lock_fd}>"$depth_lock"
    if ! flock -w 5 "$lock_fd"; then
        log "Failed to acquire queue depth lock: $depth_file"
        exec {lock_fd}>&-
        return 1
    fi

    d=$(cat "$depth_file" 2>/dev/null || echo 1)
    d=$((d - 1))
    if (( d > 0 )); then
        echo "$d" > "$depth_file"
    else
        rm -f "$depth_file"
    fi

    exec {lock_fd}>&-
}

# Enqueue a message for processing (per-chat serial, cross-chat parallel)
enqueue_message() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"
    local depth_file="$QUEUE_DIR/${chat_id}.depth"
    local depth_lock
    depth_lock=$(queue_depth_lock_file "$chat_id")
    local task_id
    task_id=$(task_create "$chat_id" "$message_id")

    # Track queue depth under a dedicated lock so increments don't race with async decrements
    local depth=0
    if ! depth=$(increment_queue_depth "$depth_file" "$depth_lock"); then
        task_transition "$task_id" failed "队列计数更新失败"
        log "Failed to increment queue depth for chat $chat_id"
        return 1
    fi

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
            decrement_queue_depth "$depth_file" "$depth_lock"
            log "Queue timeout for chat $chat_id"
            return 1
        fi

        # Remove queued indicator now that we're starting
        if [[ -n "$queued_reaction_id" ]]; then
            remove_reaction "$message_id" "$queued_reaction_id" 2>/dev/null || true
            task_clear_field "$task_id" queued_reaction_id
        fi

        process_message "$prompt" "$chat_id" "$message_id" "$task_id"

        decrement_queue_depth "$depth_file" "$depth_lock"
    ) 9>"$QUEUE_DIR/${chat_id}.lock" &
}

# Process a message: run agent with streaming reply (runs in background)
process_message() {
    local prompt="$1"
    local chat_id="$2"
    local message_id="$3"
    local task_id="$4"
    local errfile=""

    task_set_current "$chat_id" "$task_id"
    task_transition "$task_id" starting "开始处理消息"

    # Step 1: Start agent immediately (don't wait for API calls)
    local outfile
    outfile=$(mktemp /tmp/agent_out.XXXXXX)
    errfile="${outfile}.err"
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
    if [[ -z "$reply_msg_id" ]]; then
        task_set_field "$task_id" note "流式回复创建失败，将在完成后降级为普通消息发送"
    fi

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
    while [[ -n "$AGENT_PID" ]] && kill -0 "$AGENT_PID" 2>/dev/null; do
        sleep "$STREAM_INTERVAL"
        elapsed=$(( $(date +%s) - start_ts ))
        local current_content
        current_content=$(cat "$outfile" 2>/dev/null || true)
        if [[ -n "$current_content" && "$current_content" != "$last_content" && -n "$reply_msg_id" ]]; then
            if ! update_message "$reply_msg_id" "$(truncate_message "$current_content")"; then
                log "Streaming update failed for reply $reply_msg_id"
            fi
            last_content="$current_content"
            log "Stream update: ${current_content:0:100}..."
        elif [[ -z "$current_content" && -n "$reply_msg_id" ]]; then
            if ! update_message "$reply_msg_id" "⏳ 正在处理...（已等待 ${elapsed} 秒）"; then
                log "Progress update failed for reply $reply_msg_id"
            fi
            log "Progress: waiting ${elapsed}s (PID: $AGENT_PID)"
        fi
    done

    # Wait for process to fully exit
    wait "$AGENT_PID" 2>/dev/null || true

    local state
    state=$(task_read_field "$task_id" state)

    if [[ "$state" == "cancelling" || "$state" == "cancelled" ]]; then
        task_transition "$task_id" cancelled "任务已取消" || true
        log "Agent was cancelled for chat $chat_id"
        rm -f "$outfile" "${outfile}.json" "$errfile"
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
    local result error_detail
    result=$(cat "$outfile" 2>/dev/null)
    error_detail=$(head -c 500 "$errfile" 2>/dev/null | tr '\n' ' ' || true)
    rm -f "$outfile" "${outfile}.json"

    if [[ -z "$result" ]]; then
        log "Error: Agent returned empty result"
        if [[ -n "$error_detail" ]]; then
            task_transition "$task_id" failed "Agent 执行失败: ${error_detail}" || true
        else
            task_transition "$task_id" failed "Agent 未返回任何结果" || true
        fi
        if [[ -n "$reply_msg_id" ]]; then
            update_message "$reply_msg_id" "[错误] Agent 未返回任何结果，请稍后重试" || true
        fi
        reply_error_to_feishu "$chat_id" "$message_id" "Agent 未返回任何结果，请稍后重试" || true
    elif [[ -n "$reply_msg_id" ]]; then
        task_transition "$task_id" completed "任务处理完成" || true
        local first_chunk extra_chunks start
        first_chunk=$(message_chunk_at "$result" 0)
        if update_message "$reply_msg_id" "$first_chunk" --markdown; then
            start=$FEISHU_MESSAGE_LIMIT
            extra_chunks=0
            while (( start < ${#result} )); do
                local chunk
                chunk=$(message_chunk_at "$result" "$start")
                if ! reply_to_feishu "$message_id" "$chunk" --markdown; then
                    log "Additional chunk reply failed for $chat_id at offset $start"
                    send_to_feishu "$chat_id" "[错误] 长回复的后续分片发送失败，请查看日志后重试。" || true
                    break
                fi
                extra_chunks=$((extra_chunks + 1))
                start=$((start + FEISHU_MESSAGE_LIMIT))
            done
            log "Reply sent to $chat_id with $((extra_chunks + 1)) chunk(s)"
        else
            log "Final reply update failed for $chat_id, falling back to plain message"
            send_to_feishu "$chat_id" "$result" --markdown || true
        fi
    else
        task_transition "$task_id" completed "占位回复创建失败，已降级为普通消息发送结果" || true
        if send_to_feishu "$chat_id" "$result" --markdown; then
            log "Reply fallback sent to $chat_id"
        else
            log "Error: Failed to create reply message and fallback send failed"
            task_transition "$task_id" failed "回复发送失败" || true
            reply_error_to_feishu "$chat_id" "$message_id" "回复发送失败，请稍后重试" || true
        fi
    fi

    # Step 5: Remove working emoji (task complete)
    if [[ -n "$reaction_id" ]]; then
        remove_reaction "$message_id" "$reaction_id"
        log "Removed reaction: task complete"
        task_clear_field "$task_id" reaction_id
    fi

    rm -f "$errfile"
    task_clear_current "$chat_id" "$task_id"
}
