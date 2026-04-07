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
LOG_FILE="${LOG_FILE:-$PROJECT_DIR/logs/bridge.log}"

mkdir -p "$(dirname "$LOG_FILE")"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

# Add emoji reaction to a message, returns reaction_id
add_reaction() {
    local message_id="$1"
    local emoji_type="$2"

    local response
    response=$(lark-cli im reactions create \
        --params "{\"message_id\":\"$message_id\"}" \
        --data "{\"reaction_type\":{\"emoji_type\":\"$emoji_type\"}}" \
        --as bot 2>&1) || true

    echo "$response" | jq -r '.data.reaction_id // empty' 2>/dev/null
}

# Remove emoji reaction from a message
remove_reaction() {
    local message_id="$1"
    local reaction_id="$2"

    lark-cli im reactions delete \
        --params "{\"message_id\":\"$message_id\",\"reaction_id\":\"$reaction_id\"}" \
        --as bot 2>&1 >/dev/null || log "Failed to remove reaction"
}

# Call AI agent with the given prompt
call_agent() {
    local prompt="$1"
    local result=""

    log "Calling $AGENT_TYPE..."

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

# Reply to a Feishu chat
reply_to_feishu() {
    local chat_id="$1"
    local message="$2"

    # Truncate if too long (Feishu message limit)
    if [[ ${#message} -gt 4000 ]]; then
        message="${message:0:3997}..."
    fi

    lark-cli im +messages-send \
        --chat-id "$chat_id" \
        --text "$message" \
        --as bot 2>&1 || log "Failed to send reply"
}

# Main loop: subscribe to bot message events
main() {
    log "=== lark-agent-bridge started ==="
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

        # Step 3: Reply result to Feishu
        reply_to_feishu "$chat_id" "$result"
        log "Reply sent to $chat_id"

        # Step 4: Remove emoji reaction (task complete)
        if [[ -n "$reaction_id" ]]; then
            remove_reaction "$message_id" "$reaction_id"
            log "Removed reaction: task complete"
        fi

    done

    log "=== lark-agent-bridge stopped ==="
}

main "$@"
