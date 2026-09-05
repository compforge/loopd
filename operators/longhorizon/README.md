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
# 在当前进程环境配置模型凭据，不写入仓库。
go run ./operators/longhorizon/cmd/longhorizon
```

在 Web 发送框选择 LongHorizon，提交一个 CLI 工作区内的目标。执行期间可以继续发消息；
补充要求在轮次边界接收，角色过程和报告显示在右侧共享工作会话。Ask/Confirm 显示在主会话，
支持选项、自由输入和取消；普通发言不会自动批准卡片。超时或取消后，当前 Run 停止并留下总结。

可选 Helm 组件默认关闭：

```sh
helm upgrade --install loopd deploy/k8s/loopd -n loopd --create-namespace \
  --set longhorizon.enabled=true \
  --set longhorizon.model.existingSecret=longhorizon-model
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
| `LOOP_LH_WORKSPACE` | ./workspaces；以 Run UID 划分目录 |
| `LOOP_LH_MODEL_PROVIDER` / `LOOP_LH_MODEL` | openai / gpt-5-mini |
| `LOOP_LH_BASE_URL` / `LOOP_LH_API_KEY` | Adapter 模型连接配置 |

所有超时配置必须为正值。Helm 对应 `longhorizon.runTimeout`、`retentionTTL` 等字段。
当前使用 AgentGo 进程内 Adapter，支持 CLI 和文件工具，不提供浏览器。PVC 只保留文件，
不恢复 Agent 执行；生产持久执行和隔离由替换的 Harness Adapter 提供。

状态机、消息检查点、消费边界、回收及验证范围见 [设计](docs/design.md)。
