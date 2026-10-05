// Package boot holds the resolved boot wiring for the OE-grading crew's two
// ADK-Go agent binaries (oe_evaluator + oe_moderator) per ADR-172 §D2/§D3.
//
// Why this package exists: every binary's main() used to inline ~90 statements
// of env reading, agentconfig loading, validation, LLM construction, agent
// construction, plugin construction and launcher wiring, each failure handled
// by log.Fatalf. None of it could be reached without running the binary, so the
// load-bearing identity strings (agent names, termination AgentIDs, CrewPattern,
// MaxIterations) and the fail-loud env guards had zero regression cover.
//
// The split:
//
//	config.go  : env + embedded agentconfig YAML into a Config; returns errors,
//	             never exits. Fully unit-testable.
//	agents.go  : per-turn InstructionProviders + llmagent construction, with the
//	             adkmodel.LLM INJECTED so a test can pass a fake.
//	plugins.go : the four plugins + their pinned identity config + the launcher
//	             config.
//	run.go     : the thin glue that needs the network / a blocking serve
//	             loop: tracing.Init, modelgatewayclient.New,
//	             launcher Execute.
//
// main() keeps its package doc comment (env vars, ADR references, the two
// evaluator modes, the guardrail rationale, the deploy command) and shrinks to
// slog wiring plus one boot.Run* call whose error it fatals on.
package boot

import (
	"fmt"
	"os"

	"github.com/apollo-chora/chora-oe-grading/internal/agentconfig"
)

// Crew kinds. These are load-bearing identity strings: the Python orchestrator
// terminal-reads the ADK event author, the mana plugin stamps CrewKind as a
// cost-attribution label, and the termination event's CrewID is the joinable
// correlation column for cross-agent traces.
const (
	// CrewKindEvaluator is the grader of record (ADR-172 §D2/§D4/§D5).
	CrewKindEvaluator = "oe_evaluator"
	// CrewKindModerator is the judge (ADR-172 §D2).
	CrewKindModerator = "oe_moderator"
)

// Sub-agent keys inside each binary's embedded agentconfig YAML.
const (
	subAgentEvaluator = "evaluator"
	subAgentModerator = "moderator"
)

// Model-override env vars. Ops may override an individual primary for a quick
// experiment; tier, fallback chain and prompt version stay YAML-declared.
const (
	envEvaluatorModel = "OE_EVALUATOR_MODEL"
	envModeratorModel = "OE_MODERATOR_MODEL"
)

// Config is one binary's fully resolved boot configuration. Every field comes
// from an env var or the binary's embedded agentconfig YAML, never from an
// inline literal in a call site (feedback_no_inline_config).
type Config struct {
	// CrewKind is "oe_evaluator" or "oe_moderator". It doubles as the ADK agent
	// name and as the crew identity stamped on plugins and events.
	CrewKind string

	// SessionAppName is the ADK session app_name stamped onto every session
	// the subscriber creates. It namespaces sessions per binary; the default
	// is the crew's service name, overridable for experiments.
	SessionAppName string

	GatewayEndpoint string
	// GatewayAudience is the ID-token audience claim minted for the gateway
	// call. Read here rather than left to modelgatewayclient's internal
	// default, which lives in TWO places (client.go and image.go) and which
	// nothing could previously state or override. The default below is the
	// value the library already used, so this is not a behaviour change.
	GatewayAudience string
	GatewayTenantID string
	GatewayGCID     string

	// Model is the gateway logical_model_id: the agentconfig primary unless the
	// per-binary override env var is set.
	Model string
	// FallbackModels rides on InvokeRequest.fallback_logical_model_ids; the
	// gateway honours the agent-declared chain rather than a per-tier ladder.
	FallbackModels []string
	// PromptVersion selects the composer template version and is stamped onto
	// the active span by promptstamping (ADR-197 M-A).
	PromptVersion string

	ChoraEnv string
}

// EnvOr returns os.Getenv(name) if non-empty, else fallback.
func EnvOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// SafePrefix returns the first n characters of s, padded with "…" if truncated.
// Used to keep a GCID out of the logs in full.
func SafePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// LoadEvaluatorConfig resolves the oe_evaluator boot configuration from the
// process environment plus the embedded oe_evaluator.yaml. Every failure is
// returned as a descriptive error so the caller decides how to die: the guards
// themselves are unchanged from the inline main() they came from.
func LoadEvaluatorConfig() (Config, error) {
	return loadConfig(CrewKindEvaluator, subAgentEvaluator, envEvaluatorModel, agentconfig.OEEvaluator)
}

// LoadModeratorConfig resolves the oe_moderator boot configuration from the
// process environment plus the embedded oe_moderator.yaml.
func LoadModeratorConfig() (Config, error) {
	return loadConfig(CrewKindModerator, subAgentModerator, envModeratorModel, agentconfig.OEModerator)
}

// loadConfig is the shared resolution order, kept identical for both binaries:
// agentconfig load, sub-agent lookup, gateway credential guard, then the
// defaulted values. The order matters for the error a broken deployment sees
// first, so it mirrors the original main() line for line.
func loadConfig(crewKind, subAgent, modelEnv string, load func() (agentconfig.AgentConfig, error)) (Config, error) {
	raw, err := load()
	if err != nil {
		return Config{}, fmt.Errorf("%s: load agent config: %w", crewKind, err)
	}
	sub, err := raw.Sub(subAgent)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", crewKind, err)
	}

	// ADR-163 Phase 3.1 cutover. Both are required, with no in-memory fallback
	// per feedback_no_stubs_real_wiring: an agent that cannot attribute its LLM
	// spend to a tenant and a user must refuse to start, not quietly invent one.
	gatewayTenantID := os.Getenv("CHORA_GATEWAY_TENANT_ID")
	gatewayGCID := os.Getenv("CHORA_GATEWAY_GCID")
	if gatewayTenantID == "" || gatewayGCID == "" {
		return Config{}, fmt.Errorf("%s: CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required "+
			"(Phase 3.1 cutover per ADR-163; no in-memory fallback per feedback_no_stubs_real_wiring)", crewKind)
	}

	cfg := Config{
		CrewKind:        crewKind,
		SessionAppName:  EnvOr("CHORA_SESSION_APP_NAME", ServiceName(crewKind)),
		GatewayEndpoint: EnvOr("CHORA_GATEWAY_ENDPOINT", "gateway.chora.site:443"),
		GatewayAudience: EnvOr("CHORA_GATEWAY_AUDIENCE", "https://gateway.chora.site"),
		GatewayTenantID: gatewayTenantID,
		GatewayGCID:     gatewayGCID,
		Model:           EnvOr(modelEnv, sub.PrimaryModel),
		FallbackModels:  sub.FallbackModels,
		PromptVersion:   sub.PromptVersion,
		ChoraEnv:        EnvOr("CHORA_ENV", "dev"),
	}

	return cfg, nil
}

// LogRole is the per-binary slog key prefix: "evaluator" or "moderator". The
// two binaries log their model under a role-specific key, so the boot line
// stays greppable per agent in a shared log view.
func (c Config) LogRole() string {
	switch c.CrewKind {
	case CrewKindEvaluator:
		return subAgentEvaluator
	case CrewKindModerator:
		return subAgentModerator
	}
	return c.CrewKind
}

// LogAttrs renders the boot line's slog key/value pairs. The GCID is truncated
// to an 8-character prefix: it identifies the calling principal in a log
// without writing a full opaque user identifier to disk.
func (c Config) LogAttrs() []any {
	role := c.LogRole()
	return []any{
		"session_app_name", c.SessionAppName,
		role + "_model", c.Model,
		role + "_fallback", c.FallbackModels,
		"gateway_endpoint", c.GatewayEndpoint,
		"gateway_tenant_id", c.GatewayTenantID,
		"gateway_gcid_prefix", SafePrefix(c.GatewayGCID, 8),
		"chora_env", c.ChoraEnv,
	}
}
