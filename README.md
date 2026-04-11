# lark-agent-bridge

通过飞书 CLI 触发 AI Agent 处理任务。

## 简介

监听飞书机器人收到的消息，通过 [lark-cli](https://github.com/larksuite/cli) 事件订阅接收，转发给 AI Agent（Codex CLI 或 Claude Code）处理，并将结果以流式卡片（打字机效果）回复到飞书。

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
创建任务状态 → 添加工作表情 → 发送状态消息 + 流式卡片
        │
        ▼
Codex CLI / Claude Code（无头模式）
        │（通过 cardkit API 实时流式推送，打字机效果）
        ▼
最终结果写入卡片 → 状态消息更新为完成 → 添加完成表情
```

## 前置要求

- [lark-cli](https://github.com/larksuite/cli) 已安装并配置
- [Codex CLI](https://github.com/openai/codex) 或 [Claude Code](https://claude.ai/claude-code)（至少安装一个）
- 飞书机器人已开启 `im:message` 事件订阅

## 安装

### 方式一：下载二进制（推荐）

从 [Releases](https://github.com/LJW0401/lark-agent-bridge/releases) 下载对应平台的二进制文件。

### 方式二：从源码编译

```bash
# 安装 Go 1.21+
make build          # 编译当前平台
make cross          # 交叉编译 Linux + Windows
```

## 快速开始

```bash
# 1. 安装 lark-cli（如未安装）
npm install -g @larksuite/cli
npx skills add larksuite/cli -y -g

# 2. 配置 lark-cli（仅首次需要）
lark-cli config init
lark-cli auth login --recommend

# 3. 配置桥接服务（交互式向导，自动探测命令路径）
lark-agent-bridge config init

# 4. 启动服务
lark-agent-bridge
```

首次运行如果没有配置文件，会自动进入配置向导。

## 飞书聊天命令

在飞书聊天中向机器人发送以下命令：

| 命令 | 说明 |
|------|------|
| `/help`（或 `帮助`） | 显示可用命令列表 |
| `/status` | 查看当前会话状态 |
| `/agent` | 查看当前 Agent 类型 |
| `/agent codex` 或 `/agent claude` | 切换 Agent 类型 |
| `/workspace` | 查看当前工作目录 |
| `/workspace <path>` | 切换 Agent 工作目录 |
| `/cancel` | 取消正在进行的请求 |
| `/new`（或 `新对话`） | 创建新会话，开始新对话 |

以 `//` 开头可将 `/` 命令发给 Agent，如 `//help`。

## 消息支持

| 消息类型 | 支持 | 说明 |
|---------|------|------|
| 纯文本 | ✅ | 直接提取文字 |
| 富文本（post） | ✅ | 提取文字、链接、@提及、表情、代码块等，跳过图片和媒体 |
| 图片/视频/文件 | ❌ | 暂不支持 |

## 群聊行为

- **私聊**：所有消息直接处理
- **群聊**：仅当 @机器人 时才响应，未被 @ 的消息静默忽略
- @机器人 的占位符自动删除，@其他用户 替换为真实姓名
- 群聊消息自动注入群名、成员数等上下文信息
- 当消息中 @了其他用户时，Agent 会获得该用户的 open_id 和 lark-cli 工具提示，可用于创建日程等操作

## 回复方式

服务使用飞书流式卡片回复，支持打字机效果：

- **状态消息**：实时显示处理时间（⏳ 正在处理... → ✅ 处理完成）
- **结果卡片**：Agent 输出以打字机动画逐字呈现，支持 Markdown 渲染
- **表情反馈**：处理中显示 OnIt 表情，完成后替换为 Done 表情
- **降级策略**：流式卡片创建失败时自动降级为普通消息更新
- **上下文注入**：群聊消息自动携带群聊信息和被 @用户 的身份信息

## 配置管理

使用 `config` 子命令管理配置，无需手动编辑文件：

```bash
lark-agent-bridge config init            # 交互式配置向导
lark-agent-bridge config list            # 列出所有配置项及当前值
lark-agent-bridge config get <key>       # 读取配置项
lark-agent-bridge config set <key> <val> # 设置配置项
lark-agent-bridge config path            # 显示配置文件路径
```

### 可配置项

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `agent.type` | `codex` | AI Agent 类型（codex / claude） |
| `agent.codex_cmd` | `codex` | Codex CLI 命令路径 |
| `agent.claude_cmd` | `claude` | Claude Code 命令路径 |
| `feishu.lark_cli_cmd` | `lark-cli` | lark-cli 命令路径 |
| `feishu.working_emoji` | `OnIt` | 处理中表情 |
| `feishu.done_emoji` | `Done` | 完成表情 |
| `feishu.error_emoji` | `Frown` | 错误表情 |
| `stream.interval` | `3` | 流式更新间隔（秒） |
| `stream.message_limit` | `4000` | 单条消息字符上限 |
| `session.timeout` | `0` | 会话超时（秒），0=永不超时 |
| `workspace.dir` | `.` | 默认工作目录 |
| `log.file` | `./logs/bridge.log` | 日志文件路径 |
| `retry.max_retries` | `3` | 飞书 API 最大重试次数 |

## 服务管理

支持 Linux systemd 和 Windows Service：

```bash
lark-agent-bridge install     # 安装为系统服务（自动 sudo/UAC 提权）
lark-agent-bridge start       # 启动服务
lark-agent-bridge stop        # 停止服务
lark-agent-bridge restart     # 重启服务
lark-agent-bridge status      # 查看服务状态
lark-agent-bridge logs [-f]   # 查看服务日志
lark-agent-bridge uninstall   # 卸载服务
```

## 会话管理

- 每个飞书会话（chat_id）独立维护会话上下文
- 上下文持续保留直到超时或手动清除（`/new`）
- 切换 Agent 类型时自动清除当前会话上下文
- `/cancel` 取消任务后自动清除会话（SIGINT 优雅退出）
- 超时时间通过 `session.timeout` 配置，默认永不超时

## 任务状态机

每条消息维护显式任务状态：

```
queued → starting → running → completed
                         ↘ → cancelling → cancelled
                         ↘ → failed
```

用于 `/cancel`、`/status` 和结果回写的统一状态管理。

## 项目结构

```
lark-agent-bridge/
├── cmd/bridge/main.go            # 入口：子命令路由 + 主循环
├── internal/
│   ├── config/                   # 配置加载、日志、config 子命令、向导
│   ├── feishu/                   # 飞书 API（事件订阅、消息、表情、流式卡片）
│   ├── agent/                    # Agent 接口 + codex/claude 实现
│   ├── session/                  # 会话管理（上下文、工作目录、Agent 类型）
│   ├── task/                     # 任务状态机（per-task 锁、文件持久化）
│   ├── queue/                    # 消息队列（per-chat goroutine 串行）
│   │                             # feishu 包含富文本解析、@mention 处理、prompt 构建
│   └── commands/                 # 飞书斜杠命令处理
├── platform/                     # 跨平台服务管理 + 提权
├── config.example.yaml           # 配置模板
├── Makefile                      # 构建 + 交叉编译
└── CLAUDE.md                     # Claude Code 项目指令
```

## 许可证

MIT
