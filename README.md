# loopd

`loopd` is an orchestration runtime for collaboration among humans, Harnesses,
and Kubernetes Operators.

Its guiding idea is:

```text
Loop = Resource(spec + status) + Reconcile
```

An Operator owns its domain CRDs and completion semantics. `loopd` supplies the
durable collaboration plane around those resources: conversation history,
long-running invocations, resumable Harness calls, human interactions, and UI
projection.

## Components

- **loop-server** is a Hertz service that owns conversations, messages,
  invocations, activities, interactions, and Harness Call records. Its AgentUE
  SSE endpoint projects persisted state to a reconnectable UI stream.
- **loop-runtime** is a Go client embedded in an Operator. It exposes stable
  capabilities through `r.Loop.Chat`, `r.Loop.Harness`, `r.Loop.Operator`,
  `r.Loop.Ask`, and `r.Loop.Confirm`.
- **Harness adapters** connect loop-server to agentd or another intelligent
  execution service without leaking provider vocabulary into the public model.

loop-server includes its own durable Harness Call runner. It repeatedly performs
bounded `Ensure`/`Observe` steps while the actual long-running execution remains
owned by the Harness provider. AgentUE is used for the UI model and SSE binding,
not as a second execution ledger.

The public conversation roles are always:

```text
user | harness | operator
```

## Long-running execution

An Invocation may run for minutes or days. Its lifecycle is independent from
any HTTP request or browser connection:

1. a user message and its Invocation are persisted before processing begins;
2. an Operator accepts the Invocation by binding it to its own CRD, or a direct
   Harness request receives a durable Call identity;
3. short observations update persisted state and event cursors;
4. clients may disconnect and reconstruct current state from a new AgentUE
   `start` snapshot;
5. the final Harness or Operator answer is appended to the same Conversation.

`Harness.Prompt` returns a handle. A Reconciler can inspect it and return, or
call `Wait` when waiting is genuinely the only remaining work. Both paths use
the same `(owner UID, effect key)` and therefore the same external execution.

## HTTP surface

The first API slice includes:

```text
GET  /v1/responders
POST /v1/conversations
GET  /v1/conversations/{id}/messages
POST /v1/conversations/{id}/messages
GET  /v1/invocations/{id}
GET  /v1/invocations/{id}/context
GET  /v1/invocations/{id}/events/stream
GET  /v1/operators/{id}/invocations
GET  /v1/operators/{id}/events
POST /v1/invocations/{id}/accept
POST /v1/invocations/{id}/reply
POST /v1/invocations/{id}/harness-calls
POST /v1/invocations/{id}/interactions
POST /v1/interactions/{id}/resolve
```

Run the standalone server with a SQLite database and a configured responder
catalog:

```bash
export LOOP_SERVER_RESPONDERS='[
  {"role":"operator","id":"intent","display_name":"Intent Operator"}
]'
go run ./cmd/loop-server
```

Harness adapters are supplied when assembling `server.New`; the standalone
binary deliberately does not assume one provider.

## Design

- [`docs/kernel.md`](docs/kernel.md) defines the stable product and ownership
  model.
- [`server/docs/persistence.md`](server/docs/persistence.md) documents the
  loop-server persistence and recovery model.
