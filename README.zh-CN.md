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
- **loop-runtime** 是嵌入 Operator 的 Go 协作开发库，将 controller-runtime 的资源控制
  循环与 loopd 协作能力组合起来。Operator 开发契约统一见 [Runtime](docs/runtime.md)。
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

一次问题处理可能持续几分钟，也可能持续数天。用户可以断开页面，之后回到同一个
Conversation 查看进展和回答。选中的 Operator 或 Harness 负责推进工作；Operator 可以
调用多个 Harness，再汇总为自己的回答。

恢复能力取决于执行与存储配置。内置 AgentGo Demo 在进程内运行，跨 Operator 重启的
执行恢复需要持久 Harness Adapter。调用与恢复契约见 [Runtime](docs/runtime.md)，
存储配置与 Quick Start 限制见 [Kubernetes 部署](deploy/k8s/README.md)。
