# AGENTS.md

## 项目定位与边界

loopd 是 Actor 通过持久消息协作的平台，也是 “Loop is a CRD” 在编排层的实现。
Actor 自行决定何时接收、回应和确认安全消费。Server 是人、Operator、Harness 的协作平台，
loop-runtime 是嵌入 Operator 的 Go SDK/toolkit。Conv CRD 承载唤醒信号、会话关联与消费进度，
协作数据通过 Server API 读写；DB 保存会话消息和可恢复的 Harness 调用记录，Redis 经 SSE
服务 UI 会话流与 Operator 调用流。Operator 自行定义业务执行边界及领域 CRD，Harness 持有
智能执行状态；loopd 不保存 Operator 领域表。稳定模型见 `docs/kernel.md`。

ActorKind 是开放字符串枚举，内置常量为 `user`、`operator`、`harness`；Operator 可用
`operator/<operator-key>/<role>` 等扩展 kind 定义参与者身份；Go、Web 与 CRD 不维护封闭白名单。
Agent、Assistant、Session 等外部
概念通过 Harness Adapter 接入后，不再进入 loopd 公共模型。

## 代码地图与核心模块

```text
loopd/
├── cmd/loop-server/        # 进程配置、依赖组装与生命周期
├── deploy/                 # loop-server、Router、Web 镜像与 Kubernetes Helm Chart
├── docs/                   # loopd 稳定内核与跨模块设计
├── operators/longhorizon/  # Manager/Executor/Auditor 长期 CLI Operator；Run 自主管理期限与回收
├── operators/router/       # 首个业务 Operator；按复杂度临时编排一个或多个 Harness
├── operators/interaction/  # 串行 Ask → Confirm 交互示例，含取消、超时与结果汇总
├── pkg/contract/           # 跨 server、runtime 和 harness 的公共协作契约
├── pkg/harness/            # Harness Adapter 契约；agentgo 为进程内 demo，managedagent 接入远端 SDK API
├── pkg/k8s/                # server 与 runtime 共享的 Kubernetes 契约
│   ├── v1alpha1/           # Conv Go 类型
│   └── crds/               # Conv 生成清单与校验测试
├── runtime/                # Operator 协作 toolkit；提供 Conv、消息句柄、Human、Harness 与注册 Verb
├── server/                 # 协作平台、Harness Engine 与 HTTP 服务；细节见 server/AGENTS.md
└── web/                    # React Web；主对话与 Operator 执行详情的三栏协作界面
```

## 关键约定

1. server 拥有跨参与者的可见聊天历史；Operator 拥有领域状态，Harness 拥有执行状态，AgentLedger
   承载完整轨迹。具体存储、交付和发现约束见各领域文档。
2. Operator 复用 controller-runtime 的资源控制循环，通过 loop-runtime 封装的 Server API 协作。
   Poll/Commit 的消息读取和 Conv CRD 游标更新由 Server 执行；领域 CRD 由 Operator 自行操作，
   领域类型不进入 server。Server 内置 Harness Engine：Run 记录调用，HarnessRunner 驱动，
   Adapter 适配 provider 差异。
   runtime 的定位与协作能力统一见 `docs/runtime.md`。
3. 修改 `pkg/k8s/` 或 `operators/longhorizon/api/` 下的 CRD 类型后运行 `make generate manifests`。
   生成清单与校验测试分别归属 `pkg/k8s/crds/` 和 `operators/longhorizon/api/crds/`；
   提交 DeepCopy、生成清单与同步到 `deploy/k8s/loopd/crds/` 的 Helm 安装清单。
4. 根目录 `VERSION` 使用 SemVer；任何代码改动都必须在同一变更中递增版本。
5. 公开仓内容必须脱敏，不得提交内部链接、凭据或仅在公司环境成立的配置。

## References

- `docs/kernel.md` — Actor 协作模型、状态与恢复责任、设计文档分工
- `docs/harness.md` — Harness Engine：配置与发现、Run/Runner 生命周期、Adapter 契约与恢复
- `docs/runtime.md` — Operator 开发库定位、注册发现、Conv 消费、共享历史、Harness Call 与结果发布
- `server/AGENTS.md` — server 代码地图及各领域设计索引
- `server/docs/conversation.md` — Conv 消息接收与 Poll 契约
- `deploy/docker/README.md` — loop-server、Router 与 Web 镜像构建入口
- `deploy/k8s/README.md` — Helm Quick Start、组件拓扑与配置边界
- `README.md` — 产品定位与使用入口
