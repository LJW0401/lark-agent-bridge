#!/usr/bin/env bash
# lark-agent-bridge: Listen for Feishu bot messages and forward to AI agent
set -euo pipefail

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
LOG_FILE="${LOG_FILE:-$PROJECT_DIR/logs/bridge.log}"

mkdir -p "$(dirname "$LOG_FILE")"

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
        log "Add reaction failed (attempt $attempt/$MAX_RETRIES)"
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

# Call AI agent with the given prompt
call_agent() {
    local prompt="$1"
    local result=""

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Calling $AGENT_TYPE..." >> "$LOG_FILE"

    case "$AGENT_TYPE" in
        codex)
            local tmpfile
            tmpfile=$(mktemp /tmp/codex_out.XXXXXX)
            $CODEX_CMD exec "$prompt" -o "$tmpfile" >/dev/null 2>&1 || true
            result=$(cat "$tmpfile" 2>/dev/null)
            rm -f "$tmpfile"
            ;;
        claude)
            result=$($CLAUDE_CMD -p "$prompt" 2>/dev/null) || true
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

        # Step 1: Add emoji reaction (working indicator)
        reaction_id=$(add_reaction "$message_id" "$WORKING_EMOJI")
        log "Added reaction: $reaction_id"

        # Step 2: Call AI agent
        result=$(call_agent "$prompt")
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
