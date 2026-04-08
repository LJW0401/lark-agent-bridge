#!/usr/bin/env bash
# queue.sh — 消息队列和消息处理

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
        trap - EXIT TERM INT

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
    local start_ts
    start_ts=$(date +%s)
    while [[ -n "$AGENT_PID" ]] && kill -0 "$AGENT_PID" 2>/dev/null; do
        sleep "$STREAM_INTERVAL"
        elapsed=$(( $(date +%s) - start_ts ))
        local current_content
        current_content=$(cat "$outfile" 2>/dev/null || true)
        if [[ -n "$current_content" && "$current_content" != "$last_content" && -n "$reply_msg_id" ]]; then
            update_message "$reply_msg_id" "${current_content:0:3997}..."
            last_content="$current_content"
            log "Stream update: ${current_content:0:100}..."
        elif [[ -z "$current_content" && -n "$reply_msg_id" ]]; then
            # No output yet — update elapsed time in background to avoid blocking poll loop
            update_message "$reply_msg_id" "⏳ 正在处理...（已等待 ${elapsed} 秒）" &
            log "Progress: waiting ${elapsed}s (PID: $AGENT_PID)"
        fi
    done

    # Wait for process to fully exit
    wait "$AGENT_PID" 2>/dev/null || true

    # Check if cancelled (PID file already removed by /cancel)
    if [[ ! -f "$PID_DIR/$chat_id" ]]; then
        log "Agent was cancelled for chat $chat_id"
        rm -f "$outfile" "${outfile}.json"
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
