# loopd

`loopd` is an orchestration runtime for collaboration among humans, Harnesses,
and Kubernetes Operators.

Its guiding idea is:

```text
Loop = Resource(spec + status) + Reconcile
```

An Operator owns its domain CRDs and completion semantics. `loopd` supplies the
visible collaboration plane around those resources: conversations and messages.
Complete execution history belongs to the Operator, Harness, and AgentLedger.

## Components

- **loop-server** is a Hertz service that owns page-visible conversations and
  messages. One chat request atomically creates the user and responder messages.
- **loop-runtime** is a Go client embedded in an Operator. It exposes stable
  conversation and message capabilities through `r.Loop.Chat`; Harness and
  interaction capabilities build on the same runtime boundary.
- **Harness adapters** connect loop-server to agentd or another intelligent
  execution service without leaking provider vocabulary into the public model.

AgentUE is used for the page-visible message model. AgentLedger records complete
prompts, model events, tool calls, retries, and costs; it is not the chat database.

The public conversation roles are always:

```text
user | harness | operator
```

## Long-running execution

A question may run for minutes or days. Its lifecycle is independent from any
HTTP request or browser connection:

1. loop-server atomically creates a user message and an empty responder message
   with the same Runner `task_id`;
2. an Operator binds those identities to its CRD, or a Harness owns its execution;
3. visible progress updates the responder message while full events enter AgentLedger;
4. clients may disconnect and reload the current message snapshot;
5. the selected Harness or Operator finishes the same responder message.

`Harness.Prompt` returns a handle. A Reconciler can inspect it and return, or
call `Wait` when waiting is genuinely the only remaining work. Both paths use
the same `(owner UID, effect key)` and therefore the same external execution.
