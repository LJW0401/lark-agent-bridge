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
LOG_FILE="${LOG_FILE:-$PROJECT_DIR/logs/bridge.log}"

mkdir -p "$(dirname "$LOG_FILE")"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}

# Call AI agent with the given prompt
call_agent() {
    local prompt="$1"
    local result=""

    case "$AGENT_TYPE" in
        codex)
            log "Calling Codex CLI..."
            result=$($CODEX_CMD -q "$prompt" 2>&1) || true
            ;;
        claude)
            log "Calling Claude Code..."
            result=$($CLAUDE_CMD -p "$prompt" 2>&1) || true
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
    log "Listening for Feishu bot messages..."

    lark-cli event +subscribe \
        --events "im.message.receive_v1" \
        --format compact | while IFS= read -r event; do

        # Parse event fields
        chat_id=$(echo "$event" | jq -r '.event.message.chat_id // empty' 2>/dev/null)
        msg_type=$(echo "$event" | jq -r '.event.message.message_type // empty' 2>/dev/null)
        content_raw=$(echo "$event" | jq -r '.event.message.content // empty' 2>/dev/null)

        # Only handle text messages
        if [[ "$msg_type" != "text" ]]; then
            log "Skipping non-text message (type: $msg_type)"
            continue
        fi

        # Extract text from content JSON
        prompt=$(echo "$content_raw" | jq -r '.text // empty' 2>/dev/null)

        if [[ -z "$prompt" || -z "$chat_id" ]]; then
            log "Skipping: empty prompt or chat_id"
            continue
        fi

        log "Received: $prompt (chat: $chat_id)"

        # Call AI agent
        result=$(call_agent "$prompt")
        log "Agent response: ${result:0:200}..."

        # Reply to Feishu
        reply_to_feishu "$chat_id" "$result"
        log "Reply sent to $chat_id"

    done

    log "=== lark-agent-bridge stopped ==="
}

main "$@"
