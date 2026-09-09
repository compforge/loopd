# AGENTS.md

## 项目定位与边界

Web 是人、Operator、Harness 协作消息的展示与交互入口。它通过 Server HTTP API 读取和提交
消息，通过 Conv SSE 展示实时变化；业务执行、Human 终态和消费进度由 Server 与 Operator 决定。

## 代码地图与核心模块

```text
src/
├── main.tsx                 # React 挂载入口
├── app/                     # 三栏页面组装、导航身份与全局样式
├── actors/                  # Actor 身份、发现与发送目标选择
├── conversations/           # 会话列表、输入、共享消息生命周期 Hook 与历史游标
├── messages/                # 消息契约、revision 合并、正文加载与消息展示
│   └── blocks/              # Human 交互、result 和普通内容渲染
├── operator/                # 工作会话关联、Operator 卡片与并行布局
└── transport/               # 短请求、SSE reader、超时与传输错误
```

## 关键约定

1. 组件组织展示与用户操作；Hook 组合请求和页面生命周期；API 函数返回数据，不伪造 UI 事件。
   类型跟随所属模块，纯消息规则不从 API 模块导入类型。
2. 主对话与 Operator 工作会话复用 `useConversationMessages`。每次会话挂载拥有独立身份，
   所有读结果和写回调都只进入所属会话；切换页面不取消 Server 已接收的工作。
3. 元信息分页、正文按需读取、实时 SSE 各有职责。只有历史查询推进发现游标；正文与 SSE
   按 Message ID/revision 合并，不用列表位置或 block ID 代替消息身份。
4. AgentUE 负责基础模型和 patch；Web 解释 loopd 的已知业务 block。ActorKind 和 block type
   保持开放，已知 Human 内容经过类型收窄，未知内容不得自动解释为可执行交互。
5. HTTP 请求走 `transport/`。短请求包含正文读取的超时；SSE 由订阅生命周期控制，解码失败、
   重连和卸载都必须释放旧 reader。正文加载器负责可见内容读取的并发额度。
6. 测试与所属模块放在一起。纯状态规则用普通 Vitest，涉及切换、提交与重连的生命周期测试
   使用 jsdom 实际挂载 React；静态 HTML 渲染不能替代这些测试。

## 验证

在 `web/` 运行 `npm run check`，包含 TypeScript 检查、测试与生产构建。

## References

- [Web 架构](docs/architecture.md) — 模块依赖、会话状态归属与交互流程
- [UE 契约](../server/docs/ue.md) — 消息呈现、Human 交互与页面交付
- [Kernel](../docs/kernel.md) — Actor 协作模型与跨组件责任
