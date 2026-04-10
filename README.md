# lark-agent-bridge

通过飞书 CLI 触发 AI Agent 处理任务。

## 简介

监听飞书机器人收到的消息，通过 [lark-cli](https://github.com/larksuite/cli) 事件订阅接收，转发给 AI Agent（Codex CLI 或 Claude Code）处理，并将结果回复到飞书。

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
- [Codex CLI](https://github.com/openai/codex) 或 [Claude Code](https://claude.ai/claude-code)（至少安装一个）
- 飞书机器人已开启 `im:message` 事件订阅

## 安装

### 方式一：下载二进制（推荐）

从 [Releases](https://github.com/LJW0401/lark-agent-bridge/releases) 下载对应平台的二进制文件。

### 方式二：一键安装（Linux）

```bash
./installer/linux/install.sh
```

### 方式三：deb 包安装（Ubuntu/Debian）

```bash
sudo dpkg -i lark-agent-bridge_*_amd64.deb
```

### 方式四：从源码编译

```bash
# 安装 Go 1.21+
make build          # 编译当前平台
make cross          # 交叉编译 Linux + Windows
make deb            # 构建 .deb 包
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
│   ├── feishu/                   # 飞书 API（事件订阅、消息、表情、分片）
│   ├── agent/                    # Agent 接口 + codex/claude 实现
│   ├── session/                  # 会话管理（上下文、工作目录、Agent 类型）
│   ├── task/                     # 任务状态机（per-task 锁、文件持久化）
│   ├── queue/                    # 消息队列（per-chat goroutine 串行）
│   └── commands/                 # 飞书斜杠命令处理
├── platform/                     # 跨平台服务管理 + 提权
├── installer/                    # 安装包（Linux deb/脚本、Windows Inno Setup）
├── config.example.yaml           # 配置模板
└── Makefile                      # 构建 + 交叉编译 + deb 打包
```

## 许可证

MIT
