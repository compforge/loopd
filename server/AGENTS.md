# AGENTS.md

## 项目定位与边界

server 是 loop-server 组件，数据库拥有 Conversation 与 Message 两类聊天事实，以及 Operator/Harness
在线注册。每次问答还会创建一个通用 Task CRD 作为 Reconcile 入口；Operator 领域状态、Harness 执行
状态、执行审计和成本记录不进入聊天模型。

## 代码地图与核心模块

```text
server/
├── server.go               # 组件组装与资源生命周期
├── internal/api/           # Hertz 适配；Actor 发现、Registry、Conversation、Message、Chat、Task 分文件
│   ├── actor.go            # Actor 聚合发现
│   ├── operator.go         # Operator Registry
│   └── harness.go          # Harness Registry
├── internal/delivery/      # loopd 完成语义；借助 AgentUE Bridge 续接事件并固化 Message
├── internal/migrations/    # 已有数据库的 Schema 迁移
├── internal/model/         # GORM model；一张表一个 Go 文件
│   ├── conversation.go     # conversations
│   ├── message.go          # messages
│   ├── operator.go         # operators 在线注册
│   └── harness.go          # harnesses 在线注册
├── internal/repo/          # 数据库连接及按表拆分的持久化操作
│   ├── conversation.go
│   └── message.go
├── internal/task/          # server 侧 Task CRD Kubernetes Client
├── internal/service/       # 用例层；每类能力一个 Service
│   ├── conversation.go     # ConversationService
│   ├── message.go          # MessageService
│   ├── actor.go            # Operator/Harness 注册与 Actor 聚合发现
│   ├── chat.go             # ChatService；Message 与 Task CRD 的提交边界
│   ├── human.go            # Human 消息交互、持久到期与 Task 唤醒
│   └── task.go             # TaskService；按 task_id 组装 Operator 上下文
└── docs/                   # 聊天持久化、Task 交付和在线发现的领域设计
```

## 关键约定

1. 依赖方向固定为 `api → service → repo/model`；API DTO 与 GORM model 不得跨层泄漏。
2. `model/` 一张表一个文件，`repo/` 按同名模型拆分；不要重新聚合成巨型 store 文件。
3. Message 只保存页面可见聊天；完整轨迹进入 AgentLedger，Operator 领域状态不进入 server 的表。
4. server 拥有 Task 分发与最终 Message，AgentUE 拥有事件协议和续接；事务、完成顺序与重试必须
   遵循 `docs/task-delivery.md`。存储见 `docs/persistence.md`，注册发现及 runtime 契约见
   `../docs/runtime.md`。

## References

- `../AGENTS.md` — loopd 全局边界与代码地图
- `docs/persistence.md` — Conversation 与 Message 持久化设计
- `docs/task-delivery.md` — Task 创建、上下文、流式续接与完成补偿
- `../docs/runtime.md` — Operator 协作开发契约，含注册、续租与 Actor 发现
- `../docs/kernel.md` — loopd 稳定理念和参与者边界
