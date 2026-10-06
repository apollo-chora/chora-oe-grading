# chora-oe-grading

## About

`chora-oe-grading` is a Go service that provides the two agents used by Chora's open-ended (OE) answer grading flow. `oe_evaluator` grades one answer against a weighted rubric and model answer, and can also produce a whole-assessment summary; `oe_moderator` reviews an evaluator result for rubric fidelity, hallucination, and consistency and returns an accept or reject decision with feedback. Both agents receive work through NATS JetStream and send model requests through `chora-model-gateway`.

## Quick start

Prerequisites: Go 1.26.6, a reachable NATS JetStream server, and a reachable Chora model gateway. The subscriber will not start unless dispatch is enabled and both `CHORA_GATEWAY_TENANT_ID` and `CHORA_GATEWAY_GCID` are set.

Clone the repository, download Go dependencies, and verify the build and tests:

```sh
git clone https://github.com/apollo-chora/chora-oe-grading.git
cd chora-oe-grading
go mod download
go build ./...
go test ./...
```

For local environment values, copy the checked-in example and edit it for the services available in your environment:

```sh
cp .env.example .env
```

The example uses NATS at `nats://nats:4222` and a plaintext model gateway at `localhost:9090`. It sets `AGENT_DISPATCH_ENABLED=true` and provides local tenant/GCID values. The example also points OTLP tracing at `http://otel-collector:4317`; remove that setting to use stdout tracing.

Build either binary explicitly:

```sh
go build -o oe_evaluator ./cmd/oe_evaluator
go build -o oe_moderator ./cmd/oe_moderator
```

## Usage

The repository builds two subscriber binaries:

| Binary | Dispatch role | Service name | Role |
| --- | --- | --- | --- |
| `cmd/oe_evaluator` | `oe_evaluate` | `chora-oe-evaluator` | Grades one OE answer; also supports `assess_summary` mode |
| `cmd/oe_moderator` | `oe_moderate` | `chora-oe-moderator` | Judges the evaluator output and returns accept or reject+feedback |

Both binaries take no command-line arguments. They subscribe to NATS JetStream dispatch requests and expose only the health endpoints `/healthz` and `/readyz` on `AGENT_HEALTH_PORT`.

The dispatch subjects use the form:

```text
chora.ai_kernel.agent_dispatch.<role>_requested.v1
chora.ai_kernel.agent_dispatch.<role>_completed.v1
```

where the role is `oe_evaluate` or `oe_moderate`. The canonical event envelope is carried in NATS headers.

Model calls go through `chora-model-gateway` over gRPC. Production uses TLS with `CHORA_GATEWAY_TOKEN`; setting `CHORA_GATEWAY_INSECURE` enables plaintext gRPC for local development.

The evaluator reads a per-turn `mode` from session state. The supported values are:

- `evaluate`: grade one OE answer against its weighted rubric and model answer. The LLM returns one score per rubric criterion plus an always-present per-question comment. The composite `points_earned` is calculated downstream, not by the model.
- `assess_summary`: produce a learner-facing holistic summary across all assessment answers. This path does not use rubric criterion scoring or the moderator loop.

The moderator receives the evaluator's JSON result together with the rubric, learner answer, and model answer. Its output is JSON of the form `{"accepted": true|false, "feedback": "..."}`; it does not re-grade the answer.

The main configuration variables are:

| Variable | Purpose | Default |
| --- | --- | --- |
| `NATS_URL` | NATS JetStream dispatch endpoint | unset |
| `AGENT_DISPATCH_ENABLED` | Enables the dispatch subscriber; must be `true` | `false` |
| `AGENT_DISPATCH_SUBSCRIPTION` | Overrides the generated consumer name | derived from service and role |
| `AGENT_DISPATCH_MAX_DELIVERY_ATTEMPTS` | Maximum redelivery attempts | `5` |
| `AGENT_HEALTH_PORT` | Health/readiness HTTP port | `8080` |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway gRPC target | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` | Tenant used for gateway attribution | required |
| `CHORA_GATEWAY_GCID` | Actor identity used for gateway attribution | required |
| `CHORA_GATEWAY_TOKEN` | Static bearer token for the gateway | unset |
| `CHORA_GATEWAY_INSECURE` | Uses plaintext gRPC for local gateway access | unset |
| `CHORA_GATEWAY_AUDIENCE` | Gateway token audience | `https://gateway.chora.site` |
| `CHORA_SESSION_APP_NAME` | ADK session app name | service name |
| `OE_EVALUATOR_MODEL` | Evaluator primary-model override | YAML value |
| `OE_MODERATOR_MODEL` | Moderator primary-model override | YAML value |
| `CHORA_ENV` | Environment label | `dev` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/gRPC trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | OTLP `service.version` value | `dev` |

The embedded agent configuration currently selects `gemini-3.1-pro-preview` with `gemini-2.5-pro` as fallback for the evaluator, and `gemini-3.5-flash` with `gemini-2.5-flash` as fallback for the moderator.

Docker builds both binaries into one image and defaults to the evaluator:

```sh
docker build -t chora-oe-grading .
docker run --rm chora-oe-grading
```

To use the moderator binary from the same image, override the command with `/app/oe_moderator`.

## Development

The project is a Go 1.26.6 module:

```text
cmd/oe_evaluator/       evaluator executable
cmd/oe_moderator/       moderator executable
internal/agent/         prompt composition, session-state context builders, scoring helpers, and moderation parsing
internal/agent/prompts/ embedded v1 prompt fragments
internal/agentconfig/   embedded per-agent model and prompt YAML
internal/boot/          configuration, agent construction, plugins, dispatch wiring, and runtime startup
```

The normal local checks are the same checks used by CI:

```sh
gofmt -l .
go mod tidy
go vet ./...
go test ./...
```

CI also verifies that `go mod tidy` leaves `go.mod` and `go.sum` unchanged.

The unit-test suite covers prompt composition, state handling, configuration, dispatch identity, gateway wiring, plugin configuration, and the subscriber-only startup contract. The tests are designed to run without a broker, database, or network dependency.
