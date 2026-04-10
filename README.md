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
创建任务状态 → 维护 depth 计数 → 进入会话队列
        │
        ▼
解析消息 → 添加表情 → 创建占位回复
        │
        ▼
Codex CLI / Claude Code（无头模式）
        │（实时轮询输出，带重试更新回复消息）
        ▼
最终结果渲染为 Markdown 富文本回复
        │
        ├─ 更新失败时降级为普通消息发送
        └─ 完成后移除表情并写回任务状态
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
| `/status` | 查看当前会话状态（Agent 类型、工作目录、会话时长、超时设置、当前任务状态） |
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

## 任务状态与容错

服务会为每条消息维护显式任务状态，典型状态包括：

- `queued`
- `starting`
- `running`
- `cancelling`
- `cancelled`
- `completed`
- `failed`

这套状态机主要用于：

- 避免仅靠 PID 文件推断任务状态
- 让 `/cancel`、`/status` 和最终结果回写基于同一份任务元数据
- 在同一会话高并发消息下，通过 `task` 锁和 `depth` 锁减少状态竞争

当前错误处理与降级策略包括：

- 飞书消息更新失败时自动重试
- 占位回复创建失败时，最终结果降级为普通文本消息发送
- Agent 返回过长时，按消息长度自动拆分为连续多条回复，避免后半段被截断
- Agent 工作目录不存在或执行失败时，写入明确错误并回传失败状态

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

# 单条飞书消息的最大字符数，超长回复会自动分片连续发送
FEISHU_MESSAGE_LIMIT=4000

# Agent 默认工作目录
WORKSPACE_DIR=.

# 日志文件路径
LOG_FILE=./logs/bridge.log
```

服务启动时会在日志中输出当前项目最近一次修改时间，便于确认部署版本。

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
│   ├── bridge.sh           # 入口：加载模块 + 主循环
│   ├── test.sh             # 组件测试脚本
│   └── lib/
│       ├── config.sh       # 环境变量、日志、cleanup
│       ├── feishu.sh       # 飞书 API（消息、表情、更新）
│       ├── task.sh         # 任务状态机（task 元数据、状态转移、锁）
│       ├── session.sh      # 会话管理（session、workspace、取消）
│       ├── agent.sh        # Agent 调用（codex / claude）
│       ├── queue.sh        # 消息队列、depth 计数和流式处理
│       └── commands.sh     # 斜杠命令处理
└── services/
    └── bridge_service.sh   # systemd 服务管理
```

## 许可证

MIT
