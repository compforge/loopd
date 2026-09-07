# loopd

**English** | [简体中文](README.zh-CN.md)

`loopd` is a collaboration platform where people, Operators, and Agent execution
services (Harnesses) exchange persistent messages. Build Kubernetes Operators with
the Go loop-runtime toolkit to connect these participants with business systems,
from routing a question to running a long-term task.

## Philosophy

An Agent can execute work. A business still needs to define its goals, coordinate
participants, and decide when the result is good enough. loopd gives developers
a collaboration platform and an Operator toolkit for expressing those decisions
in ordinary code:

```text
Loop = Resource(spec + status) + Reconcile
```

Resources hold goals and observed state; Reconcile decides the next step.

Each Operator defines its own process, collaboration model, and completion
criteria. Server manages shared messages and includes a Harness Engine to drive
calls through adapters for different Harnesses. The embedded loop-runtime toolkit
lets Operators use these capabilities through verbs such as Speak and Prompt.

## What makes it useful

- **Develop your own orchestration.** Combine Agent judgment with deterministic
  code and business APIs. The Go loop-runtime toolkit provides shared capabilities
  for calling Harnesses, asking people, and publishing progress.
- **Collaborate while work continues.** People, Operators, and Harnesses exchange
  persistent messages. Users can add context during execution, and Operators can
  share partial results or request confirmation as needed.
- **Keep long-running work observable.** Conversations show shared progress and
  results; domain resources retain business state. Operators can coordinate
  planning, execution, and verification over time.

![loopd orchestration architecture](docs/arch_v1.svg)

## See it in action

The bundled Router plans a request, runs independent Harness calls in parallel,
and synthesizes an answer. Here it introduces Liu Bei, Guan Yu, and Zhang Fei;
the detail panel shows the planning, execution, and synthesis alongside the answer.

![Router demo: an answer alongside planning, parallel execution, and synthesis](docs/router_demo.jpeg)

For longer tasks, the [LongHorizon Operator](operators/longhorizon/README.md)
combines a Manager, Executor, and Auditor to plan CLI work, execute it, check
artifacts, and decide what to do next.

## Get started

Follow the [Kubernetes Quick Start](deploy/k8s/README.md) to install the server,
Router, and Web UI. Configure an OpenAI-compatible model endpoint and credentials,
open the UI, select Router, and submit a question.

The Quick Start uses temporary storage and an in-process AgentGo demo. Recovery
across restarts requires persistent storage, saved Operator progress, and a
Harness with persistent execution and an adapter that supports recovery; see the
[recovery contract](docs/harness.md#恢复与内存).

To build an Operator, start with the [Router source](operators/router/internal/router/router.go)
and [runtime guide](docs/runtime.md). For Harness integration and execution, see
the [Harness Engine guide](docs/harness.md). Explore the [kernel](docs/kernel.md),
[component stack](docs/stack_v1.svg), or [image build guide](deploy/docker/README.md)
for more detail.
