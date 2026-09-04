# AGENTS.md

## 项目定位与边界

server 是 loop-server 组件，数据库只拥有 Conversation 与 Message 两类聊天事实。每次问答还会创建一个
通用 Task CRD 作为 Reconcile 入口；Operator 领域状态、Harness 运行状态、执行审计和成本记录不进入
聊天模型。

## 代码地图与核心模块

```text
server/
├── server.go               # 组件组装与资源生命周期
├── internal/api/           # Hertz 适配；Conversation、Message、Chat、Task handler 分文件
├── internal/delivery/      # loopd 完成语义；借助 AgentUE Bridge 续接事件并固化 Message
├── internal/model/         # GORM model；一张表一个 Go 文件
│   ├── conversation.go     # conversations
│   └── message.go          # messages
├── internal/repo/          # 数据库连接及按表拆分的持久化操作
│   ├── conversation.go
│   └── message.go
├── internal/task/          # server 侧 Task CRD Kubernetes Client
├── internal/service/       # 用例层；每类能力一个 Service
│   ├── conversation.go     # ConversationService
│   ├── message.go          # MessageService
│   ├── chat.go             # ChatService；Message 与 Task CRD 的提交边界
│   └── task.go             # TaskService；按 task_id 组装 Operator 上下文
└── docs/persistence.md     # 两表 schema 与 UUIDv7 游标约束
```

## 关键约定

1. 依赖方向固定为 `api → service → repo/model`；API DTO 与 GORM model 不得跨层泄漏。
2. `model/` 一张表一个文件，`repo/` 按同名模型拆分；不要重新聚合成巨型 store 文件。
3. Conversation 使用 `name`。Message 使用 `task_id + kind + key + content`：kind 只能是 `user`、
   `operator`、`harness`，key 是该参与者的稳定标识，content 是页面可见的 AgentUE semantic model JSON，
   不是完整执行日志。
4. 两张表的 `id` 都由 service 使用 go-stdx `uuid.V7()` 生成。消息按 UUIDv7 字典序读取和翻页，不增加
   `sequence` 字段。
5. 一次 Chat 请求由 `ChatService` 在数据库事务中创建 user Message 和目标 Actor 的空 response Message，再以
   `task_id` 初始化 AgentUE Redis Stream 并创建 Task CRD；任一步失败则事务回滚。外部资源创建成功但 DB
   commit 失败时，server 尽力删除 Stream 与 CRD。完成时先固化 Message 并终结 Stream，再幂等删除 Task
   marker；删除失败必须返回可重试错误。
6. `conversations.parent_message_id` 只用于把 Operator 详情会话挂到主链路 response Message。主会话为空，
   详情会话不可继续嵌套，同一 Message 最多对应一个详情会话。
7. 页面不可见的 prompt、tool call/result、重试和成本等完整轨迹进入 AgentLedger，不扩张 Message schema。
8. Task 查询是基于主 Conversation Message 的实时视图，不增加 `tasks` 表；详情 Conversation 中共享
   `task_id` 的内部消息不得覆盖主链路的 input/response。
9. `runtime/api/` 是公共 CRD API，字段按 Kubernetes API 兼容规则增量演进；修改类型后运行
   `make generate manifests`，提交生成的 DeepCopy 和 CRD YAML。
10. AgentUE 拥有事件协议、Reducer、Redis Stream 与续接；server 拥有 Redis client 生命周期、Task 路由和
    最终 Message 快照。Redis 中的事件是活跃任务投影，不替代 AgentLedger 的完整执行记录。
11. Task event API 接受 Operator/runtime 发布的 AgentUE `set/append`。返回的 event ID 是 Redis/SSE
    transport identity，仅用于 `Last-Event-ID` 续接；语义顺序和幂等由 event data 内的 AgentUE `seq` 表达。

## References

- `../AGENTS.md` — loopd 全局边界与代码地图
- `docs/persistence.md` — Conversation 与 Message 持久化设计
- `../docs/kernel.md` — loopd 稳定理念和参与者边界
