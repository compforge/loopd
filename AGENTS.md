# AGENTS.md

## 项目定位与边界

loopd 是 “Loop is a CRD” 在编排层的实现，也是 Human、Harness 与 Operator 的协作平台。
loop-server 保存跨参与者的 Conversation 与 Message；Operator 通过自己的 CRD/Reconcile 持有领域
Loop 和长期执行状态；Harness 持有智能执行状态。loopd 不定义统一 Task CRD，也不保存 Operator
领域表。

Conversation 中的公开角色统一为 `user`、`operator`、`harness`。Agent、Assistant、Session 等外部
概念通过 Harness Adapter 接入后，不再进入 loopd 公共模型。

## 代码地图与核心模块

```text
loopd/
├── cmd/loop-server/        # 进程配置、依赖组装与生命周期
├── docs/                   # loopd 稳定内核与跨模块设计
├── harness/                # Harness Adapter 公共契约与具体适配
├── runtime/                # Operator Reconciler 使用的 Go client
├── server/                 # Conversation、Message 与 HTTP 服务；细节见 server/AGENTS.md
└── *.go                    # 跨 server、runtime 和 harness 的公共协作模型
```

## 关键约定

1. Conversation History 属于 loop-server，不属于某一个 Operator 或 Harness；同一 Conversation 可由
   多个参与者先后协作。
2. loop-server 的聊天事实只有 Conversation 与 Message。Message 通过 `kind + key + content` 表达
   Human、Operator 或 Harness 的一条发言；`content` 是 AgentUE semantic model，使用可扩展 blocks
   同时承载文本、工具调用和产物，不混入执行状态和审计事件。
3. `conversations` 与 `messages` 均使用 go-stdx 生成的 UUIDv7 `id` 作为主键和游标；不再维护平行的
   message sequence。
4. 主会话的 `parent_message_id` 为空；Operator 内部工作会话通过该字段引用触发它的主链路 Message。
   v1 不允许详情会话继续嵌套，且同一条 Message 最多关联一个详情会话。
5. Operator 的 CRD、领域状态、完成条件和外部事实由 Operator 自己拥有；loop-server 不复制这些表。
6. AgentLedger 后续只承载执行事实、审计和成本记录，不替代 Conversation 与 Message 的业务存储。
7. 公开仓内容必须脱敏，不得提交内部链接、凭据或仅在公司环境成立的配置。

## References

- `docs/kernel.md` — loopd 稳定模型、主流程和扩展边界
- `server/docs/persistence.md` — Conversation 与 Message 的数据库约束
- `README.md` — 产品定位与使用入口
