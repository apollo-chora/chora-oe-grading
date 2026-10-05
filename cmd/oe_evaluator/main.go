// Command oe_evaluator is the entry point for the OE-grading crew's evaluator
// agent — the grader of record (ADR-172 §D2/§D4/§D5). It is the first of two
// members (oe_evaluator grades, oe_moderator judges), mirroring the qgen
// question→critic 2-agent loop (ADR-153).
//
// Two modes, selected PER TURN from the session-state "mode" key (the qgen
// "critic never saw the candidate" lesson — NOT a static system prompt):
//
//   - evaluate       — grade ONE OE answer against its weighted rubric +
//     model_answer → per-criterion sub-scores + an
//     always-present per-question comment. The deterministic
//     composite (points_earned) is derived downstream in the
//     Python orchestrator from these sub-scores + the rubric
//     weights, NOT by the LLM.
//   - assess_summary — the holistic whole-assessment narrative over ALL answers
//     (MCQ + OE). No rubric, no per-criterion scoring, NO
//     moderator loop (ADR-172 §D5). Same agent, different
//     per-turn instruction.
//
// Runtime: a plain container Deployment, NOT a hosted agent service
// (ADR-172 D3 + ADR-169), and since ADR-254 D6 a SUBSCRIBER-ONLY process: the
// ADK web launcher is gone, the binary composes runner + plugin chain +
// agentdispatch.Serve directly and consumes its NATS JetStream dispatch
// subscription ({service}.agent-dispatch-{role}-requested); the only HTTP
// surface is the health port (/healthz, /readyz). A subscriber that cannot
// start, or stops, ends the process with a non-zero exit so the container
// dies instead of idling healthy. The binary takes NO arguments: a stale
// "web -port 8080 ... agentengine" command is refused by name.
//
// Guardrails: the guardrail screen runs at chora-model-gateway (ADR-152 /
// ADR-163), plus an orchestrator-side screen in the Python graph; the plugin
// chain is the qgen chain [inboundTrace, mana, tenantProp, termination] with
// no agent-side screening (the grading content rides in the instruction, not
// the user message, so an agent-side screen would see only the "BEGIN"
// trigger).
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	OE_EVALUATOR_MODEL                  — override (default agentconfig YAML primary)
//	CHORA_SESSION_APP_NAME              — ADK session app_name (default: the service name)
//	CHORA_GATEWAY_ENDPOINT              — default gateway.chora.site:443
//	CHORA_GATEWAY_TENANT_ID / _GCID     — required (ADR-163 Phase 3.1; no stub fallback)
//	CHORA_GATEWAY_TOKEN                 — static bearer token for the model gateway
//	CHORA_GATEWAY_INSECURE              — plaintext gRPC to a local gateway (dev only)
//	CHORA_ENV                           — dev | staging | prod
//	NATS_URL                            — NATS JetStream event bus (dispatch subscriber)
//	AGENT_DISPATCH_ENABLED              — must be "true" (ADR-254 D6: no other transport)
//	AGENT_DISPATCH_SUBSCRIPTION         — request subscription (default: derived from the service + role)
//	AGENT_HEALTH_PORT                   — health port (default 8080; /healthz + /readyz only)
//	OTEL_EXPORTER_OTLP_ENDPOINT         — OTLP/gRPC trace endpoint (unset: stdout)
//
// The boot wiring itself lives in internal/boot so it is unit-testable: config
// resolution returns errors instead of fatal-exiting in place, the LLM is
// injected as an interface, and the plugin identity strings are assertable.
// main() keeps only the process-level concerns: the log handler and the exit.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/apollo-chora/chora-oe-grading/internal/boot"
)

var (
	serviceName = "chora-oe-grading"
	gitSHA      = "unknown"
	buildTime   = "unknown"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	// SIGTERM (rollout, scale-down) cancels the context: the dispatch
	// subscriber stops receiving, in-flight work finishes, and the process
	// exits 0. Any other way out is an error and exits non-zero (ADR-254 D6).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := boot.RunEvaluator(ctx, os.Args[1:]); err != nil {
		log.Fatalf("oe_evaluator: %v", err)
	}
}
