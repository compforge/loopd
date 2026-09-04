# loopd

`loopd` is an orchestration runtime for collaboration among humans, Harnesses,
and Kubernetes Operators.

Its guiding idea is:

```text
Loop = Resource(spec + status) + Reconcile
```

Every chat request creates a small loopd Task CRD that wakes the selected
responder. A simple Operator can reconcile that Task directly; a complex
Operator may create domain CRDs for its own state and completion semantics.
`loopd` keeps visible collaboration in conversations and messages, while full
execution history belongs to the Harness and AgentLedger.

## Components

- **loop-server** is a Hertz service that owns page-visible conversations and
  messages. Before committing a chat request, it also creates the Task CRD;
  failure rolls the messages back.
- **loop-runtime** is a Go client embedded in an Operator. It exposes stable
  conversation and message capabilities through `r.Loop.Chat`, and lets a
  Reconciler watch and resolve Tasks through `r.Loop.Task`.
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

1. loop-server creates a user message and an empty responder message with one
   `task_id`, then creates a same-ID Task CRD before committing the messages;
2. the selected Operator or Harness watches the Task and resolves its current
   input and conversation history through loop-runtime;
3. a complex Operator may create a domain CRD, while a simple Operator handles
   the shared Task directly;
4. visible progress updates the responder message while full events enter AgentLedger;
5. clients may disconnect and reload the current message snapshot;
6. the selected Harness or Operator finishes the same responder message.

`Harness.Prompt` returns a handle. A Reconciler can inspect it and return, or
call `Wait` when waiting is genuinely the only remaining work. Both paths use
the same `(owner UID, effect key)` and therefore the same external execution.
