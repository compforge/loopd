# AGENTS.md

## 项目定位与边界

loopd 是 “Loop is a CRD” 在编排层的实现，也是 Human、Harness 与 Operator 的协作平台。
loop-server 保存跨参与者的 Conversation 与 Message，并为每次问答创建通用 Task CRD；Operator
Reconcile 该 Task，复杂 Operator 可以继续创建自己的领域 CRD。Harness 持有智能执行状态，loopd
不保存 Operator 领域表。

Conversation 中的公开角色统一为 `user`、`operator`、`harness`。Agent、Assistant、Session 等外部
概念通过 Harness Adapter 接入后，不再进入 loopd 公共模型。

## 代码地图与核心模块

```text
loopd/
├── cmd/loop-server/        # 进程配置、依赖组装与生命周期
├── config/crd/             # loopd Task CRD 安装清单
├── deploy/                 # loop-server、Router、Web 镜像与 Kubernetes Helm Chart
├── docs/                   # loopd 稳定内核与跨模块设计
├── harness/                # runtime 侧 Harness Adapter 契约；agentgo 为进程内 demo
├── operators/router/       # 首个业务 Operator；按复杂度临时编排一个或多个 Harness
├── runtime/                # Operator 使用的 Go client 与内置 Task CRD API
├── server/                 # Conversation、Message 与 HTTP 服务；细节见 server/AGENTS.md
├── web/                    # React Web；主对话与 Operator 执行详情的三栏协作界面
└── *.go                    # 跨 server、runtime 和 harness 的公共协作模型
```

## 关键约定

1. Conversation History 属于 loop-server，不属于某一个 Operator 或 Harness；同一 Conversation 可由
   多个参与者先后协作。
2. loop-server 的聊天事实只有 Conversation 与 Message。Message 通过 `task_id + kind + key + content`
   表达 Human、Operator 或 Harness 的一条页面可见发言；`content` 是 AgentUE semantic model，使用
   可扩展 blocks 承载文本、可见工具状态和产物，不混入完整执行轨迹。
3. `conversations` 与 `messages` 均使用 go-stdx 生成的 UUIDv7 `id` 作为主键和游标；不再维护平行的
   message sequence。
4. 一次问答由 user Message、目标 Actor 的 response Message 和同 ID 的 Task CRD 组成。服务端在数据库事务提交前
   初始化 AgentUE Redis Stream 并创建 CRD；任一步失败则回滚两条 Message，并尽力删除已创建的外部资源。
   回答完成并固化后删除 Task marker，避免 Operator 重启时再次处理已经结束的工作。
5. 主会话的 `parent_message_id` 为空；Operator 内部工作会话通过该字段引用主链路 response Message。
   v1 不允许详情会话继续嵌套，且同一条 Message 最多关联一个详情会话。
6. v1alpha1 Task CRD 当前保存路由和唤醒信息，详细上下文由 runtime 按 Task ID 向 server 查询。公共协调
   字段可按 Kubernetes API 兼容规则增量演进；领域状态复杂时，Operator 创建并拥有自己的 CRD。
7. AgentLedger 承载完整执行历史、审计和成本记录，不替代 Conversation 与 Message 的页面业务存储。
8. 公开仓内容必须脱敏，不得提交内部链接、凭据或仅在公司环境成立的配置。
9. 修改 `runtime/api/` 下的 CRD 类型后运行 `make generate manifests`，并提交生成的 DeepCopy、基础 CRD
   YAML 与 Helm Chart 中同步的 CRD YAML。
10. Operator 通过 loop-runtime 调用 Harness；可见 `set/append` 事件由 runtime 发送给任意 loop-server
    实例，再进入共享 Redis Stream。AgentGo Adapter 只用于进程内演示，生产级执行恢复由 agentd 持有。

## References

- `docs/kernel.md` — loopd 稳定模型、主流程和扩展边界
- `deploy/docker/README.md` — loop-server、Router 与 Web 镜像构建入口
- `deploy/k8s/README.md` — Helm Quick Start、组件拓扑与配置边界
- `server/docs/persistence.md` — Conversation 与 Message 的数据库约束
- `README.md` — 产品定位与使用入口
