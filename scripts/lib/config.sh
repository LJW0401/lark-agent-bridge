#!/usr/bin/env bash
# config.sh — 环境变量加载和目录初始化

AGENT_TYPE="${AGENT_TYPE:-codex}"
CODEX_CMD="${CODEX_CMD:-codex}"
CODEX_SKIP_CHECK="--skip-git-repo-check"
CLAUDE_CMD="${CLAUDE_CMD:-claude}"
WORKING_EMOJI="${WORKING_EMOJI:-OnIt}"
ERROR_EMOJI="${ERROR_EMOJI:-Frown}"
MAX_RETRIES="${MAX_RETRIES:-3}"
SESSION_TIMEOUT="${SESSION_TIMEOUT:-0}"
STREAM_INTERVAL="${STREAM_INTERVAL:-3}"
LOG_FILE="$(cd "$PROJECT_DIR" && realpath -m "${LOG_FILE:-./logs/bridge.log}")"
SESSION_DIR="${PROJECT_DIR}/.sessions"
WORKSPACE_DIR="${WORKSPACE_DIR:-$PROJECT_DIR}"
PID_DIR="${PROJECT_DIR}/.pids"
QUEUE_DIR="${PROJECT_DIR}/.queue"

mkdir -p "$(dirname "$LOG_FILE")" "$SESSION_DIR" "$PID_DIR" "$QUEUE_DIR"

# Reset stale state from previous run (depth counters, PID files)
rm -f "$QUEUE_DIR"/*.depth "$PID_DIR"/*

# Clean up all child processes on exit (runs only once)
cleanup() {
    trap - EXIT TERM INT
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Cleaning up child processes..." >> "$LOG_FILE"
    kill 0 2>/dev/null || true
}
trap cleanup EXIT TERM INT

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG_FILE"
}
