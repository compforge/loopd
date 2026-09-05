# Interaction：交互演示

在聊天目标中选择「交互演示 · Ask → Confirm」，发送任意问题：

1. Ask 提供「简要说明 / 分步骤说明 / 举例说明」。
2. 选择后才出现 Confirm，可确认或取消。
3. 最终答案列出原始问题、Operator 的提问及你的选择和确认结果。

每一步最多等待 10 秒。Ask 取消或超时不再发起 Confirm；Confirm 取消或超时也直接汇总。
取消、超时都不是同意。本例不调用模型，不执行外部业务操作。

## 本机启动

先启动 loop-server 并安装 Conv CRD，然后在仓库根目录运行：

```sh
SERVER_URL=http://127.0.0.1:8080 CONVERSATION_NAMESPACE=default \
  go run ./operators/interaction/cmd/interaction
```

Kubernetes 配置使用标准 `KUBECONFIG`；Conversation namespace 必须与 server 一致。进程通过 runtime
注册 `interaction` 并自动续租，退出后租约到期会从目标列表消失。

## 独立参与者与消费策略

演示监听 Conv CRD，通过 runtime Poll 接收发给自己的消息。同一 Conv 的普通用户发言按顺序
处理：Ask → Confirm → Speak 汇总 → 关闭可选页面流 → Commit。等待卡片答复时释放 Reconcile
并发位，不提交当前输入；用户可以继续追加消息，它们留在队列中，当前交互结束后再处理。

卡片答复经 Human handle 获取，不再触发新的 Ask。普通发言即使写着“确认”也不会自动批准
卡片；本例将其作为排队的新发言。这是演示的业务策略，不是 runtime 对所有 Operator 的限制。

EffectKey 与汇总消息 Key 以输入 Message ID 为基础，TaskID 只关联页面流。重启从 Committed
重读输入，复用已持久化的问题、deadline、结果与汇总；不依赖进程内执行状态，也不重置超时。
只有汇总和可选页面收尾成功，才 Commit 此输入，卡片答复随后独立消费并确认。

本例的业务进度可从持久问题和结果重新推导，因此不另建领域 CRD。这不意味着 runtime 可以
恢复任意 Go 调用栈；增加无法推导的领域进度时，应由 Operator 自己持久化。
每个 Actor/Conv 应保持单一消费 owner，注册续租不是多副本执行锁。

验证：`go test ./operators/interaction/...`。
