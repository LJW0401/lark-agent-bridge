#!/usr/bin/env bash
# task.sh — 显式任务状态机和任务元数据管理

task_now() {
    date +%s
}

task_file() {
    local task_id="$1"
    echo "$TASK_DIR/${task_id}.json"
}

task_current_file() {
    local chat_id="$1"
    echo "$TASK_DIR/${chat_id}.current"
}

task_read_field() {
    local task_id="$1"
    local field="$2"
    local file
    file=$(task_file "$task_id")

    [[ -f "$file" ]] || return 1
    jq -r --arg field "$field" '.[$field] // empty' "$file" 2>/dev/null
}

task_write_json() {
    local task_id="$1"
    local content="$2"
    local file tmp
    file=$(task_file "$task_id")
    tmp="${file}.tmp"

    printf '%s\n' "$content" > "$tmp"
    mv "$tmp" "$file"
}

task_update_json() {
    local task_id="$1"
    local filter="$2"
    shift 2
    local file tmp content
    file=$(task_file "$task_id")
    tmp="${file}.tmp"
    content=$(cat "$file" 2>/dev/null || echo '{}')

    if ! printf '%s\n' "$content" | jq "$filter" "$@" > "$tmp" 2>/dev/null; then
        rm -f "$tmp"
        log "Failed to update task state for $task_id with filter: $filter"
        return 1
    fi

    mv "$tmp" "$file"
}

task_generate_id() {
    local chat_id="$1"
    printf '%s-%s-%s-%s' "$chat_id" "$(task_now)" "$$" "$RANDOM"
}

task_create() {
    local chat_id="$1"
    local message_id="$2"
    local task_id
    task_id=$(task_generate_id "$chat_id")

    task_write_json "$task_id" "$(jq -n \
        --arg task_id "$task_id" \
        --arg chat_id "$chat_id" \
        --arg message_id "$message_id" \
        --arg state "queued" \
        --argjson now "$(task_now)" \
        '{
            task_id: $task_id,
            chat_id: $chat_id,
            message_id: $message_id,
            state: $state,
            created_at: $now,
            updated_at: $now
        }'
    )"

    echo "$task_id"
}

task_set_field() {
    local task_id="$1"
    local field="$2"
    local value="${3:-}"
    task_update_json "$task_id" '.[$field] = $value | .updated_at = $now' \
        --arg field "$field" \
        --arg value "$value" \
        --argjson now "$(task_now)"
}

task_clear_field() {
    local task_id="$1"
    local field="$2"
    task_update_json "$task_id" 'del(.[$field]) | .updated_at = $now' \
        --arg field "$field" \
        --argjson now "$(task_now)"
}

task_transition_allowed() {
    local from_state="$1"
    local to_state="$2"

    case "$from_state:$to_state" in
        queued:starting|queued:cancelling|queued:cancelled|queued:failed)
            return 0
            ;;
        starting:running|starting:cancelling|starting:cancelled|starting:failed)
            return 0
            ;;
        running:cancelling|running:completed|running:failed)
            return 0
            ;;
        cancelling:cancelled|cancelling:failed)
            return 0
            ;;
        cancelled:cancelled|completed:completed|failed:failed)
            return 0
            ;;
    esac

    return 1
}

task_transition() {
    local task_id="$1"
    local new_state="$2"
    local reason="${3:-}"
    local current_state
    current_state=$(task_read_field "$task_id" state)

    if [[ -z "$current_state" ]]; then
        log "Task $task_id has no current state; cannot transition to $new_state"
        return 1
    fi

    if [[ "$current_state" == "$new_state" ]]; then
        task_set_field "$task_id" state "$new_state"
        if [[ -n "$reason" ]]; then
            task_set_field "$task_id" note "$reason"
        fi
        return 0
    fi

    if ! task_transition_allowed "$current_state" "$new_state"; then
        log "Invalid task transition for $task_id: $current_state -> $new_state"
        return 1
    fi

    task_update_json "$task_id" '.state = $state
        | .updated_at = $now
        | if $reason == "" then del(.note) else .note = $reason end' \
        --arg state "$new_state" \
        --arg reason "$reason" \
        --argjson now "$(task_now)"
}

task_set_current() {
    local chat_id="$1"
    local task_id="$2"
    echo "$task_id" > "$(task_current_file "$chat_id")"
}

task_get_current() {
    local chat_id="$1"
    local file
    file=$(task_current_file "$chat_id")
    [[ -f "$file" ]] || return 1
    cat "$file"
}

task_clear_current() {
    local chat_id="$1"
    local task_id="${2:-}"
    local file current_id
    file=$(task_current_file "$chat_id")
    [[ -f "$file" ]] || return 0

    if [[ -n "$task_id" ]]; then
        current_id=$(cat "$file" 2>/dev/null || true)
        [[ "$current_id" == "$task_id" ]] || return 0
    fi

    rm -f "$file"
}

task_runtime_summary() {
    local chat_id="$1"
    local task_id state note updated_at
    task_id=$(task_get_current "$chat_id") || return 1
    state=$(task_read_field "$task_id" state)
    note=$(task_read_field "$task_id" note)
    updated_at=$(task_read_field "$task_id" updated_at)

    printf '当前任务: %s\n任务状态: %s' "$task_id" "${state:-unknown}"
    if [[ -n "$updated_at" ]]; then
        printf '\n最近更新: %s' "$updated_at"
    fi
    if [[ -n "$note" ]]; then
        printf '\n状态说明: %s' "$note"
    fi
}
