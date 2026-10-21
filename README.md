# chora-oe-grading

> The open-ended grading pair: an **evaluator** that scores one free-text answer against its
> rubric, and a **moderator** that judges the evaluator's own output.

Go. Two subscriber binaries, one image, no database. They consume agent-dispatch requests
over NATS JetStream and call the platform's model gateway.

## What it is

Open-ended answers cannot be marked right or wrong, so grading is two agents and a judge:

| Binary | Dispatch role | Compose service | Job |
| --- | --- | --- | --- |
| `cmd/oe_evaluator` | `oe_evaluate` | `oe-evaluator` | grades one OE answer, or produces a holistic summary |
| `cmd/oe_moderator` | `oe_moderate` | `oe-moderator` | accepts or rejects the evaluator's output, with feedback |

The two halves are deliberately split by cost. The embedded agent configuration declares the
evaluator's tier as **HIGH** and the moderator's as **CHEAP** (ADR-172 §D2), and the tier is
carried as a label rather than baked into a model id — so a deployment can point either at a
different model without touching the split.

Neither agent serves application traffic. They take no command-line arguments and expose only
health endpoints.

### The evaluator's two modes

The evaluator reads a per-turn `mode` from session state, and the two modes behave very
differently:

| Mode | What it does |
| --- | --- |
| `evaluate` (default) | grades one answer against its weighted rubric and model answer, returning one score **per rubric criterion** plus an always-present per-question comment |
| `assess_summary` | produces a learner-facing holistic summary across all answers in an assessment; **no** criterion scoring and **no** moderator loop |

Note where the arithmetic happens: the evaluator returns per-criterion scores, and the
composite `points_earned` is **calculated downstream, not by the model**. Do not expect the
model to have summed anything.

### The moderator does not re-grade

The moderator receives the evaluator's JSON result together with the rubric, the learner's
answer and the model answer. It returns:

```json
{"accepted": true, "feedback": "..."}
```

That is a verdict on the evaluator's work, not a second opinion on the answer. A rejection
comes back as feedback for a retry, not as an alternative score.

### Session state the dispatch must carry

Both agents read their inputs from per-dispatch session state, and a missing key aborts prompt
construction rather than degrading. The keys differ by role and by mode:

| Role / mode | Required keys | Optional |
| --- | --- | --- |
| `oe_evaluate`, `evaluate` | `learner_answer`, `rubric_json`, `model_answer` | `prior_moderator_feedback` on a re-grade |
| `oe_evaluate`, `assess_summary` | `results_digest` | — |
| `oe_moderate` | `evaluation_json`, `learner_answer`, `rubric_json` | `model_answer` |

Plus `mode` on the evaluator, set to `evaluate` or `assess_summary`.

That table is the part an integration gets wrong: a dispatcher that publishes to the right
subject with the wrong state shape sees a prompt-construction failure, which reads like a
model or transport problem and is neither.

### Dispatch subjects

```text
chora.ai_kernel.agent_dispatch.<role>_requested.v1
chora.ai_kernel.agent_dispatch.<role>_completed.v1
```

where `<role>` is `oe_evaluate` or `oe_moderate`. The canonical event envelope travels in the
NATS headers rather than the body.

### The health endpoint does not bind — this is the agent framework

Both binaries document `AGENT_HEALTH_PORT` (default `8080`), and `chora-adk-common` logs a
`health_addr` at boot. **Nothing answers on that port** — verified for both services in the
running stack, from a sibling container. `chora-companion-diagnosis` and `chora-kg-explorer`
are built the same way and behave identically, so this is the shared agent framework rather
than a fault in any one repository.

Do not gate readiness on `curl http://…:8080/healthz` for these agents. The broker's consumer
list is the reliable check — see [Verify it works](#verify-it-works).

## Prerequisites

| Route | What you need |
| --- | --- |
| In the stack | A working `chora-stack` deployment. NATS and the model gateway come from it. |
| Standalone | Go **1.26.6** (`go.mod` declares it), a reachable NATS JetStream broker, and a reachable model gateway. |
| Either | Two logical model ids the gateway understands (`OE_EVALUATOR_MODEL`, `OE_MODERATOR_MODEL`) and gateway identity. |

## Credentials

This pair holds **one** credential — an internal caller token for the model gateway. There is
no provider key here.

| Value | Where it comes from | Variable |
| --- | --- | --- |
| Gateway caller token | the stack's `GATEWAY_API_KEY` | `CHORA_GATEWAY_TOKEN` |
| Gateway tenant | the stack's `CHORA_DEFAULT_TENANT_ID` | `CHORA_GATEWAY_TENANT_ID` |
| Gateway GCID | the stack's `CHORA_DEFAULT_GCID` | `CHORA_GATEWAY_GCID` |
| NATS endpoint | the stack's NATS service; no credential locally | `NATS_URL` |
| Evaluator model id | the stack's `.env`; **required, no default** | `OE_EVALUATOR_MODEL` |
| Moderator model id | the stack's `.env`; **required, no default** | `OE_MODERATOR_MODEL` |

`compose.yml` marks `GATEWAY_API_KEY` required (`${GATEWAY_API_KEY:?...}`), so the stack will
not start either service without it. Locally the value is inert — `GATEWAY_AUTH_ENABLED=false`
— but it must be non-empty, and it must equal the `<key>` half of `GATEWAY_API_KEYS` before
gateway authentication is switched on.

`OE_EVALUATOR_MODEL` and `OE_MODERATOR_MODEL` are **logical model ids the gateway resolves**,
not provider model names. The stack supplies `LongCat-2.5-Preview` for both; boot fails if
either is empty. That both currently point at the same id is a deployment choice — the
HIGH/CHEAP tier split still applies, and production would typically give them different
models.

The provider key behind those routes belongs to the gateway, not to these agents. Where it
comes from is documented once in
[`chora-stack/README.md`](../chora-stack/README.md#credentials).

## Run it in the stack

`compose.yml` builds the image from `../chora-oe-grading` **twice**, overriding the entrypoint
each time:

| Service | Entrypoint | Subscription |
| --- | --- | --- |
| `oe-evaluator` | `/app/oe_evaluator` | `chora-oe-evaluator.agent-dispatch-oe-evaluate-requested` |
| `oe-moderator` | `/app/oe_moderator` | `chora-oe-moderator.agent-dispatch-oe-moderate-requested` |

Both start once `nats` is healthy, `nats-init` has completed, and `model-gateway` has started.
Neither publishes a host port.

```sh
cd chora-stack
docker compose up -d oe-evaluator oe-moderator
docker compose ps oe-evaluator oe-moderator
```

What Compose sets, per service: `CHORA_GATEWAY_ENDPOINT=model-gateway:9090`,
`CHORA_GATEWAY_AUDIENCE=chora-local`, `CHORA_GATEWAY_INSECURE=1` (plaintext gRPC inside the
Compose network), `CHORA_ENV=local`, `AGENT_DISPATCH_ENABLED=true`, `AGENT_HEALTH_PORT=8080`,
and the matching `OE_*_MODEL`.

The dispatching half also has to be on: the kernel starts the `oe_grading` lane only when
`OE_GRADING_CREW_ENABLED` is set, which the stack does. Either half missing means requests go
to a subject nobody consumes — silently.

## Run it standalone

```sh
git clone https://github.com/apollo-chora/chora-oe-grading.git
cd chora-oe-grading
go build ./...
```

```sh
export CHORA_GATEWAY_ENDPOINT=localhost:9090   # else it defaults to gateway.chora.site:443
export CHORA_GATEWAY_INSECURE=1                # plaintext gRPC to a local gateway
export CHORA_GATEWAY_AUDIENCE=chora-local
export CHORA_GATEWAY_TENANT_ID=your-tenant-id
export CHORA_GATEWAY_GCID=your-gcid
export CHORA_GATEWAY_TOKEN=<gateway caller token>
export OE_EVALUATOR_MODEL=<logical model id>
export OE_MODERATOR_MODEL=<logical model id>
export NATS_URL=nats://localhost:4222
export AGENT_DISPATCH_ENABLED=true
go run ./cmd/oe_evaluator
```

The first three lines matter for a local run: without them the agents dial
`gateway.chora.site:443` over TLS and look for an audience of `https://gateway.chora.site`, so
they will not reach a gateway on your machine.

Swap `cmd/oe_evaluator` for `cmd/oe_moderator` for the other half; run both to exercise the
accept/reject loop. The binaries do not read `.env` — export the values.

Container: the image defaults to the evaluator, and the moderator is selected by command:

```sh
docker build -t chora-oe-grading .
docker run --rm chora-oe-grading                      # evaluator
docker run --rm chora-oe-grading /app/oe_moderator    # moderator
```

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `OE_EVALUATOR_MODEL` | **required** (stack: `LongCat-2.5-Preview`) | evaluator gateway `logical_model_id` |
| `OE_MODERATOR_MODEL` | **required** (stack: `LongCat-2.5-Preview`) | moderator gateway `logical_model_id` |
| `OE_EVALUATOR_FALLBACK_MODELS` | unset → no fallback | evaluator fallback chain |
| `OE_MODERATOR_FALLBACK_MODELS` | unset → no fallback | moderator fallback chain |
| `NATS_URL` | unset | NATS JetStream dispatch endpoint |
| `AGENT_DISPATCH_ENABLED` | `false` | must be `true` for the subscriber to start |
| `AGENT_DISPATCH_SUBSCRIPTION` | derived from service and role | consumer override |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | `5` | redelivery ceiling |
| `AGENT_HEALTH_PORT` | `8080` | the port the health listener is configured for — which does not bind |
| `CHORA_GATEWAY_ENDPOINT` | `gateway.chora.site:443` | gateway gRPC target (stack: `model-gateway:9090`) |
| `CHORA_GATEWAY_TENANT_ID` | **required** | attribution tenant |
| `CHORA_GATEWAY_GCID` | **required** | attribution actor |
| `CHORA_GATEWAY_TOKEN` | unset in code; **required by the stack** | static bearer token |
| `CHORA_GATEWAY_INSECURE` | unset | plaintext gRPC when truthy (stack: `1`) |
| `CHORA_GATEWAY_AUDIENCE` | `https://gateway.chora.site` | token audience (stack: `chora-local`) |
| `CHORA_SESSION_APP_NAME` | the service name | ADK session app name |
| `CHORA_ENV` | `dev` | environment label (stack: `local`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | stdout | OTLP/gRPC trace endpoint |
| `CHORA_SERVICE_VERSION` | `dev` | OTLP `service.version` |

## Verify it works

### Do not probe the health port

As above, `AGENT_HEALTH_PORT` is logged but not bound. Use the checks below.

### Both subscribers came up

```sh
docker compose logs oe-evaluator --tail=20 | grep agentdispatch.serving
docker compose logs oe-moderator --tail=20 | grep agentdispatch.serving
```

A line naming the role (`oe_evaluate`, `oe_moderate`) and its subscription is the pass. A
`subscriber ... stopped` line is the failure — most often a NATS connect error at boot.

### The broker knows both consumers

The `nats` container ships no `nats` CLI, so read the monitoring endpoint from the host:

```sh
curl -s 'http://localhost:8222/jsz?consumers=1' \
  | grep -o 'chora-oe-[a-z]*-agent-dispatch[a-z-]*' | sort -u
```

```text
chora-oe-evaluator-agent-dispatch-oe-evaluate-requested
chora-oe-moderator-agent-dispatch-oe-moderate-requested
```

That is the stronger check: the broker's own view rather than the process's claim about
itself.

### The dispatching lane is on

```sh
docker compose exec -T nats wget -qO- http://ai-kernel-orchestrator:8080/readyz \
  | grep -o '"oe_grading":{[^}]*}'
```

`"started": true` with both roles is required. With the agents running and the lane off,
grading requests are published to a subject with no consumer.

### It has no database

There is nothing to migrate and no schema to check. If a deployment suggests otherwise, it is
describing a different service.

## Troubleshooting

- **The health probe fails while the agent is healthy** — expected; the listener does not bind
  in this build. Use the log and broker checks above.
- **Boot fails naming `OE_EVALUATOR_MODEL` or `OE_MODERATOR_MODEL`** — one is unset. Both are
  required with no default; the stack supplies `LongCat-2.5-Preview`.
- **The service exits or restarts immediately** — `AGENT_DISPATCH_ENABLED` is not `true`, or
  gateway identity is missing. Dispatch enablement is handled inside `chora-adk-common`, so
  the local code alone does not guarantee that every disabled path exits.
- **Grading requests are published but nothing consumes them** — either the kernel's
  `OE_GRADING_CREW_ENABLED` is off, or the publisher is using a
  different subject. Compare with the broker consumer list.
- **Every answer comes back with per-criterion scores but no total** — expected. The composite
  `points_earned` is computed downstream; the model is not asked to add up.
- **A summary assessment produces no moderator step** — correct. `assess_summary` mode skips
  both criterion scoring and the moderator loop.
- **The moderator rejects repeatedly** — it is judging the evaluator, not the learner. Read
  the `feedback` field: it describes what the evaluator got wrong, so the fix is usually in
  the rubric or the model route rather than the answer.
- **An unknown `mode` in session state** — only `evaluate` and `assess_summary` are supported;
  treat anything else as a dispatch-contract error rather than a model problem.
- **Traces never reach Tempo** — note first that **these two agents are not affected**.
  `compose.yml` gives them `OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317` (no scheme), and
  the boot log confirms `endpoint=otel-collector:4317`. The `too many colons in address`
  error seen elsewhere in the stack belongs to services configured with an `http://` prefix.
  If these agents still emit nothing, look at the collector rather than the endpoint string.

## API surface

No inbound HTTP API. Both binaries are subscriber-only.

| Role | Service name | Request subject | Completion subject |
| --- | --- | --- | --- |
| `oe_evaluate` | `chora-oe-evaluator` | `chora.ai_kernel.agent_dispatch.oe_evaluate_requested.v1` | `…oe_evaluate_completed.v1` |
| `oe_moderate` | `chora-oe-moderator` | `chora.ai_kernel.agent_dispatch.oe_moderate_requested.v1` | `…oe_moderate_completed.v1` |

The envelope travels in the NATS headers, not the payload.

The only HTTP surface is the health listener described above, which does not bind in this
build.

## Architecture

```text
cmd/oe_evaluator/      evaluator binary
cmd/oe_moderator/      moderator binary
internal/agent/        prompt composition, mode parsing, scoring output handling
internal/agentconfig/  embedded tier and prompt-version configuration
internal/boot/         configuration, gateway wiring, dispatch setup, startup
Dockerfile             one image, both binaries
```

The design point worth knowing is the split between *scoring* and *judging*. The evaluator
produces per-criterion numbers; the moderator never touches them. That keeps the expensive
model doing the judgement and the cheap one doing the review, which is the whole reason for
the HIGH/CHEAP tier split.

## Events

Dispatch is NATS JetStream, one durable consumer per role, with the request subject set by
`AGENT_DISPATCH_SUBSCRIPTION` or derived from the service and role.

| Concern | Setting |
| --- | --- |
| Request / completion subjects | `chora.ai_kernel.agent_dispatch.<role>_{requested,completed}.v1` |
| Redelivery ceiling | `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS`, default `5` |
| Streams | `CHORA_EVENTS` for normal dispatch, `CHORA_DLQ` for dead letters |
| Envelope | carried in NATS headers |

The evaluator and the moderator form a loop rather than a pipeline: the moderator's verdict
goes back to the kernel, which decides whether to accept the grade or re-dispatch the
evaluator with the feedback.

## Migrations

**None.** Neither binary has a database, ships no `migrations/` directory and is not part of
the stack's migrate one-shot set. Everything needed arrives in the dispatch payload.

If you are auditing a deployment and expect an `oe-grading-migrate` container, there isn't one
and there should not be.
