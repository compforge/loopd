# Kubernetes Quick Start

`deploy/k8s/loopd` 是 loopd 的 Helm Chart，安装以下组件：

- 一个 loop-server Deployment、Service 与 SQLite PVC；
- 一个 Router Operator Deployment；
- 一个 Web Deployment 与 Service，Web 同源代理 loop-server 的 `/v1` SSE/API；
- Quick Start 使用的 Redis Deployment、Service 与 PVC；
- Task CRD，以及 loop-server 和 Router 访问 Task 所需的 namespace RBAC。

loop-server 当前只支持 SQLite，所以 Chart 固定要求 `server.replicaCount=1`。Redis 让浏览器断线后可以
继续读取 Task 事件，但不会把 SQLite 变成共享数据库。等 loop-server 支持外部共享数据库后，再开放
server 多副本。

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

安装后访问 Web：

```bash
kubectl -n loopd port-forward service/loopd-loopd-web 8080:80
```

然后打开 `http://127.0.0.1:8080`。
