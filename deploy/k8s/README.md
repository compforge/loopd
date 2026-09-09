# Kubernetes Quick Start

`deploy/k8s/loopd` 是 loopd 的 Helm Chart，安装以下组件：

- 一个 loop-server Deployment 与 Service；
- 一个 Router Operator Deployment；
- 一个 Web Deployment 与 Service，Web 同源代理 loop-server 的 `/v1` SSE/API；
- Quick Start 使用的内存 Redis Deployment 与 Service；
- Conversation CRD，以及 loop-server 和 Router 访问 Conversation 所需的 namespace RBAC。

Quick Start 不依赖 StorageClass：未配置 MySQL 时，loop-server 使用临时 SQLite；内置 Redis 只保留
内存数据。Pod 重建后聊天记录与运行中的事件都会丢失，因此该模式只用于体验。
SQLite 要求 `server.replicaCount=1`；共享 MySQL 与 Redis 时可配置多个 Server 副本。

Router 委托 Server 的 Harness Engine 调用模型。Chart 将模型名称与 API URL 写入 ConfigMap，
API key 引用已有 Secret，避免密钥出现在普通配置中。

Quick Start 可以使用内置 Redis，并配置模型 URL 与密钥：

```bash
helm upgrade --install loopd deploy/k8s/loopd \
  --namespace loopd --create-namespace \
  --set-string server.harnesses.agentgo.base_url="https://model.example.com/v1" \
  --set-string server.harnessAPISecret=loopd-model
```

先创建 `loopd-model` Secret 的 `api-key` 字段；模型凭据只注入 Server。生产环境通常关闭内置 Redis，并通过
`redis.address` 与 `redis.existingSecret` 接入独立运维的 Redis。

需要持久化聊天记录时，配置外部 MySQL。Chart 不创建或管理 MySQL；生产环境应通过已有 Secret 提供
DSN。Chart 对 loop-server 统一设置 `DATABASE_DRIVER=mysql` 与 `DATABASE_DSN=<secret value>`：

```yaml
database:
  mysql:
    existingSecret: loopd-database
    existingSecretKey: dsn
```

也可以为本地试用直接设置 `database.mysql.dsn`，Chart 会据此创建 Secret。DSN 非空时 loop-server
自动使用 MySQL，否则使用内置 SQLite。

安装后访问 Web：

```bash
kubectl -n loopd port-forward service/loopd-loopd-web 8080:80
```

然后打开 `http://127.0.0.1:8080`。

可选的 [LongHorizon Operator](../../operators/longhorizon/README.md) 通过 `longhorizon.enabled=true`
启用。其 Role 只读 Conv，独立管理 Run/Execution/Audit；`longhorizon.runTimeout` 和 `retentionTTL`
分别控制业务期限和终态资源保留时间，均默认 24h，不影响 server 的 Conv 或页面交付生命周期。

### Message 内容存储

loop-server 通过正整数环境变量 `CONTENT_MAX_BYTES`（默认 65536）统一限制 Message 和 Part
的 content 列，不能超过 65536。内联 block 数量是代码内的存储策略，不提供部署配置。
超过内联预算的 block 保存到
数据库 `message_parts`，读取时由 server 展开，页面与 Operator 接收完整正文。
根 content 和单个 Part content 编码后均不超过 64 KiB；大 block 自动拆为存储 frames，
逻辑读取时透明重组。根 metadata 或引用目录超限会拒绝写入。详见
[持久化约定](../../server/docs/persistence.md#message-内容与-parts)。

## Harness 配置

`server.harnessRunConcurrency` 设置每个 Server Pod 的 Harness 驱动上限，默认 50，通过
`HARNESS_RUN_CONCURRENCY` 传入。新调用满载返回 429；已接收调用的超时、取消维护不占执行槽。

`server.harnesses` 是 target → Adapter 配置映射，通过 ConfigMap 挂载为 HARNESS_CONFIG_FILE。
Router 默认使用 agentgo；LongHorizon 使用 manager/executor/auditor。模型、凭据与文件工作目录
属于 Server，Operator Pod 只保留编排配置。`server.workspace` 配置 demo 文件工具存储。

AgentGo 仅用于单 Server demo，不具备跨进程执行恢复；多副本应配置可恢复的远端 Adapter。
完整协议、Managed Agent 配置和回放边界见 [Harness](../../docs/harness.md)。
