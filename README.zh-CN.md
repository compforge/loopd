# loopd

[English](README.md) | **简体中文**

`loopd` 是一个面向 Human、Harness 与 Kubernetes Operator 协作的编排运行时。

它的核心理念是：

```text
Loop = Resource(spec + status) + Reconcile
```

每次聊天请求都会创建一个小型 loopd Task CRD，用来唤醒选定的 Actor。简单的
Operator 可以直接 Reconcile 这个 Task；复杂的 Operator 则可以创建自己的领域
CRD，保存领域状态和完成条件。loopd 通过 Conversation 和 Message 保存页面可见的
协作内容；Harness 持有自身执行状态，AgentLedger 记录完整执行历史。

![loopd 编排架构](docs/arch_v1.svg)

## 组件

- **loop-server** 拥有页面可见的 Conversation 和 Message。它为每个活跃的聊天请求
  关联一个 Task CRD，使工作不依赖某次 HTTP 请求、浏览器连接或服务进程而存在。
- **loop-runtime** 是嵌入 Operator 的 Go Client。它通过 `r.Loop.Chat` 提供稳定的
  Conversation 和 Message 能力，并允许 Reconciler 通过 `r.Loop.Task` 监听和解析
  Task。
- **Harness Adapter** 让 Operator 可以通过 loop-runtime 调用 agentd 或其他智能执行
  服务，而不会把 provider 术语泄漏到公共模型中。内置的 AgentGo Adapter 是进程内
  Demo；生产级的持久执行由 agentd 提供。

AgentUE 提供页面可见的事件模型和 Redis Bridge。AgentLedger 记录完整的 prompt、
模型事件、工具调用、重试和成本，但不充当聊天数据库。Hostel 是 agent-native
sandbox，提供文件、工具和计算环境。

公开 Conversation 中的角色固定为：

```text
user | harness | operator
```

## 运行时栈

下面的组件图展示了这套协作模型背后的职责和运行边界。loop-runtime 嵌入
Operator，AgentLedger 则横跨编排与 Agent 执行，保存完整执行事实。

![loopd 组件栈](docs/stack_v1.svg)

## 长时间运行的执行

一次问题处理可能持续几分钟，也可能持续数天。它的生命周期独立于任何 HTTP 请求
或浏览器连接：

1. loop-server 创建 user Message 和空的目标 response Message，使它们共享同一个
   `task_id`；随后初始化 AgentUE Stream，并在提交 Message 前创建同 ID 的 Task
   CRD；
2. 选定的 Operator 或 Harness 监听 Task，通过 loop-runtime 解析当前输入和
   Conversation History；
3. 复杂 Operator 可以创建领域 CRD，简单 Operator 则直接处理通用 Task；
4. 页面可见的进展通过 AgentUE Redis Event Bridge 传递，完整事件进入 AgentLedger；
5. Client 可以使用同一个 `task_id` 重连任意 loop-server 实例，并从上一个 Event
   ID 继续接收；
6. 完成时，loop-server 将可见事件折叠进选定 Actor 的 response Message，终结
   Stream，并删除 Task Marker。

`Harness.Prompt` 返回一个 Handle。Reconciler 可以一边消费它的 Stream，一边处理
其他工作；当 Harness 结果是唯一剩余依赖时，也可以调用 `Wait`。重复调用使用同一
个 `(task ID, effect key)`：AgentGo Demo 会在单个 runtime 生命周期内复用 Call，
生产环境中的 agentd Adapter 则必须让这个执行身份在 Operator 重启后仍然持久有效。
