#!/usr/bin/env bash
# agent.sh — AI Agent 调用（codex / claude）

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
            return 1
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
                    $CLAUDE_CMD -p "$prompt" --resume "$session_id" > "$outfile" 2>/dev/null || true
                    if [[ ! -s "$outfile" ]]; then
                        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Resume failed, starting new claude session" >> "$LOG_FILE"
                        rm -f "$outfile"
                        $CLAUDE_CMD -p "$prompt" > "$outfile" 2>/dev/null || true
                    fi
                else
                    $CLAUDE_CMD -p "$prompt" > "$outfile" 2>/dev/null || true
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
