# loopd

[English](README.md) | **简体中文**

`loopd` 是一个通过 Kubernetes Operator 开发和运行 Agent 编排的服务端运行时。
业务代码定义目标、协作策略与完成条件；loopd 提供 Human 与 Harness 之间的公共协作能力。
Harness 是 Operator 调用的 Agent 智能执行服务。

User、Operator 与 Harness 都是通过持久消息协作的 Actor。各参与者可以独立接收输入、
发布进展；用户可以在工作进行期间继续补充信息。

一个能力足够强的 Agent 可以执行任务，但完整的工作还可能需要等待人、读取外部系统、
组织多次执行，或在验证结果后决定下一步。loopd 让开发者用普通代码表达这些判断，
并通过进程之外的 Resource 保存领域目标与进度。

## 可以用来做什么

- **问题路由与并行执行**：为问题制定计划，调用一个或多个 Harness，将结果汇总回同一个对话。
- **人在环中的协作**：询问缺失信息或请求确认，再根据人的答复决定如何推进。
- **长期业务闭环**：定义领域 Resource，结合外部事实持续 Reconcile，自行决定角色与完成条件。

仓库内置的 Router 展示了规划、并行执行与汇总。
[LongHorizon Operator](operators/longhorizon/README.md) 展示更长的闭环：Manager 规划，
Executor 执行 CLI 工作，Auditor 检查工件，通过 Run、Execution、Audit 保存领域控制状态，
并在轮次边界接收用户补充输入。

## Router 演示

下面的用户请求分别介绍刘备、关羽、张飞。Router 先生成计划，再并行发起三个独立的
Harness 调用，最后汇总结果。主对话呈现回答，处理详情展示 `plan`、并行的 `work/*`
以及 `summarize`。

![Router 演示：主对话回答与规划、并行 Harness 调用、汇总过程](docs/router_demo.jpeg)

Router 使用配置好的同一个 Harness 目标发起这些调用，编排策略由
[Router Operator](operators/router/internal/router/router.go) 的业务代码实现。

## 如何开发 Operator

开发模型是：

```text
Loop = Resource(spec + status) + Reconcile
```

Resource 保存目标与观测到的状态，Reconciler 读取当前事实，决定执行、等待、重试还是完成。
业务开发者拥有这部分逻辑，也可以通过普通 Client 访问自己的数据库和 API。

loop-runtime 是嵌入 Operator 的 Go 开发库，与 Kubernetes controller-runtime 配合使用。
它提供会话上下文、Harness 调用、向人提问和请求确认、发布进展与回答等能力；
controller-runtime 提供资源 Watch、队列与 Reconcile 调度。

消息通过持久 Conversation（Conv）CRD 通知选定的 Actor。Operator 用 Poll 接收输入，
用 Speak 发言，处理到可安全恢复的位置后 Commit。Router 直接 Reconcile Conv；
复杂 Operator 可以创建领域 CRD，保存自己的目标、进度与完成条件。输入如何组成工作、
何时完成，由各 Operator 自己决定。

开发入口见 [Router 源码](operators/router/internal/router/router.go) 与
[Operator 开发契约](docs/runtime.md)。

## 架构

![loopd 编排架构](docs/arch_v1.svg)

- **loop-server** 保存可见的 Conversation 与 Message，通过 Conv CRD 通知选定参与者。
  用户通过 Web UI 共享历史、观察进展。
- **loop-runtime** 为业务 Operator 提供会话上下文、Human 交互、Harness 执行与结果发布能力。
- **Harness Adapter** 将运行时连接到 Agent 执行服务。内置 AgentGo Adapter 在进程内运行；
  跨 Operator 重启的执行恢复需要持久 Harness Adapter。

Conversation 保存可见协作内容，Operator Resource 保存领域状态，Harness 持有执行状态。
AgentUE 提供页面事件模型与 Redis Bridge；AgentLedger 负责完整执行事实，包括 prompt、
模型事件、工具调用、重试与成本；Hostel 提供 agent-native sandbox，承载文件、工具与计算执行。

![loopd 组件栈](docs/stack_v1.svg)

共享概念与职责归属见 [Kernel](docs/kernel.md)。

## 长期执行与恢复

浏览器断开连接不会取消执行，用户可以回到同一个 Conversation 查看进展与回答。
持久编排需要 Operator 保存领域进度，Conv 消费游标不能恢复执行状态。Harness 调用
使用业务定义的稳定幂等键，Human 交互通过各自稳定的问题身份复用。

恢复能力还取决于执行与存储配置。runtime 的 Harness Call 缓存和内置 AgentGo Adapter
都在进程内；持久 Harness Adapter 需要在重启后将同一个调用标识映射到同一个持久执行。
外部业务 API 的幂等与恢复由业务集成负责。Router 的计划与结果保存在内存中，
未消费或未提交的消息可以重新接收，但中间工作不会自动恢复。具体边界见 [Runtime](docs/runtime.md)。

## 快速开始

按照 [Kubernetes Quick Start](deploy/k8s/README.md)，使用 Helm 安装 loop-server、
Router 与 Web UI，配置 OpenAI-compatible 模型地址和凭据，打开页面后选择 Router 并提交问题。

Quick Start 默认使用临时 SQLite 与内存 Redis，对应 Pod 重建后聊天记录与事件数据会丢失。
需要持久运行时，应配置持久存储与 Harness 执行能力。

镜像构建见 [Docker 指南](deploy/docker/README.md)。
