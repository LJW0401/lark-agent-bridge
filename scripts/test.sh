#!/usr/bin/env bash
# Test script: verify each component works independently
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

if [[ -f "$PROJECT_DIR/.env" ]]; then
    source "$PROJECT_DIR/.env"
fi

CODEX_CMD="${CODEX_CMD:-codex}"

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }

echo "=== lark-agent-bridge component tests ==="
echo ""

# Test 1: lark-cli installed
echo "Test 1: lark-cli installation"
if command -v lark-cli &>/dev/null; then
    pass "lark-cli found: $(which lark-cli)"
else
    fail "lark-cli not found. Run: npm install -g @larksuite/cli"
fi

# Test 2: lark-cli auth
echo "Test 2: lark-cli authentication"
if lark-cli auth status &>/dev/null; then
    pass "lark-cli authenticated"
else
    fail "lark-cli not authenticated. Run: lark-cli auth login --recommend"
fi

# Test 3: Codex CLI installed
echo "Test 3: Codex CLI installation"
if command -v "$CODEX_CMD" &>/dev/null; then
    pass "Codex CLI found: $(which "$CODEX_CMD")"
else
    fail "Codex CLI not found. Run: npm install -g @openai/codex"
fi

# Test 4: Codex CLI responds
echo "Test 4: Codex CLI basic call"
if result=$($CODEX_CMD exec "Reply with only: OK" 2>&1); then
    pass "Codex CLI responded: ${result:0:100}"
else
    fail "Codex CLI call failed: ${result:0:100}"
fi

# Test 5: jq installed
echo "Test 5: jq installation"
if command -v jq &>/dev/null; then
    pass "jq found"
else
    fail "jq not found. Run: sudo apt install jq"
fi

echo ""
echo "=== Tests complete ==="
