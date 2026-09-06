# loopd

**English** | [简体中文](README.zh-CN.md)

`loopd` is a server-side runtime for building and running Agent orchestration
with Kubernetes Operators. Business code defines the goals, coordination
policy, and completion criteria; loopd provides shared collaboration capabilities
for humans and Harnesses—the Agent execution services an Operator calls.

Users, Operators, and Harnesses are Actors that collaborate through persistent
messages. Each participant can receive input and publish progress independently;
users can add information while work is running.

A capable Agent can execute a task, while the surrounding work may need to wait
for a person, consult an external system, coordinate several executions, or
verify a result before continuing. loopd lets developers express those decisions
in ordinary code and keep domain goals and progress in resources outside the
executing process.

## What you can build

- **Request routing and parallel work:** plan a request, call one or more
  Harnesses, and bring their results back into one conversation.
- **Human participation:** ask for missing information or confirmation, then
  use the response to decide how work proceeds.
- **Long-running business loops:** define domain resources and reconcile their
  state against external facts, with your own roles and completion criteria.

The bundled Router demonstrates planning, parallel execution, and synthesis.
The [LongHorizon Operator](operators/longhorizon/README.md) demonstrates a longer
loop: Manager plans, Executor performs CLI work, and Auditor checks artifacts.
Run, Execution, and Audit resources hold its domain control state; additional
user input is consumed at round boundaries.

## Router demo

Here the user asks for separate introductions to Liu Bei, Guan Yu, and Zhang Fei.
The Router creates a plan, starts three independent Harness calls, and summarizes
the results. The main conversation shows its answer; the detail panel shows
`plan`, the parallel `work/*` calls, and `summarize`.

![Router demo: a shared answer alongside planning, parallel Harness calls, and synthesis](docs/router_demo.jpeg)

The Router uses one configured Harness target for these calls. Its orchestration
policy lives in the [Router Operator](operators/router/internal/router/router.go).

## Build an Operator

The development model is:

```text
Loop = Resource(spec + status) + Reconcile
```

A Resource records goals and observed state. Its Reconciler reads current facts
and decides whether to execute, wait, retry, or finish. Business developers own
that logic and can use ordinary clients to access their databases and APIs.

loop-runtime is a Go toolkit embedded in the Operator, alongside Kubernetes
controller-runtime. It provides conversation context, Harness calls, human
questions and confirmations, and progress and answer publication.
controller-runtime supplies resource watches, queues, and reconciliation.

Messages signal their selected Actor through a persistent Conversation (Conv) CRD.
Operators use Poll to receive input, Speak to publish messages, and Commit once
processing reaches a point that is safe to resume from. Router reconciles Conv
directly; more complex Operators create domain CRDs to retain their own goals,
progress, and completion criteria. Each Operator decides how incoming messages
form work and when that work is complete.

Start from the [Router source](operators/router/internal/router/router.go) and
[Operator development contract](docs/runtime.md).

## Architecture

![loopd orchestration architecture](docs/arch_v1.svg)

- **loop-server** owns visible conversations and messages and signals selected
  participants through Conv CRDs. Users share history and observe progress through
  the Web UI.
- **loop-runtime** connects business Operators to conversation context, Human
  interaction, Harness execution, and result publication.
- **Harness adapters** connect the runtime to an Agent execution service. The
  bundled AgentGo adapter runs in process; a persistent adapter is required for
  execution recovery across Operator restarts.

Conversation preserves visible collaboration; Operator resources hold domain
state; Harnesses own execution state. AgentUE supplies the page event model and
Redis bridge. AgentLedger is responsible for complete execution facts, including
prompts, model events, tool calls, retries, and costs. Hostel provides the
agent-native sandbox for file, tool, and compute execution.

![loopd component stack](docs/stack_v1.svg)

See the [kernel design](docs/kernel.md) for shared concepts and ownership.

## Long-running work and recovery

A browser disconnect does not cancel execution. Users can return to the same
conversation to follow progress and read the answer. Durable orchestration
requires the Operator to persist domain progress; a Conv consumption cursor
cannot restore its execution state. Harness calls use stable, business-owned
idempotency keys, while human interactions reuse their own stable question
identities.

Recovery also depends on the execution and storage configuration. The runtime's
Harness Call cache and bundled AgentGo adapter are in process. A persistent
Harness adapter must map the same call identity to the same durable execution
after a restart. External business APIs need their own idempotency and recovery
handling. Router keeps its plan and results in memory, so unread or uncommitted
messages can be received again, but intermediate work is not automatically
restored. See the [runtime design](docs/runtime.md) for these boundaries.

## Get started

Follow the [Kubernetes Quick Start](deploy/k8s/README.md) to install loop-server,
the Router, and the Web UI with Helm. Configure an OpenAI-compatible model
endpoint and credentials, open the UI, select Router, and submit a question.

The Quick Start uses temporary SQLite and in-memory Redis by default; chat and
event data are lost when their Pods are recreated. Persistent storage and Harness
execution must be configured for durable deployments.

For image builds, see the [Docker guide](deploy/docker/README.md).
