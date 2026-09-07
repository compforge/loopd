# LongHorizon Operator

LongHorizon 是 loop-runtime 的使用示例：Manager 规划，Executor 使用 CLI/文件工具执行，
Auditor 独立检查工件，再由 Manager 决定继续、询问用户或结束。它注册为 `operator / longhorizon`，
通过 Conv 消费消息，领域资源为 `longhorizon.loopd.compforge.io/v1alpha1` 下的 Run、Execution、Audit。

## 运行

```sh
make generate manifests
kubectl apply -f config/crd/bases/
# 使用当前 kubeconfig；server 必须服务同一 namespace。
export LOOP_LH_NAMESPACE=loopd
export LOOP_LH_SERVER_URL=http://127.0.0.1:8080
# 在 Server 配置 manager/executor/auditor Harness 目标和模型凭据。
go run ./operators/longhorizon/cmd/longhorizon
```

在 Web 发送框选择 LongHorizon，提交一个 CLI 工作区内的目标。执行期间可以继续发消息；
补充要求在轮次边界接收，角色过程和报告显示在右侧共享工作会话。Ask/Confirm 显示在主会话，
支持选项、自由输入和取消；普通发言不会自动批准卡片。超时或取消后，当前 Run 停止并留下总结。

当前不自动识别新任务与继续旧任务：同一主会话中，Run 未结束时的输入视为补充；Run 已结束且
总结已保存后，后续输入会启动新 Run，并读取会话历史。需要独立开展工作时，可以新建主会话。

可选 Helm 组件默认关闭：

```sh
helm upgrade --install loopd deploy/k8s/loopd -n loopd --create-namespace \
  --set longhorizon.enabled=true \
  --set server.harnessAPISecret=longhorizon-model
```

先准备该 Secret 的 `api-key` 字段及对应版本镜像，构建见 [Docker 说明](../../deploy/docker/README.md)。
Helm 不自动更新已安装 CRD，升级前显式 apply 新定义。一个副本和 leader election 保证单一消费 owner。

## 配置

| 配置 | 默认 |
|---|---|
| `LOOP_LH_MAX_ROUNDS` | 25；确认后可追加 25，总上限 1000 |
| `LOOP_LH_MANAGER_TIMEOUT` / `LOOP_LH_AUDITOR_TIMEOUT` | 5m |
| `LOOP_LH_EXECUTOR_TIMEOUT` | 30m |
| `LOOP_LH_HUMAN_TIMEOUT` | 30m |
| `LOOP_LH_RUN_TIMEOUT` | 24h；Operator 自己的业务期限 |
| `LOOP_LH_RETENTION_TTL` | 24h；最终报告落库后保留 Run 的时间 |
| `LOOP_LH_SERVER_URL` / `LOOP_LH_NAMESPACE` | http://127.0.0.1:8080 / default |

所有超时配置必须为正值。Helm 对应 `longhorizon.runTimeout`、`retentionTTL` 等字段。
Server 默认示例使用 AgentGo 进程内 Adapter，支持 CLI 和文件工具，不提供浏览器。PVC 只保留文件，
不恢复 Agent 执行；生产持久执行和隔离由替换的 Harness Adapter 提供。

状态机、消息检查点、消费边界、回收及验证范围见 [设计](docs/design.md)。

角色 Adapter、模型凭据和工具工作目录统一在 Server 配置，见 [Harness](../../server/docs/harness.md)。
