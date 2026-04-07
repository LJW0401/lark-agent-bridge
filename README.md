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
解析消息内容，添加表情表示正在处理
        │
        ▼
Codex CLI / Claude Code（无头模式）
        │
        ▼
将处理结果回复到飞书，移除表情
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

## 配置说明

复制 `.env.example` 为 `.env` 并按需修改：

```bash
# AI Agent 类型：codex | claude
AGENT_TYPE=codex

# 处理中显示的表情
WORKING_EMOJI=OnIt

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
