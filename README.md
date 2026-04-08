# lark-agent-bridge

通过飞书 CLI 触发 AI Agent 处理任务。

## 简介

监听飞书机器人收到的消息，通过 [lark-cli](https://github.com/larksuite/cli) 事件订阅接收，转发给 AI Agent（默认使用 Codex CLI）处理，并将结果回复到飞书。

```
用户向飞书机器人发送消息
        │
        ▼
lark-cli 事件订阅（WebSocket）
        │
        ▼
消息队列（同一会话串行，跨会话并行）
        │
        ▼
解析消息 → 添加表情 → 创建占位回复
        │
        ▼
Codex CLI / Claude Code（无头模式）
        │（实时轮询输出，更新回复消息）
        ▼
最终结果渲染为 Markdown 富文本回复，移除表情
```

## 前置要求

- [lark-cli](https://github.com/larksuite/cli) 已安装并配置
- [Codex CLI](https://github.com/openai/codex) 已安装
- 飞书机器人已开启 `im:message` 事件订阅
- jq（JSON 解析工具）

## 快速开始

```bash
# 1. 安装依赖
npm install -g @larksuite/cli
npx skills add larksuite/cli -y -g
sudo apt install jq

# 2. 配置 lark-cli（仅首次需要）
lark-cli config init
lark-cli auth login --recommend

# 3. 配置环境变量
cp .env.example .env

# 4. 运行测试
./scripts/test.sh

# 5. 启动桥接服务
./scripts/bridge.sh
```

## 可用命令

在飞书聊天中向机器人发送以下命令：

| 命令 | 说明 |
|------|------|
| `/help`（或 `帮助`） | 显示可用命令列表 |
| `/status` | 查看当前会话状态（Agent 类型、工作目录、会话时长、超时设置） |
| `/agent` | 查看当前 Agent 类型 |
| `/agent codex` 或 `/agent claude` | 切换 Agent 类型 |
| `/workspace` | 查看当前工作目录 |
| `/workspace <path>` | 切换 Agent 工作目录 |
| `/cancel` | 取消正在进行的请求 |
| `/new`（或 `新对话`） | 创建新会话，开始新对话 |

以 `/` 开头的未知命令会被拦截并提示使用 `/help`。其他普通消息会直接转发给 AI Agent 处理。

## 会话管理

服务支持多轮对话上下文保持：

- 每个飞书会话（chat_id）独立维护会话上下文
- 上下文会持续保留直到超时或手动清除（发送 `/new`）
- 切换 Agent 类型时会自动清除当前会话上下文
- 超时时间通过 `SESSION_TIMEOUT` 配置，默认为 `0`（永不超时）

## 配置说明

复制 `.env.example` 为 `.env` 并按需修改：

```bash
# AI Agent 类型：codex | claude
AGENT_TYPE=codex

# Codex CLI 命令（默认: codex）
CODEX_CMD=codex

# Claude Code 命令（默认: claude）
CLAUDE_CMD=claude

# 处理中显示的表情
WORKING_EMOJI=OnIt

# 出错时显示的表情
ERROR_EMOJI=Frown

# 会话超时（秒），0 表示永不超时
SESSION_TIMEOUT=0

# 流式更新轮询间隔（秒）
STREAM_INTERVAL=3

# Agent 默认工作目录
WORKSPACE_DIR=.

# 日志文件路径
LOG_FILE=./logs/bridge.log
```

## 服务管理

使用 systemd 管理服务，支持开机自启：

```bash
# 安装服务
./services/bridge_service.sh install

# 启动 / 停止 / 重启
./services/bridge_service.sh start
./services/bridge_service.sh stop
./services/bridge_service.sh restart

# 查看状态 / 跟踪日志
./services/bridge_service.sh status
./services/bridge_service.sh logs

# 卸载服务
./services/bridge_service.sh uninstall
```

## 项目结构

```
lark-agent-bridge/
├── README.md               # 项目说明
├── TODO.md                 # 功能规划清单
├── .env.example            # 配置模板
├── .gitignore
├── scripts/
│   ├── bridge.sh           # 核心：监听消息 → 调用 Agent → 回复
│   └── test.sh             # 组件测试脚本
└── services/
    └── bridge_service.sh   # systemd 服务管理
```

## 许可证

MIT
