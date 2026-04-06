# lark-agent-bridge

Use Feishu/Lark CLI to trigger AI agents for task processing.

## Overview

Listen for messages sent to a Feishu bot via [lark-cli](https://github.com/larksuite/cli) event subscription, forward them to an AI agent (Codex CLI by default), and send the results back to Feishu.

```
User sends message to Feishu Bot
        │
        ▼
lark-cli event subscribe (WebSocket)
        │
        ▼
Parse message content
        │
        ▼
Codex CLI / Claude Code (headless mode)
        │
        ▼
Send result back to Feishu chat
```

## Prerequisites

- [lark-cli](https://github.com/larksuite/cli) installed and configured
- [Codex CLI](https://github.com/openai/codex) installed
- Feishu bot with `im:message` event subscription enabled

## Quick Start

```bash
# 1. Install dependencies
npm install -g @larksuite/cli
npx skills add larksuite/cli -y -g

# 2. Configure lark-cli (first time only)
lark-cli config init
lark-cli auth login --recommend

# 3. Run the bridge
./scripts/bridge.sh
```

## Configuration

Copy `.env.example` to `.env` and fill in your settings:

```bash
cp .env.example .env
```

## License

MIT
