# chora-oe-grading

The OE-grading crew for Chora: the two-agent open-ended-answer quality loop
that backs the orchestrator's per-OE-answer grading path (ADR-172 §D2/§D4/§D5).
The **oe_evaluator** grades ONE OE answer against its weighted rubric +
model_answer (per-criterion sub-scores + an always-present per-question
comment) and also runs `assess_summary` mode (the holistic whole-assessment
narrative over all answers); the **oe_moderator** is the judge that reviews the
evaluator's grading and decides accept | reject+feedback for rubric fidelity,
hallucination, and internal consistency. The Python orchestrator owns the
≤2-iteration loop; the human instructor is the authoritative override
downstream.

Module path: `github.com/apollo-chora/chora-oe-grading`.

The crew is cloud-neutral: NATS JetStream for dispatch (via
`chora-adk-common/agentdispatch` + `chora-common/eventbus`), standard OTLP for
traces (via `chora-adk-common/tracing` + `chora-common/otel`), env-backed
secrets, and all model calls routed through `chora-model-gateway`. No cloud
account or managed service is required.

## Binaries

| Binary | Dispatch role | Service name | Purpose |
|---|---|---|---|
| `cmd/oe_evaluator` | `oe_evaluate` | `chora-oe-evaluator` | Grader of record (evaluate + assess_summary modes) |
| `cmd/oe_moderator` | `oe_moderate` | `chora-oe-moderator` | Judge: accept/reject the evaluator's grading |

Both are subscriber-only processes (ADR-254 D6): the binary composes runner +
plugin chain + `agentdispatch.Serve` directly and consumes its NATS JetStream
dispatch subscription. The only HTTP surface is the health port
(`/healthz`, `/readyz`). The binaries take NO arguments — a stale
`web -port 8080 ... agentengine` command line is refused by name.

## Packages

| Package | Purpose |
|---|---|
| `internal/agent/` | The two prompt composers (pure functions), the per-turn mode-branch instruction providers, the rubric→composite scoring context builders, and the moderator accept/reject parser. |
| `internal/agentconfig/` | Embedded per-agent model + prompt YAML (tier / primary_model / fallback_models / prompt_version) — the single source of truth for model selection. |
| `internal/boot/` | Resolved boot wiring: env + agentconfig into a `Config`, agent constructors with the LLM injected, the plugin chain [inboundTrace, tenantProp, termination], and the subscriber-only serve glue. |

## Transport

- **Events** — NATS JetStream via `chora-common/eventbus`. The dispatch
  subjects (`chora.ai_kernel.agent_dispatch.<role>_{requested,completed}.v1`)
  are valid NATS subjects; the canonical event envelope rides as NATS headers.
- **Traces** — standard OTLP/gRPC via `chora-common/otel`; stdout in local dev
  when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset.
- **Model calls** — gRPC to `chora-model-gateway`; TLS + `CHORA_GATEWAY_TOKEN`
  in production, plaintext when `CHORA_GATEWAY_INSECURE` is set for local dev.

## Configuration

| Variable | Purpose | Local default |
| --- | --- | --- |
| `NATS_URL` | NATS JetStream event bus (dispatch subscriber) | unset |
| `AGENT_DISPATCH_ENABLED` | Opt in to the dispatch subscriber (must be `true`) | `false` |
| `AGENT_DISPATCH_SUBSCRIPTION` | Consumer name override | derived from service + role |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | Redelivery ceiling (must match the consumer) | `5` |
| `AGENT_HEALTH_PORT` | Health/readiness port for the subscriber-only agent | `8080` |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway gRPC target | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` | Tenant scope for RLS + ledger attribution (required) | unset |
| `CHORA_GATEWAY_GCID` | Actor identity: learner GCID or agent AGID (required) | unset |
| `CHORA_GATEWAY_TOKEN` | Static bearer token for the model gateway | unset |
| `CHORA_GATEWAY_INSECURE` | Plaintext gRPC to a local gateway (dev only) | unset |
| `CHORA_GATEWAY_AUDIENCE` | Audience claim for gateway tokens | `https://gateway.chora.site` |
| `CHORA_SESSION_APP_NAME` | ADK session app_name (default: the service name) | `chora-oe-evaluator` / `chora-oe-moderator` |
| `OE_EVALUATOR_MODEL` | Override the evaluator's primary model | YAML-declared |
| `OE_MODERATOR_MODEL` | Override the moderator's primary model | YAML-declared |
| `CHORA_ENV` | Environment label (dev / staging / prod) | `dev` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | Stamped as the OTLP `service.version` attribute | `dev` |

The crew needs no database. It needs NATS (dispatch) and reaches
`chora-model-gateway` for every LLM call.

## Build and test

```sh
go build ./...
go vet ./...
gofmt -l .   # must be empty
go test ./...
```

The suite is hermetic — no broker, database, or network is required.

## Docker

```sh
docker build -t chora-oe-grading .
```

The image carries both binaries; the entrypoint defaults to `oe_evaluator`.
Override the command to select the moderator: `["/app/oe_moderator"]`.
