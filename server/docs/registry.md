# 在线注册与发现

注册表描述可直接接收 Task 的执行目标是否在线。Operator 与 Harness 分别拥有注册入口，Actor
是 server 提供给 Chat UI 的聚合发现视图；Human 无需注册，也不出现在可选目标列表中。

## 注册与续租

Operator 使用 `Loop.Operator.Register`，可直聊 Harness 使用 `Loop.Harness.Register`。runtime
首次注册成功后随自身生命周期定期续租，进程停止续租后，该目标自然从发现列表消失。

`operators` 与 `harnesses` 各以 `key` 唯一标识一个逻辑目标，保存 UUIDv7 主键、展示名称、描述、
`expires_at` 与创建/更新时间。具体字段由各自的 model 定义。注册状态不承载调用轨迹或领域配置。

多个副本使用相同 key 续租同一条记录。进程退出时不主动删除记录，避免一个副本误删其他副本的
可用性；过期记录保留，供后续注册复用。续租由 runtime 按租期三分之一的间隔发起。

## 面向 Chat 的发现

`GET /v1/actors` 聚合尚未过期的 Operator 与 Harness。用户在发送框旁选择目标，每次请求携带
目标的 kind/key；目标属于这次问答，Conversation 不绑定一个固定 Operator。

租约表示近期有进程续租，不能保证发现之后的每次请求都会立即得到处理。Task 的持久唤醒职责
独立于租约；已开始任务的生命周期见 [Task 交付](task-delivery.md)。

Router 只注册自身。Operator 内部按请求临时创建、只能经由 Operator 调用的 Harness 不进入
可直聊目标列表。Operator 的候选集合和分派策略由其自身配置或领域 Resource 决定。
