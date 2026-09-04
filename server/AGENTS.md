# AGENTS.md

## 项目定位与边界

loopd 是 “Loop is a CRD” 在编排层的开源实现，也是 Human、Harness 与 Operator 的协作平台。
loop-server 持有跨参与者的协作事实，loop-runtime 为 Operator Reconciler 提供 Go API；领域 CRD、
拆解策略、外部事实与完成条件由各 Operator 自己拥有。

loopd 公共模型统一使用 Harness，不再建立与 Harness 平行的 Agent 概念。外部 Agent、Assistant 或
Session 产品通过 Adapter 接入后均表现为 Harness。

## 代码地图与核心模块

```text
├── api/                    # loop-server 的公开 HTTP 数据模型
├── cmd/loop-server/        # Hertz 服务组装与进程生命周期
├── harness/                # 可替换 Harness provider 边界
├── runtime/                # Operator Reconciler 使用的 Go client
├── server/                 # 持久化、HTTP、AgentUE 投影与 Harness 调度
│   └── docs/persistence.md # 数据库和恢复语义
└── docs/kernel.md          # 稳定内核、参与者与 ownership
```

## 关键约定

1. Conversation History 属于 loop-server，不属于某一个 Operator 或 Harness；同一会话可以切换
   responder。
2. loop-server 不保存 Operator 领域表，也不要求拥有自己的 CRD；Operator 只在 Invocation 上绑定
   自己的 Resource 引用。
3. `Harness.Prompt` 返回持久 Call handle。同一 `owner UID + effect key` 的重复请求必须恢复同一次
   执行；参数变化必须冲突。
4. Invocation 与 Harness Call 可以运行数天，不能依赖 HTTP/SSE 连接存活。SSE 每次连接均从持久
   snapshot 开始，并以数据库 cursor 继续观察。
5. Provider 调用必须是短时间、带 timeout 的 ensure/observe；实际 Harness 执行由 provider 持久
   化。结果不确定时 fail closed，不可盲目重复触发。
6. Operator 内部 Harness 输出默认只进入 Invocation 详情；只有 Operator 汇总发布的内容才成为主
   对话回答。
7. agentledger 仅可作为审计/成本记录 sink，不能替代 Message、Invocation、Interaction 或 Call 的
   业务存储。

## References

- `../docs/kernel.md` — loopd 稳定模型、主流程和扩展边界
- `docs/persistence.md` — loop-server 表、事务、游标与恢复语义
- `../README.md` — 产品定位与最短开发入口
