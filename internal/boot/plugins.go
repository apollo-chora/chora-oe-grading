package boot

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"

	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/terminationplugin"
	"github.com/apollo-chora/chora-adk-common/tracing"
)

// Pinned termination identity (D6 P3 trace-emission contract). These strings
// travel on chora.ai_kernel.agent.terminated.v1 and are read downstream, so
// they are behaviour, not decoration.
const (
	// terminationAgentIDSuffix turns a crew kind into the termination AgentID:
	// "oe_evaluator" becomes "oe_evaluator_p1_single".
	terminationAgentIDSuffix = "_p1_single"
	// terminationRuntime tells subscribers which runtime terminated.
	terminationRuntime = "AGENT_EXECUTION_RUNTIME_ADK_GO"
	// terminationCrewPattern is the crew-composition pattern both OE-grading
	// agents run: a single tool-free agent per dispatch.
	terminationCrewPattern = "P1_SINGLE_AGENT"
	// terminationMaxIterations caps model calls per agent run at 3. Single-shot
	// per attempt: the Python orchestrator owns the outer evaluator to moderator
	// loop, so an agent that loops on its own is a bug to abort, not to absorb.
	terminationMaxIterations = 3
)

// StateString reads a string value from ADK session state. Returns "" on miss
// or type mismatch.
func StateString(state interface {
	Get(string) (any, error)
}, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// NewInboundTracePlugin builds the ADK plugin that links the agent's local
// trace to the orchestrator's inbound W3C traceparent (read from session state
// in a BeforeAgentCallback). Missing/malformed traceparent is a safe no-op.
func NewInboundTracePlugin(crewKind string) (*plugin.Plugin, error) {
	p, err := plugin.New(plugin.Config{
		Name: "chora_inbound_trace_" + crewKind,
		BeforeAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			traceparent := StateString(ctx.State(), "traceparent")
			if traceparent == "" {
				return nil, nil
			}
			tracestate := StateString(ctx.State(), "tracestate")
			tracing.AddInboundLink(ctx, traceparent, tracestate)
			return nil, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("inbound-trace plugin.New: %w", err)
	}
	return p, nil
}

// NewTenantPropagationPlugin builds the per-request tenant propagation plugin
// (ADR-169). It stamps the REQUESTING tenant_id / user_gcid, threaded into
// session state by the orchestrator, onto each gateway LLMRequest so RLS,
// token-usage ledger and budget attribute to the learner's tenant rather than
// the process-fixed env tenant.
func NewTenantPropagationPlugin(crewKind string) (*plugin.Plugin, error) {
	p, err := modelgatewayclient.NewTenantPropagationPlugin(crewKind)
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.NewTenantPropagationPlugin: %w", err)
	}
	return p, nil
}

// TerminationConfig is the pinned termination-plugin configuration for a crew
// kind. Exposed separately from the constructor so the identity strings are
// directly assertable: a rename here silently breaks the orchestrator's
// terminal read of the event author.
func TerminationConfig(crewKind string) terminationplugin.Config {
	return terminationplugin.Config{
		Publisher:     &terminationplugin.LoggingPublisher{},
		AgentID:       crewKind + terminationAgentIDSuffix,
		Runtime:       terminationRuntime,
		CrewKind:      crewKind,
		CrewPattern:   terminationCrewPattern,
		MaxIterations: terminationMaxIterations,
	}
}

// NewTerminationPlugin builds the plugin that emits
// chora.ai_kernel.agent.terminated.v1 once per invocation (D6 P3
// trace-emission contract via the plugin's OTel span).
func NewTerminationPlugin(crewKind string) (*plugin.Plugin, error) {
	p, err := terminationplugin.New(TerminationConfig(crewKind))
	if err != nil {
		return nil, fmt.Errorf("terminationplugin.New: %w", err)
	}
	return p, nil
}

// NewPlugins builds the crew's plugin chain in its runtime order:
// [inboundTrace, mana, tenantProp, termination]. This is the qgen chain, and
// the order is load-bearing: the trace link must exist before the mana gate can
// refuse a run, and tenant propagation must have stamped the request before the
// termination plugin counts the model call.
//
// There is no agent-side guardrail screen anywhere in the chain. The
// guardrail screen runs at chora-model-gateway (the single un-bypassable LLM
// chokepoint, ADR-152 / ADR-163), and the second screen is orchestrator-side
// in the Python graph. An agent-side OnUserMessageCallback would screen the
// "BEGIN" trigger, not the learner answer, which rides in the instruction.
func NewPlugins(crewKind string) ([]*plugin.Plugin, error) {
	inboundTraceP, err := NewInboundTracePlugin(crewKind)
	if err != nil {
		return nil, err
	}
	tenantPropP, err := NewTenantPropagationPlugin(crewKind)
	if err != nil {
		return nil, err
	}
	terminationP, err := NewTerminationPlugin(crewKind)
	if err != nil {
		return nil, err
	}
	return []*plugin.Plugin{inboundTraceP, tenantPropP, terminationP}, nil
}
