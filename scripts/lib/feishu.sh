#!/usr/bin/env bash
# feishu.sh — 飞书 API 交互（消息发送、回复、更新、表情）

log_feishu_failure() {
    local operation="$1"
    local attempt="$2"
    local response="$3"
    log "${operation} failed (attempt ${attempt}/${MAX_RETRIES}): $(echo "$response" | tr '\n' ' ' | head -c 200)"
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
        log_feishu_failure "Add reaction" "$attempt" "$response"
        sleep 2
    done

    return 1
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
        log_feishu_failure "Remove reaction" "$attempt" "$response"
        sleep 2
    done

    log "Failed to remove reaction after $MAX_RETRIES attempts"
    return 1
}

# Fit a message into a single Feishu message update/send payload.
truncate_message() {
    local message="$1"
    if [[ ${#message} -gt "$FEISHU_MESSAGE_LIMIT" ]]; then
        echo "${message:0:$((FEISHU_MESSAGE_LIMIT - 3))}..."
    else
        echo "$message"
    fi
}

message_chunk_count() {
    local message="$1"
    local length=${#message}
    if (( length == 0 )); then
        echo 1
        return 0
    fi

    local total=1
    while true; do
        local suffix payload_limit needed
        suffix=$(message_chunk_suffix "$total" "$total")
        payload_limit=$((FEISHU_MESSAGE_LIMIT - ${#suffix}))
        if (( payload_limit <= 0 )); then
            payload_limit=1
        fi
        needed=$(( (length + payload_limit - 1) / payload_limit ))
        if (( needed == total )); then
            echo "$total"
            return 0
        fi
        total="$needed"
    done
}

message_chunk_suffix() {
    local index="$1"
    local total="$2"
    if (( total <= 1 )); then
        echo ""
    else
        printf '\n\n[%s/%s]' "$index" "$total"
    fi
}

message_chunk_at() {
    local message="$1"
    local start="$2"
    local index="${3:-1}"
    local total="${4:-1}"
    local suffix payload_limit chunk

    suffix=$(message_chunk_suffix "$index" "$total")
    payload_limit=$((FEISHU_MESSAGE_LIMIT - ${#suffix}))
    if (( payload_limit <= 0 )); then
        payload_limit=1
    fi

    chunk="${message:start:payload_limit}"
    printf '%s%s\n' "$chunk" "$suffix"
}

send_message_once() {
    local chat_id="$1"
    local message="$2"
    local use_markdown="${3:-}"

    local msg_flag="--text"
    if [[ "$use_markdown" == "--markdown" ]]; then
        msg_flag="--markdown"
    fi

    local attempt=0
    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli im +messages-send \
            --chat-id "$chat_id" \
            $msg_flag "$message" \
            --as bot 2>&1)

        if echo "$response" | jq -e '.ok == true or .code == 0' &>/dev/null; then
            return 0
        fi

        attempt=$((attempt + 1))
        log_feishu_failure "Send" "$attempt" "$response"
        sleep 2
    done

    log "Send failed after $MAX_RETRIES attempts"
    return 1
}

reply_message_once() {
    local message_id="$1"
    local message="$2"
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
        log_feishu_failure "Reply" "$attempt" "$response"
        sleep 2
    done

    log "Reply failed after $MAX_RETRIES attempts"
    return 1
}

# Reply to a Feishu message by quoting it (with retry), prints message_id on success
# Usage: reply_to_feishu <message_id> <text> [--markdown]
reply_to_feishu() {
    local message_id="$1"
    local message="$2"
    local use_markdown="${3:-}"
    local first_reply_msg_id=""
    local start=0
    local total index payload_limit suffix

    total=$(message_chunk_count "$message")
    index=1
    while (( start < ${#message} || start == 0 )); do
        local chunk reply_msg_id
        chunk=$(message_chunk_at "$message" "$start" "$index" "$total")
        reply_msg_id=$(reply_message_once "$message_id" "$chunk" "$use_markdown") || return 1
        if [[ -z "$first_reply_msg_id" ]]; then
            first_reply_msg_id="$reply_msg_id"
        fi
        suffix=$(message_chunk_suffix "$index" "$total")
        payload_limit=$((FEISHU_MESSAGE_LIMIT - ${#suffix}))
        if (( payload_limit <= 0 )); then
            payload_limit=1
        fi
        start=$((start + payload_limit))
        index=$((index + 1))
        if (( ${#message} == 0 )); then
            break
        fi
    done

    if [[ -n "$first_reply_msg_id" ]]; then
        echo "$first_reply_msg_id"
        return 0
    fi

    return 1
}

# Send a message to a Feishu chat (not quoting, for commands)
send_to_feishu() {
    local chat_id="$1"
    local message="$2"
    local use_markdown="${3:-}"
    local start=0
    local total index payload_limit suffix

    total=$(message_chunk_count "$message")
    index=1
    while (( start < ${#message} || start == 0 )); do
        local chunk
        chunk=$(message_chunk_at "$message" "$start" "$index" "$total")
        send_message_once "$chat_id" "$chunk" "$use_markdown" || return 1
        suffix=$(message_chunk_suffix "$index" "$total")
        payload_limit=$((FEISHU_MESSAGE_LIMIT - ${#suffix}))
        if (( payload_limit <= 0 )); then
            payload_limit=1
        fi
        start=$((start + payload_limit))
        index=$((index + 1))
        if (( ${#message} == 0 )); then
            break
        fi
    done

    return 0
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

    local attempt=0
    while (( attempt < MAX_RETRIES )); do
        local response
        response=$(lark-cli api PUT "/open-apis/im/v1/messages/$msg_id" \
            --data "$(jq -n --arg t "$msg_type" --arg c "$content" '{msg_type:$t,content:$c}')" \
            --as bot 2>&1) || true

        if echo "$response" | jq -e '.code == 0 or .msg == "success"' &>/dev/null; then
            return 0
        fi

        attempt=$((attempt + 1))
        log_feishu_failure "Update message" "$attempt" "$response"
        sleep 2
    done

    log "Update message failed after $MAX_RETRIES attempts"
    return 1
}

# Send error feedback to Feishu
reply_error_to_feishu() {
    local chat_id="$1"
    local message_id="$2"
    local error_msg="$3"

    add_reaction "$message_id" "$ERROR_EMOJI" >/dev/null || true
    send_to_feishu "$chat_id" "[错误] $error_msg"
}
