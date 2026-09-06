# AGENTS.md

## 项目定位与边界

server 是 loop-server 组件，数据库拥有 Conversation 与 Message 两类聊天事实，以及 Operator/Harness
在线注册。DB 消息记录支持 Actor 独立消费，Conv CRD 保存定向信号和消费进度，Redis 支持页面
流与重连；task_id 仅标识页面交付。Operator 领域状态、Harness 执行状态、执行审计和成本记录
不进入聊天模型。

## 代码地图与核心模块

```text
server/
├── server.go               # 组件组装与资源生命周期
├── internal/api/           # Hertz 适配；Actor 发现、Registry、Conversation、Message、Chat、Poll 分文件
│   ├── actor.go            # Actor 聚合发现
│   ├── operator.go         # Operator Registry
│   └── harness.go          # Harness Registry
├── internal/view/          # API 与 service 共用的 View Model；按领域拆文件，仅定义数据结构
├── internal/domain/        # Human 消息的纯状态规则，不持有独立存储
├── internal/component/     # 有生命周期的运行组件
│   ├── message_gc.go       # 随 server 启停的全局 Message 失活回收
│   └── conv_listener.go    # 随 stream 请求启停的单 Conv 监听
├── internal/delivery/      # Message 输出固化与独立 Redis 流写入
├── internal/migrations/    # 已有数据库的 Schema 迁移
├── internal/model/         # GORM model；一张表一个 Go 文件
│   ├── conversation.go     # conversations
│   ├── message.go          # messages
│   ├── message_part.go     # message_parts，Message 内部的物理正文分片
│   ├── operator.go         # operators 在线注册
│   └── harness.go          # harnesses 在线注册
├── internal/repo/          # 数据库连接及按表拆分的持久化操作
│   ├── conversation.go
│   ├── message.go
│   └── message_part.go     # 内联/引用装箱、定点更新与一致性展开
├── internal/conversation/  # Conv CRD 定向唤醒与参与者接收游标
├── internal/service/       # 用例层；每类能力一个 Service
│   ├── conversation.go     # ConversationService
│   ├── message.go          # MessageService
│   ├── message_enrichment.go # 页面消息富化；分页不变、直接引用与卡片投影
│   ├── actor.go            # Operator/Harness 注册与 Actor 聚合发现
│   ├── chat.go             # ChatService；输入提交与消息输出
│   ├── poll.go             # DB 消息接收、提交后通知与重试
│   └── human.go            # Human 消息交互、持久到期与类型化答复
└── docs/                   # 消息消费、可见事实持久化与用户交互的领域设计
```

## 关键约定

1. API 适配用例，service 协调生命周期，domain 表达纯状态规则，repo/model 负责持久化。
   状态规则不依赖 GORM 或 HTTP；Message 是问题与答复的唯一事实来源。
   `view/` 与 `api/`、`service/` 平级，按领域存放 View Model；API 与 service 可依赖它，
   View Model 不依赖 handler、service 或 repo。
2. `model/` 一张表一个文件，`repo/` 按同名模型拆分；不要重新聚合成巨型 store 文件。
3. Message 只保存页面可见聊天；完整轨迹进入 AgentLedger，Operator 领域状态不进入 server 的表。
4. server 拥有 Conv 定向通知与可见 Message，AgentUE 拥有事件协议和续接；事务、完成顺序与重试必须
   遵循 `docs/ue.md` 的页面交付契约。存储见 `docs/persistence.md`，注册发现及 runtime 契约见
   `../docs/runtime.md`。

## References

- `../AGENTS.md` — loopd 全局边界与代码地图
- `docs/persistence.md` — Conversation 与 Message 持久化设计
- `docs/conversation.md` — Conv 定向唤醒、Poll/Commit 消费位置和恢复边界
- `docs/ue.md` — 页面布局、消息呈现、交互卡片与页面交付；不定义业务完成
- `../docs/runtime.md` — Operator 协作开发契约，含注册、续租与 Actor 发现
- `../docs/kernel.md` — loopd 稳定理念和Actor 边界
