# Kubernetes Quick Start

`deploy/k8s/loopd` 是 loopd 的 Helm Chart，安装以下组件：

- 一个 loop-server Deployment 与 Service；
- 一个 Router Operator Deployment；
- 一个 Web Deployment 与 Service，Web 同源代理 loop-server 的 `/v1` SSE/API；
- Quick Start 使用的内存 Redis Deployment 与 Service；
- Conversation CRD，以及 loop-server 和 Router 访问 Conversation 所需的 namespace RBAC。

Quick Start 不依赖 StorageClass：未配置 MySQL 时，loop-server 使用临时 SQLite；内置 Redis 只保留
内存数据。Pod 重建后聊天记录与运行中的事件都会丢失，因此该模式只用于体验。Chart v1 固定要求
`server.replicaCount=1`。

Router 通过 OpenAI-compatible API 访问模型。Chart 会把模型名称与 API URL 写入 ConfigMap，
API key 则写入或引用 Secret，避免密钥出现在普通配置中。

Quick Start 可以使用内置 Redis，并配置模型 URL 与密钥：

```bash
helm upgrade --install loopd deploy/k8s/loopd \
  --namespace loopd --create-namespace \
  --set-string router.model.baseURL="https://model.example.com/v1" \
  --set-string router.model.apiKey="$MODEL_API_KEY"
```

也可以通过 `router.model.existingSecret` 引用已有 Secret。生产环境通常关闭内置 Redis，并通过
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
