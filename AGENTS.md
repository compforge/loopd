# AGENTS.md

## 项目定位与边界

loopd 是 “Loop is a CRD” 在编排层的实现，也是 Human、Harness 与 Operator 的协作平台。
独立参与者通过持久化消息协作，自行决定何时接收、回应和确认安全消费。DB 保存会话消息，
Conv CRD 保存消费进度并触发 Reconcile，Redis 服务页面流与重连。Operator 自行定义业务执行边界
及领域 CRD，Harness 持有智能执行状态；loopd 不保存 Operator 领域表。稳定模型见 `docs/kernel.md`。

Conversation 中的公开角色统一为 `user`、`operator`、`harness`。Agent、Assistant、Session 等外部
概念通过 Harness Adapter 接入后，不再进入 loopd 公共模型。

## 代码地图与核心模块

```text
loopd/
├── cmd/loop-server/        # 进程配置、依赖组装与生命周期
├── config/crd/             # loopd Conv CRD 安装清单
├── deploy/                 # loop-server、Router、Web 镜像与 Kubernetes Helm Chart
├── docs/                   # loopd 稳定内核与跨模块设计
├── harness/                # runtime 侧 Harness Adapter 契约；agentgo 为进程内 demo
├── operators/router/       # 首个业务 Operator；按复杂度临时编排一个或多个 Harness
├── operators/interaction/  # 串行 Ask → Confirm 交互示例，含取消、超时与结果汇总
├── runtime/                # Operator 协作 toolkit；提供 Conv、Delivery、Human、Harness 与注册 Verb
├── server/                 # Conversation、Message 与 HTTP 服务；细节见 server/AGENTS.md
├── web/                    # React Web；主对话与 Operator 执行详情的三栏协作界面
└── *.go                    # 跨 server、runtime 和 harness 的公共协作模型
```

## 关键约定

1. server 拥有跨参与者的可见聊天历史；Operator 拥有领域状态，Harness 拥有执行状态，AgentLedger
   承载完整轨迹。具体存储、交付和发现约束见各领域文档。
2. Operator 通过 loop-runtime 公共契约接入，复用 controller-runtime 的资源控制循环；runtime 的
   定位与协作能力统一见 `docs/runtime.md`，领域类型与 Harness provider 差异不进入 server。
3. 修改 `runtime/api/` 下的 CRD 类型后运行 `make generate manifests`，提交 DeepCopy、基础 CRD
   YAML 与 Helm Chart 中同步的 CRD YAML。
4. 根目录 `VERSION` 使用 SemVer；任何代码改动都必须在同一变更中递增版本。
5. 公开仓内容必须脱敏，不得提交内部链接、凭据或仅在公司环境成立的配置。

## References

- `docs/kernel.md` — 独立参与者协作模型、状态与恢复责任、设计文档分工
- `docs/runtime.md` — Operator 开发库定位、注册发现、Conv 消费、消息上下文、Harness Call 与结果发布
- `server/AGENTS.md` — server 代码地图及各领域设计索引
- `server/docs/conversation.md` — Conv 消息接收与 Poll 契约
- `deploy/docker/README.md` — loop-server、Router 与 Web 镜像构建入口
- `deploy/k8s/README.md` — Helm Quick Start、组件拓扑与配置边界
- `README.md` — 产品定位与使用入口
