#!/usr/bin/env bash
# agent.sh — AI Agent 调用（codex / claude）

ensure_agent_workspace() {
    local workspace="$1"
    if [[ ! -d "$workspace" ]]; then
        log "Workspace does not exist: $workspace"
        return 1
    fi
}

write_agent_error() {
    local outfile="$1"
    local error_msg="$2"
    printf '%s\n' "$error_msg" > "$outfile"
    printf '%s\n' "$error_msg" > "${outfile}.err"
}

# Create a new agent session for a chat (synchronous, used by /new)
create_new_session() {
    local chat_id="$1"
    local workspace agent_type
    workspace=$(get_workspace "$chat_id")
    agent_type=$(get_agent_type "$chat_id")

    if ! ensure_agent_workspace "$workspace"; then
        return 1
    fi

    case "$agent_type" in
        codex)
            local tmpfile jsonfile
            tmpfile=$(mktemp /tmp/codex_out.XXXXXX)
            jsonfile=$(mktemp /tmp/codex_json.XXXXXX)

            (cd "$workspace" && $CODEX_CMD exec $CODEX_SKIP_CHECK "你好" -o "$tmpfile" --json < /dev/null > "$jsonfile") 2>>"$LOG_FILE" || true

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
            local jsonfile
            jsonfile=$(mktemp /tmp/claude_json.XXXXXX)

            (cd "$workspace" && $CLAUDE_CMD -p "你好" --output-format json > "$jsonfile" < /dev/null) 2>>"$LOG_FILE" || true

            echo "[$(date '+%Y-%m-%d %H:%M:%S')] create_new_session(claude): $(head -c 200 "$jsonfile" 2>/dev/null)" >> "$LOG_FILE"

            local session_id
            session_id=$(jq -r '.session_id // empty' "$jsonfile" 2>/dev/null || true)

            rm -f "$jsonfile"

            if [[ -n "$session_id" ]]; then
                save_session_id "$chat_id" "$session_id"
                echo "$session_id"
                return 0
            fi
            return 1
            ;;
        *)
            log "Unsupported agent type for new session: $agent_type"
            return 1
            ;;
    esac
}

# Start AI agent in background, writing output to tmpfile. Sets AGENT_PID.
start_agent() {
    local prompt="$1"
    local chat_id="$2"
    local outfile="$3"

    local workspace agent_type
    workspace=$(get_workspace "$chat_id")
    agent_type=$(get_agent_type "$chat_id")

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Calling $agent_type in $workspace ..." >> "$LOG_FILE"

    if ! ensure_agent_workspace "$workspace"; then
        write_agent_error "$outfile" "工作目录不存在: $workspace"
        AGENT_PID=""
        return 1
    fi

    case "$agent_type" in
        codex)
            local session_id
            session_id=$(get_session_id "$chat_id") || true

            local jsonfile="${outfile}.json"
            local errfile="${outfile}.err"

            (
                trap - EXIT TERM INT
                cd "$workspace"

                run_codex_json() {
                    "$@" --json < /dev/null 2>>"$errfile" | while IFS= read -r line; do
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

            local jsonfile="${outfile}.json"
            local errfile="${outfile}.err"

            (
                trap - EXIT TERM INT
                cd "$workspace"

                run_claude() {
                    # Run claude with --output-format json, extract text result to outfile
                    # and session_id to jsonfile for session tracking
                    "$@" --output-format json < /dev/null 2>>"$errfile" | {
                        local full_json
                        full_json=$(cat)
                        # Extract result text
                        local text
                        text=$(echo "$full_json" | jq -r '.result // empty' 2>/dev/null) || true
                        if [[ -n "$text" ]]; then
                            printf '%s' "$text" > "$outfile"
                        fi
                        # Save session_id for future resume
                        local sid
                        sid=$(echo "$full_json" | jq -r '.session_id // empty' 2>/dev/null) || true
                        if [[ -n "$sid" ]]; then
                            echo "$sid" > "$jsonfile"
                        fi
                    } || true
                }

                if [[ -n "$session_id" ]]; then
                    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resuming claude session: $session_id" >> "$LOG_FILE"
                    run_claude $CLAUDE_CMD -p "$prompt" --resume "$session_id"
                    if [[ ! -s "$outfile" ]]; then
                        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resume failed, starting new claude session" >> "$LOG_FILE"
                        rm -f "$outfile" "$jsonfile"
                        run_claude $CLAUDE_CMD -p "$prompt"
                    fi
                else
                    run_claude $CLAUDE_CMD -p "$prompt"
                fi
            ) &
            AGENT_PID=$!
            ;;
        *)
            write_agent_error "$outfile" "未知的 Agent 类型: $agent_type"
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
