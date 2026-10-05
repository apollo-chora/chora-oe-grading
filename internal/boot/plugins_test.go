// Tests for the plugin chain and the launcher wiring.
//
// The identity strings pinned here are wire contract, not cosmetics: the
// orchestrator terminal-reads the ADK event author, and the termination event's
// AgentID / CrewKind / CrewPattern land on
// chora.ai_kernel.agent.terminated.v1 where D6 chaos forensics joins on them.
// A rename that compiles is exactly the kind of change these tests exist to
// catch.
package boot

import (
	"testing"
)

// ---------------------------------------------------------------------------
// StateString
// ---------------------------------------------------------------------------

func TestStateString(t *testing.T) {
	state := newFakeState(map[string]any{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"attempt":     float64(2),
		"nil_value":   nil,
	})

	if got := StateString(state, "traceparent"); got != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Errorf("hit: StateString = %q", got)
	}
	if got := StateString(state, "absent"); got != "" {
		t.Errorf("miss: StateString = %q, want empty", got)
	}
	if got := StateString(state, "attempt"); got != "" {
		t.Errorf("wrong type: StateString = %q, want empty rather than a coerced number", got)
	}
	if got := StateString(state, "nil_value"); got != "" {
		t.Errorf("nil value: StateString = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// NewInboundTracePlugin
// ---------------------------------------------------------------------------

func TestNewInboundTracePlugin_nameCarriesTheCrewKind(t *testing.T) {
	for crewKind, want := range map[string]string{
		CrewKindEvaluator: "chora_inbound_trace_oe_evaluator",
		CrewKindModerator: "chora_inbound_trace_oe_moderator",
	} {
		p, err := NewInboundTracePlugin(crewKind)
		if err != nil {
			t.Fatalf("NewInboundTracePlugin(%s): %v", crewKind, err)
		}
		if p.Name() != want {
			t.Errorf("plugin name = %q, want %q", p.Name(), want)
		}
	}
}

func TestNewInboundTracePlugin_linksWhenTheOrchestratorStampedATraceparent(t *testing.T) {
	p, err := NewInboundTracePlugin(CrewKindEvaluator)
	if err != nil {
		t.Fatalf("NewInboundTracePlugin: %v", err)
	}
	cb := p.BeforeAgentCallback()
	if cb == nil {
		t.Fatalf("plugin has no BeforeAgentCallback: the inbound trace link would never be attempted")
	}

	content, err := cb(newFakeCallbackCtx(map[string]any{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"tracestate":  "chora=orchestrator",
	}))
	if err != nil {
		t.Errorf("callback returned error on the linking path: %v", err)
	}
	if content != nil {
		t.Errorf("callback returned content %v: it must not short-circuit the agent run", content)
	}
}

func TestNewInboundTracePlugin_noOpsWithoutATraceparent(t *testing.T) {
	p, err := NewInboundTracePlugin(CrewKindEvaluator)
	if err != nil {
		t.Fatalf("NewInboundTracePlugin: %v", err)
	}
	cb := p.BeforeAgentCallback()

	for _, tc := range []struct {
		name  string
		state map[string]any
	}{
		{"absent", map[string]any{}},
		{"empty string", map[string]any{"traceparent": ""}},
		{"wrong type", map[string]any{"traceparent": float64(7)}},
		{"malformed", map[string]any{"traceparent": "not-a-traceparent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := cb(newFakeCallbackCtx(tc.state))
			if err != nil {
				t.Errorf("callback returned error: a missing or malformed traceparent must be a safe no-op, not a failed run: %v", err)
			}
			if content != nil {
				t.Errorf("callback returned content %v, want nil", content)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Termination plugin identity
// ---------------------------------------------------------------------------

func TestTerminationConfig_pinnedIdentity(t *testing.T) {
	for _, tc := range []struct {
		crewKind    string
		wantAgentID string
	}{
		{CrewKindEvaluator, "oe_evaluator_p1_single"},
		{CrewKindModerator, "oe_moderator_p1_single"},
	} {
		t.Run(tc.crewKind, func(t *testing.T) {
			cfg := TerminationConfig(tc.crewKind)

			if cfg.AgentID != tc.wantAgentID {
				t.Errorf("AgentID = %q, want %q", cfg.AgentID, tc.wantAgentID)
			}
			if cfg.CrewKind != tc.crewKind {
				t.Errorf("CrewKind = %q, want %q", cfg.CrewKind, tc.crewKind)
			}
			if cfg.CrewPattern != "P1_SINGLE_AGENT" {
				t.Errorf("CrewPattern = %q, want %q", cfg.CrewPattern, "P1_SINGLE_AGENT")
			}
			if cfg.Runtime != "AGENT_EXECUTION_RUNTIME_ADK_GO" {
				t.Errorf("Runtime = %q, want %q", cfg.Runtime, "AGENT_EXECUTION_RUNTIME_ADK_GO")
			}
			// Single-shot per attempt. The Python orchestrator owns the outer
			// evaluator to moderator loop, so 3 is a cap on an agent looping on
			// its own, not a budget for the grading loop.
			if cfg.MaxIterations != 3 {
				t.Errorf("MaxIterations = %d, want 3", cfg.MaxIterations)
			}
			if cfg.Publisher == nil {
				t.Errorf("Publisher is nil: terminationplugin.New would refuse to build")
			}
			// AGID stays empty: these are system-owned agents, not A2A-published
			// ones, so they hold no per-instance agent identity.
			if cfg.AgentAGID != "" {
				t.Errorf("AgentAGID = %q, want empty for a system-owned agent", cfg.AgentAGID)
			}
		})
	}
}

func TestNewTerminationPlugin_nameEmbedsTheAgentID(t *testing.T) {
	p, err := NewTerminationPlugin(CrewKindEvaluator)
	if err != nil {
		t.Fatalf("NewTerminationPlugin: %v", err)
	}
	if p.Name() != "chora_termination_oe_evaluator_p1_single" {
		t.Errorf("plugin name = %q, want %q", p.Name(), "chora_termination_oe_evaluator_p1_single")
	}
}

// ---------------------------------------------------------------------------
// Tenant propagation
// ---------------------------------------------------------------------------

func TestNewTenantPropagationPlugin_nameCarriesTheCrewKind(t *testing.T) {
	p, err := NewTenantPropagationPlugin(CrewKindEvaluator)
	if err != nil {
		t.Fatalf("NewTenantPropagationPlugin: %v", err)
	}
	if p.Name() != "chora_gateway_tenant_propagation_oe_evaluator" {
		t.Errorf("plugin name = %q, want %q", p.Name(), "chora_gateway_tenant_propagation_oe_evaluator")
	}
}

// ---------------------------------------------------------------------------
// The chain
// ---------------------------------------------------------------------------

func TestNewPlugins_orderIsTheQgenChain(t *testing.T) {
	for _, tc := range []struct {
		crewKind string
		want     []string
	}{
		{CrewKindEvaluator, []string{
			"chora_inbound_trace_oe_evaluator",
			"chora_gateway_tenant_propagation_oe_evaluator",
			"chora_termination_oe_evaluator_p1_single",
		}},
		{CrewKindModerator, []string{
			"chora_inbound_trace_oe_moderator",
			"chora_gateway_tenant_propagation_oe_moderator",
			"chora_termination_oe_moderator_p1_single",
		}},
	} {
		t.Run(tc.crewKind, func(t *testing.T) {
			plugins, err := NewPlugins(tc.crewKind)
			if err != nil {
				t.Fatalf("NewPlugins: %v", err)
			}
			if len(plugins) != len(tc.want) {
				t.Fatalf("chain has %d plugins, want %d", len(plugins), len(tc.want))
			}
			for i, want := range tc.want {
				if plugins[i].Name() != want {
					t.Errorf("plugin %d = %q, want %q", i, plugins[i].Name(), want)
				}
			}
		})
	}
}

func TestNewPlugins_carriesNoGuardrailScreen(t *testing.T) {
	// There is deliberately no agent-side guardrail plugin. The guardrail
	// screen runs at chora-model-gateway, and the orchestrator holds the
	// untrusted learner answer. An agent-side OnUserMessageCallback would
	// screen the "BEGIN" trigger, not the answer, which is a screen that looks
	// real and inspects nothing.
	plugins, err := NewPlugins(CrewKindEvaluator)
	if err != nil {
		t.Fatalf("NewPlugins: %v", err)
	}
	for _, p := range plugins {
		if p.OnUserMessageCallback() != nil {
			t.Errorf("plugin %q registers an OnUserMessageCallback: it would screen the BEGIN trigger, not the learner answer", p.Name())
		}
	}
}
