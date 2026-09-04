# loopd Kernel

本文定义 loopd 的最小稳定内核：它位于 Agent 技术体系的哪一层，协调哪些参与者，哪些事实由谁
拥有，以及一条用户请求如何经由 Harness 或 Operator 返回结果。具体 HTTP 路由、数据库表、CRD
字段和 Harness 协议不属于本文；它们应当服从这里的模型，而不是反过来定义内核。

## 1. 定位

loopd 的定位分为三个维度：

- **理念上，Loop is a CRD**：需要跨时间持续收敛的业务 Loop，由领域 Resource 的
  `spec/status` 与 Reconcile 承载。
- **技术层次上，loopd 属于 Orchestrator 编排层**：它保存协作状态，连接人、领域控制循环和智能
  执行，但不实现模型推理、工具循环或 Sandbox。
- **实现上，loopd 是 Human、Harness 与 Operator 的 server-native 协作平台**。

一句话概括：

> Human 在持久 Conversation 中向 Harness 或 Operator 提出目标；Operator 通过 CRD/Reconcile
> 持续推进领域 Loop，按需调用 Harness，并把过程与结果带回同一 Conversation。

loopd 在四层 Agent 技术体系中的位置如下：

```text
Interface       loop-server 的 Chat API、Web 页面、Activity 详情与 Interaction
Orchestrator    loop-server、loop-runtime，以及 Operator 与 Harness 的协调边界
Agent Loop      由 loopd 外部接入的 Harness 承载
Infrastructure  模型、Sandbox、文件与计算环境
```

loop-server 提供必要的 Interface，但 loopd 的主体仍是 Orchestrator。Interface 是协作状态面向 Human
的输入面和投影面，不拥有编排事实。

## 2. 三类参与者

loopd 的公开协作角色只有三类：

| 参与者 | 职责 |
|---|---|
| **Human** | 提出目标、选择 responder、补充上下文、回答 Interaction，并判断结果是否满足需要 |
| **Harness** | 接受 prompt 与 tools，执行一次可流式观察的智能任务；既可直接回答 Human，也可被 Operator 调用 |
| **Operator** | 拥有领域 CRD、Reconcile、外部事实读取和完成条件；决定何时调用 Harness、询问 Human、等待或结束 |

Conversation 中的公开消息角色相应为：

```text
user | harness | operator
```

这里描述的是 loopd 的协作角色，不是模型协议中的 `user/assistant/system/tool`。Harness Adapter 在调用
具体实现时负责协议转换，模型协议角色不得反向进入 loopd 的公共模型。

外部系统可能把某个执行目标称为 Agent、Assistant 或 Session；接入 loopd 后统一表现为 Harness。
loopd Core、公共 API、数据库和 UI 不再建立一套与 Harness 平行的 Agent 概念。

## 3. 核心协作对象

三类参与者通过少量具有稳定身份和生命周期的对象协作：

| 概念 | 语义与 owner |
|---|---|
| **Conversation** | loop-server 拥有的持久协作空间；历史属于 Conversation，不属于某个 Operator 或 Harness |
| **Message** | Conversation 中一次有序表达；author role 为 `user`、`operator` 或 `harness` |
| **Invocation** | 一条用户 Message 请求某个 Operator 或 Harness 作答的处理实例；连接输入、过程和最终回答 |
| **Harness Call** | 一次可恢复、可流式观察的 Harness 执行；直接问答通常有一个 Call，Operator 可以拥有零到多个 Call |
| **Interaction** | Operator 或 Harness 等待 Human 给出 typed answer/confirmation 的持久协作对象 |

Activity、Artifact 和各种流式事件是这些对象的过程记录或投影，不与它们平级：

- **Activity** 展示一次 Invocation 内部正在发生什么，例如 Operator 阶段、Harness Call 和工具执行；
- **Artifact** 引用文本之外的结果，例如文件、图片或报告；
- 流式事件说明 Harness Call 仍有反应，不代表 Call 已经完成。

`loop-server` 和 `loop-runtime` 是平台组件，不是新的协作角色。前者拥有服务端协作状态，后者是供
Operator Reconciler 使用的 Go API，不单独拥有业务事实。

一次 Invocation 可能持续几十分钟乃至数天。它的业务生命周期必须独立于 HTTP request、SSE 连接、
浏览器页面和某一个 loop-server 进程；这些都只是创建、观察或恢复持久执行的临时通道。

## 4. Loop is a CRD

loopd 将需要长期收敛的领域 Loop 表达为：

```text
Loop = Resource(spec + status) + Reconcile
```

- `spec` 表达 Human 认可的目标和约束；
- `status` 表达 Operator 对现实世界的最新观测；
- Reconciler 每次读取最新目标、状态和外部事实，判断应该继续、等待、重试、询问 Human 还是结束；
- Event 只提示事实可能变化，不能代替 Reconciler 重新读取权威状态。

CRD 的 schema、子资源、领域状态和完成条件全部由 Operator 拥有。LongHorizon 可以定义 Task、Manager、
Executor 和 Auditor，其他 Operator 可以采用完全不同的 Resource；这些概念都不进入 loopd Core。

loopd 不要求拥有自己的 CRD。Conversation、Message、Invocation、Interaction、Harness Call 的协调记录
由 loop-server 持久化；Kubernetes 只承担 Operator 领域 Resource 的声明、Watch 和 Reconcile。

“Loop is a CRD”也不意味着每次聊天都必须创建 CRD。Human 直接请求 Harness 是基础协作路径；只有
请求进入 Operator 所拥有的长期领域 Loop 时，才转换为相应的根 Resource。

## 5. 两条主流程

### 5.1 Human 直接请求 Harness

```text
Human
  → append user Message
  → create Invocation(target = Harness)
  → start or resume durable Harness Call
  → project stream into the Conversation
  → append harness answer Message
```

Harness 的流式文本可以直接构成主回答，工具执行和诊断信息则进入当前 Invocation 的 Activity。一次 Call
耗时很久不等于没有响应；进行中、等待输入、完成和失败必须是可区分的事实。

### 5.2 Human 请求 Operator

```text
Human
  → append user Message
  → create Invocation(target = Operator)
  → create or update the Operator-owned root CRD
  → Reconciler reads Conversation context
  → Reconciler observes facts and advances the domain Loop
       ├─ Ask / Confirm Human
       ├─ Prompt one or more Harnesses
       ├─ update Activity
       └─ wait for external state
  → Operator aggregates results
  → append operator answer Message
```

Operator 内部 Harness 的输出默认属于 Invocation 详情，不自动进入主对话。只有 Operator 显式发布的
内容才成为 `operator` 角色的最终回答；Operator 也可以选择把某个 Harness 结果显式公开为 Conversation
Message。

## 6. Conversation Context

Conversation History 是 loop-server 的事实，不属于任何 Operator 或 Harness。同一 Conversation 可以先后
由不同 Operator 和 Harness 参与，它们都可以在授权范围内读取已有历史。

Operator CRD 不复制完整对话，只保存协作引用和稳定水位，例如：

```yaml
spec:
  loop:
    invocationID: inv-123
    conversationID: conv-123
    inputMessageID: msg-456
    contextThroughSeq: 17
```

Reconciler 通过 loop-runtime 读取 Invocation Context。默认 Context 固定在 Invocation 创建时的
`contextThroughSeq`，使同一次 Reconcile 在重试时看到稳定输入，而不是被后来进入 Conversation 的消息
悄悄改变语义。

Operator 如果确实需要后续消息，应显式读取水位之后的 History。Ask/Confirm 的回答由 Interaction identity
关联并重新唤醒 Reconcile，不能靠扫描“最新一条消息”猜测回答属于谁。

## 7. loop-runtime

loop-runtime 向 Operator 暴露少量 typed capability，使领域 Reconcile 不必重复实现会话、Harness 调用和
Human interaction 的控制骨架：

```text
Chat.Context     读取本次 Invocation 的稳定输入与历史
Chat.History     显式读取 Conversation 的增量历史
Harness.Prompt   以 prompt、tools 和可选 Harness target 发起或恢复一次 Call
Ask / Confirm    请求 Human 提供信息或确认决定
Chat.Activity    发布本次 Invocation 的处理进展
Chat.Reply       以 Operator 身份提交主回答
```

`Harness.Prompt` 返回持久 Call handle，而不是等待整个执行完成的普通 RPC：

```go
call, err := r.Loop.Harness.Prompt(ctx, loop.Prompt{
    Target: harnessRef,
    Text:   prompt,
    Tools:  tools,
})
```

调用方可以观察事件、结束当前 Reconcile 后等待状态变化再次唤醒，或在确实只剩结果未返回时等待同一个
Call。等待方式只改变 caller 的控制流，不能创建第二次 Harness 执行。

Tools 由调用 Harness 的一方选择和提供。Harness Adapter 负责把稳定的 Prompt、Tool、Event 和终态语义
转换为 agentd 或第三方系统的协议；provider 差异不得进入 Operator 领域逻辑和 loop-server 的协作模型。

## 8. Operator 编排 Harness

Operator 可以拥有一组对它可见的 Harness 引用。可见性和角色映射属于 Operator 配置或 CRD，不属于
loop-server 的领域表。

一个意图识别 Operator 可以用同一套内核表达不同复杂度的处理：

```text
简单请求
  → 根据 Query 与 Conversation History 识别意图
  → 选择一个可见 Harness
  → Prompt
  → 汇总并 Reply

复杂请求
  → 将 Query 拆成多个子问题
  → 串行或并行 Prompt 多个 Harness
  → 观察各 Call 的流式进展与终态
  → 校验并汇总结果
  → Reply
```

拆解策略、串并行关系、中间结果和完成条件由该 Operator 的 Resource/Reconcile 表达。loopd 只提供 Call
identity、观察、Interaction 和 Chat 投影，不把某种 Agent Team、DAG 或固定角色分工写入 Core。

## 9. 状态与投影边界

loop-server 是 Conversation 协作事实的 owner：

- Message 是对话历史的事实来源；
- Invocation 关联一次用户输入、目标 responder、处理状态和最终回答；
- Harness Call 保存外部执行引用、幂等 identity、当前状态和结果引用；
- Interaction 保存待决问题、回答和终态；
- Activity 是面向 UI 的通用处理详情；
- Artifact 保存或引用非文本产物。

loop-server 不保存 Operator 领域表，也不复制 Operator CRD 的完整状态。通用 Activity 只承载跨 Operator
都能理解的处理摘要；更丰富的领域详情留在 Operator Resource，必要时通过专用 View 扩展展示。

agentledger 可以记录 Message、Invocation、Harness Call 和 Activity 的不可变执行事实，用于审计、回放和
成本分析；它不承担 Conversation History、待决 Interaction 或其它业务状态，因此不能替代 loop-server
的数据模型。

AgentUE 在 loopd 中定义 UI model、patch 和 SSE 投影。AgentUE Runner 当前负责 Python 后台任务、Redis
事件桥和 heartbeat recovery；直接把它嵌入 Go loop-server 会与 loopd DB、Harness provider 及 Operator
CRD 形成多个执行 owner。因此 v1 由 loopd 自己提供基于数据库的 durable Harness Call runner，不把
AgentUE Runner 作为 Invocation runner。Python Harness/Operator 可以在自身实现
内部使用 AgentUE Runner，但对 loopd 仍只暴露 Harness/Operator 契约。未来只有在 Runner 支持 Go 或允许
复用 loopd 现有 event store 时，才考虑合并两套 lifecycle 基础设施。

## 10. 关键不变量

1. **公开角色只有三种**：Conversation 中只使用 `user`、`harness`、`operator`；外部 provider 的术语不
   扩散到 loopd Core。
2. **Conversation 独立于 responder**：历史属于 Human 持有的 Conversation，不因切换 Operator 或 Harness
   被复制或切断。
3. **Operator 拥有领域 Loop**：CRD、Reconcile、外部事实、拆解策略和完成条件不进入 loopd Core。
4. **直接 Harness 问答不伪装成 CRD Loop**：只有需要领域控制循环的请求才进入 Operator Resource。
5. **外部动作先获得持久 identity**：同一 owner 与 effect key 的重试必须观察或恢复同一次 Harness Call，
   不能重复触发无法证明结果的副作用。
6. **流式响应与完成正交**：有事件表示 Call 有进展，不表示成功；长时间运行也不能被等同于失联。
7. **上下文有明确水位**：Reconcile 默认读取 Invocation 创建时的稳定 Context，后续 History 必须显式请求。
8. **最终回答由 responder 收口**：直连 Harness 由 Harness 回答；Operator 内部可以调用多个 Harness，但由
   Operator 汇总并回答。
9. **事实与投影分层**：Message、Invocation、Interaction 和 Call 状态是协作事实；UI、Activity 详情和
   agentledger 记录不能反向成为业务 owner。
10. **Harness 差异止于 Adapter**：新增 agentd 或第三方 Harness 不应要求修改 Conversation、Operator 或
    Interaction 模型。
11. **连接不拥有执行**：HTTP/SSE 断开只结束本次观察；Invocation、Call、Interaction 与 Operator CRD
    继续存在，重连从持久 snapshot/cursor 恢复。

## 11. 组件与依赖方向

```text
Human Surface ↔ loop-server ↔ Harness Adapter ↔ Harness
                    ↑
                    │ public API
Operator CRD / Reconciler
          └─ embeds loop-runtime as its Go client
```

Operator 只依赖 loop-runtime 的公共契约，不依赖 loop-server 的数据库模型或私有实现。loop-server 不导入
Operator CRD，不解释 LongHorizon 或其它领域语义。Harness Adapter 可以替换，Conversation 与 Operator
不因 provider 变化而改变。

## 12. loopd 不是什么

- loopd 不是模型或单次智能执行框架；这部分能力由 Harness 提供。
- loopd 不是预置业务流程的工作流引擎；Graph、动态拆解或固定角色都可以是某个 Operator 的实现。
- loopd 不定义统一 Task CRD；每个 Operator 自己拥有适合其领域的 Resource。
- loopd 不让高频流式事件充当 CRD 状态，也不让 CRD 充当聊天记录或事件日志。
- loopd 不要求 Human 永远经过 Operator；直接请求 Harness 是与 Operator 编排并列的基础路径。
