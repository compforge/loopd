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

> Human 在持久 Conversation 中向 Harness 或 Operator 提出目标；loop-server 创建 Task CRD 唤醒
> responder，Operator 按需建立领域 CRD、调用 Harness，并把过程与结果带回同一 Conversation。

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
| **Operator** | Reconcile loopd Task，读取外部事实并决定何时调用 Harness、询问 Human、等待或结束；复杂业务可以拥有领域 CRD |

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
| **Message** | Conversation 中一次页面可见表达；`task_id + kind + key` 标识问答任务与发送者，content 保存 AgentUE semantic model 快照 |
| **Task** | loop-server 为一次问答创建的通用 CRD；只保存 responder 路由与唤醒版本，名称即 Message 使用的 `task_id` |
| **Operator Resource** | 复杂 Operator 按需创建并拥有的领域 CRD；记录该领域独有的目标、状态和完成条件 |
| **Harness Execution** | Harness 自己拥有的执行；可耗时、流式返回，完整轨迹由 AgentLedger 记录 |

Activity、Artifact 和各种流式事件是这些对象的过程记录或投影，不与它们平级：

- **Activity** 展示一次问答内部正在发生什么，例如 Operator 阶段、Harness Call 和工具执行；
- **Artifact** 引用文本之外的结果，例如文件、图片或报告；
- 流式事件说明 Harness Call 仍有反应，不代表 Call 已经完成。

`loop-server` 和 `loop-runtime` 是平台组件，不是新的协作角色。前者拥有服务端协作状态，后者是供
Operator Reconciler 使用的 Go API，不单独拥有业务事实。

一次问答任务可能持续几十分钟乃至数天。Task CRD 提供独立于 HTTP request、SSE 连接、浏览器页面或
某一个 loop-server 进程的持久 Reconcile 入口；复杂领域的长期状态由 Operator Resource 持有，完整智能
执行状态由 Harness 持有。

## 4. Loop is a CRD

loopd 将需要长期收敛的领域 Loop 表达为：

```text
Loop = Resource(spec + status) + Reconcile
```

- `spec` 表达 Human 认可的目标和约束；
- `status` 表达 Operator 对现实世界的最新观测；
- Reconciler 每次读取最新目标、状态和外部事实，判断应该继续、等待、重试、询问 Human 还是结束；
- Event 只提示事实可能变化，不能代替 Reconciler 重新读取权威状态。

loopd 拥有一个通用 Task CRD。它不是业务 Task 模型，只是一次问答的持久标识、路由与唤醒载体：

```yaml
metadata:
  name: <task-id>
spec:
  target:
    kind: operator | harness
    key: <responder-key>
  revision: 1
```

Query、Conversation History 和回答不复制进 CRD。Reconciler 从 `metadata.name` 取得 Task ID，再通过
loop-runtime 向 loop-server 获取当前 input、response 和有界 History。

简单 Operator 直接 Reconcile 通用 Task。复杂 Operator 收到 Task 后可以创建自己的领域 CRD，由领域
Resource 的 spec/status 承载长期状态和完成条件。例如 LongHorizon 可以定义 Manager、Executor 和
Auditor，其他 Operator 可以采用完全不同的 Resource；这些领域概念不进入 loopd Core。

## 5. 两条主流程

### 5.1 Human 直接请求 Harness

```text
Human
  → create user Message + empty harness Message (same task_id)
  → create same-ID Task CRD before the database commit
  → Harness starts or resumes its execution
  → project visible stream into the harness Message
  → finish the harness Message
```

Harness 的流式文本可以直接构成主回答，可见工具状态可以投影到 Message；完整 prompt、tool call/result、
诊断与成本进入 AgentLedger。一次 Call 耗时很久不等于没有响应；进行中、等待输入、完成和失败必须是
可区分的事实。

### 5.2 Human 请求 Operator

```text
Human
  → create user Message + empty operator Message (same task_id)
  → create same-ID Task CRD before the database commit
  → Operator Reconciler resolves Conversation context through loop-runtime
  → optionally create or update an Operator-owned domain CRD
  → Reconciler observes facts and advances the domain Loop
       ├─ Ask / Confirm Human
       ├─ Prompt one or more Harnesses
       ├─ update Activity
       └─ wait for external state
  → Operator aggregates results
  → update and finish the operator Message
```

Operator 内部 Harness 的输出默认属于详情会话或 AgentLedger，不自动进入主对话。只有 Operator 显式发布的
内容才成为 `operator` 角色的最终回答；Operator 也可以选择把某个 Harness 结果显式公开为 Conversation
Message。

## 6. Conversation Context

Conversation History 是 loop-server 的事实，不属于任何 Operator 或 Harness。同一 Conversation 可以先后
由不同 Operator 和 Harness 参与，它们都可以在授权范围内读取已有历史。

通用 Task CRD 不复制完整对话，只保存 responder 路由与唤醒版本。loop-server 根据 Task ID 从主
Conversation Message 即时组装上下文：

```text
TaskContext
  ├─ Conversation
  ├─ current input Message
  ├─ current response Message
  └─ bounded Conversation History through the current input
```

详情 Conversation 可以沿用同一 `task_id`，但不会覆盖主 Conversation 的 input/response。Ask/Confirm
等后续可见消息也沿用同一 Task ID；server 更新 Task revision 后再次唤醒 Reconciler。复杂 Operator 若需
稳定保存领域水位，应在自己的 Resource 中记录相关 Message ID。

## 7. loop-runtime

loop-runtime 向 Operator 暴露少量 typed capability，使领域 Reconcile 不必重复实现会话、Harness 调用和
Human interaction 的控制骨架：

```text
Chat.Conversation 读取 Conversation
Chat.History     显式读取 Conversation 的增量历史
Chat.Send        创建 user、responder Message 与 Task CRD，并返回 responder Message
Chat.Update      更新 responder Message 的可见语义快照
Task.Get         按 Task ID 读取当前 input、response 与 Conversation History
Task.Watch       为指定 responder 注册 Task Reconciler
Harness.Prompt   以 prompt、tools 和可选 Harness target 发起或恢复一次 Call
Ask / Confirm    请求 Human 提供信息或确认决定
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

拆解策略、串并行关系、中间结果和完成条件由该 Operator 的 Reconcile 表达；复杂度需要时再落入其领域
Resource。loopd 只提供 Task、Call identity、观察、Interaction 和 Chat 投影，不把某种 Agent Team、DAG
或固定角色分工写入 Core。

## 9. 状态与投影边界

loop-server 是页面协作事实和 Task 分发的 owner：

- Conversation 代表一个对话框；
- Message 是页面可见对话历史的事实来源；
- 同一次问答的 user、responder 及后续可见交互共享 `task_id`；
- Message content 可以投影 Activity、Artifact 等页面需要展示的内容。
- Task CRD 以同一个 `task_id` 命名，只承载 responder 路由与可推进版本。

loop-server 不建立 `tasks` 表，也不保存 Operator 领域表。Task 查询是基于 Message 的实时视图；通用
Activity 只承载跨 Operator 都能理解的处理摘要，更丰富的领域详情留在 Operator Resource，必要时通过
专用 View 扩展展示。

AgentLedger 记录 prompt、模型事件、Harness Call、tool call/result、重试和成本等完整执行事实，用于审计、
回放和分析；它不承担页面 Conversation History，因此不能替代 loop-server 的两表业务模型。

AgentUE 在 loopd 中定义页面语义模型。AgentUE Runner 当前负责 Python 后台任务、Redis 事件桥和 heartbeat
recovery；直接把它嵌入 Go loop-server 会与 Harness provider、Task 和领域 CRD 形成多个执行 owner。因此
v1 只复用 AgentUE 的页面模型，不把 AgentUE Runner 作为 loopd 的执行 owner。

## 10. 关键不变量

1. **公开角色只有三种**：Conversation 中只使用 `user`、`harness`、`operator`；外部 provider 的术语不
   扩散到 loopd Core。
2. **Conversation 独立于 responder**：历史属于 Human 持有的 Conversation，不因切换 Operator 或 Harness
   被复制或切断。
3. **Task 只负责分发和唤醒**：Query、History、回答和领域状态不复制进 loopd Task CRD。
4. **领域 CRD 按复杂度引入**：简单 Operator 直接 Reconcile Task；复杂 Operator 自己拥有领域 Resource。
5. **一次问答有稳定 task identity**：初始 user/responder Message 和后续反问、确认共享同一 `task_id`。
6. **外部动作先获得持久 identity**：同一 owner 与 effect key 的重试必须观察或恢复同一次 Harness Call，
   不能重复触发无法证明结果的副作用。
7. **流式响应与完成正交**：有事件表示 Call 有进展，不表示成功；长时间运行也不能被等同于失联。
8. **上下文有明确边界**：TaskContext 给出当前输入、回答和 History 水位；复杂 Operator 可以把所需引用
   保存到自己的 Resource。
9. **最终回答由 responder 收口**：直连 Harness 由 Harness 回答；Operator 内部可以调用多个 Harness，但由
   Operator 汇总并回答。
10. **可见历史与完整轨迹分层**：Message 只保存页面可见快照，AgentLedger 保存完整执行轨迹。
11. **Harness 差异止于 Adapter**：新增 agentd 或第三方 Harness 不应要求修改 Conversation 或 Operator 模型。
12. **连接不拥有执行**：HTTP/SSE 断开只结束本次观察；Task、Harness 执行与可选的领域 CRD 继续存在，
    重连从持久状态恢复。

## 11. 组件与依赖方向

```text
Human Surface ↔ loop-server ↔ Harness Adapter ↔ Harness
                    │
                    ├─ creates loopd Task CRD
                    │
Operator Reconciler ┘
          ├─ embeds loop-runtime as its Go client
          └─ optionally owns domain CRDs
```

Operator 只依赖 loop-runtime 的公共契约，不依赖 loop-server 的数据库模型或私有实现。loop-server 不导入
Operator CRD，不解释 LongHorizon 或其它领域语义。Harness Adapter 可以替换，Conversation 与 Operator
不因 provider 变化而改变。

## 12. loopd 不是什么

- loopd 不是模型或单次智能执行框架；这部分能力由 Harness 提供。
- loopd 不是预置业务流程的工作流引擎；Graph、动态拆解或固定角色都可以是某个 Operator 的实现。
- loopd Task CRD 不是通用工作流状态机；复杂领域仍由各 Operator 定义自己的 Resource。
- loopd 不让高频流式事件充当 CRD 状态，也不让 CRD 充当聊天记录或事件日志。
- loopd 不要求 Human 永远经过 Operator；直接请求 Harness 是与 Operator 编排并列的基础路径。
