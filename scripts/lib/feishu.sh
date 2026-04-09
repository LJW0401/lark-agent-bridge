#!/usr/bin/env bash
# feishu.sh — 飞书 API 交互（消息发送、回复、更新、表情）

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
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Add reaction failed (attempt $attempt/$MAX_RETRIES)" >> "$LOG_FILE"
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

# Truncate message to Feishu limit
truncate_message() {
    local message="$1"
    if [[ ${#message} -gt 4000 ]]; then
        echo "${message:0:3997}..."
    else
        echo "$message"
    fi
}

# Reply to a Feishu message by quoting it (with retry), prints message_id on success
# Usage: reply_to_feishu <message_id> <text> [--markdown]
reply_to_feishu() {
    local message_id="$1"
    local message
    message=$(truncate_message "$2")
    local use_markdown="${3:-}"

    local msg_flag="--text"
    if [[ "$use_markdown" == "--markdown" ]]; then
        msg_flag="--markdown"
    fi

    local attempt=0
    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli im +messages-reply \
            --message-id "$message_id" \
            $msg_flag "$message" \
            --as bot 2>&1)

        local reply_msg_id
        reply_msg_id=$(echo "$response" | jq -r '.data.message_id // .message_id // empty' 2>/dev/null)
        if [[ -n "$reply_msg_id" ]]; then
            echo "$reply_msg_id"
            return 0
        fi

        attempt=$((attempt + 1))
        # Write directly to log file to avoid tee stdout pollution when called inside $()
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Reply failed (attempt $attempt/$MAX_RETRIES): $(echo "$response" | head -c 200)" >> "$LOG_FILE"
        sleep 2
    done

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Reply failed after $MAX_RETRIES attempts" >> "$LOG_FILE"
    return 1
}

# Send a message to a Feishu chat (not quoting, for commands)
send_to_feishu() {
    local chat_id="$1"
    local message
    message=$(truncate_message "$2")

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
        log "Send failed (attempt $attempt/$MAX_RETRIES): $(echo "$response" | head -c 200)"
        sleep 2
    done

    log "Send failed after $MAX_RETRIES attempts"
    return 1
}

# Update an existing Feishu message (for streaming)
# Usage: update_message <msg_id> <text> [--markdown]
update_message() {
    local msg_id="$1"
    local message
    message=$(truncate_message "$2")
    local use_markdown="${3:-}"

    local msg_type content
    if [[ "$use_markdown" == "--markdown" ]]; then
        msg_type="post"
        content=$(jq -n --arg md "$message" '{"zh_cn":{"content":[[{"tag":"md","text":$md}]]}}' | jq -c .)
    else
        msg_type="text"
        content=$(jq -n --arg text "$message" '{"text":$text}' | jq -c .)
    fi

    lark-cli api PUT "/open-apis/im/v1/messages/$msg_id" \
        --data "$(jq -n --arg t "$msg_type" --arg c "$content" '{msg_type:$t,content:$c}')" \
        --as bot 2>/dev/null || true
}

# Send error feedback to Feishu
reply_error_to_feishu() {
    local chat_id="$1"
    local message_id="$2"
    local error_msg="$3"

    add_reaction "$message_id" "$ERROR_EMOJI" >/dev/null
    send_to_feishu "$chat_id" "[错误] $error_msg" || true
}
