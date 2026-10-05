package boot

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/tracing"
)

// newAgentFunc is the per-binary root-agent constructor. It takes the resolved
// Config and the injected LLM, so the only thing that varies between the two
// binaries is which prompt composers and conditions extractor get wired.
type newAgentFunc func(cfg Config, llm adkmodel.LLM) (agent.Agent, error)

// RunEvaluator boots and serves the oe_evaluator agent as a subscriber-only
// process (ADR-254 D6). It returns rather than exits, so main() owns the
// process exit; a non-nil error means the container must die.
func RunEvaluator(ctx context.Context, args []string) error {
	if err := refuseArgs(CrewKindEvaluator, args); err != nil {
		return err
	}
	cfg, err := LoadEvaluatorConfig()
	if err != nil {
		return err
	}
	return run(ctx, cfg, NewEvaluatorAgent)
}

// RunModerator boots and serves the oe_moderator agent, same contract.
func RunModerator(ctx context.Context, args []string) error {
	if err := refuseArgs(CrewKindModerator, args); err != nil {
		return err
	}
	cfg, err := LoadModeratorConfig()
	if err != nil {
		return err
	}
	return run(ctx, cfg, NewModeratorAgent)
}

// refuseArgs rejects any command-line argument. The ADK web launcher and its
// "web -port 8080 ... agentengine" command line were removed by ADR-254 D6;
// the binary is subscriber-only. Refusing, rather than ignoring, the old
// arguments means a Deployment whose command was not updated dies with the
// cause in its exit line instead of silently running a different program.
func refuseArgs(crewKind string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("%s takes no arguments, got %q: the ADK web launcher "+
		"(\"web -port ... agentengine\") was removed by ADR-254 D6 and this binary "+
		"is subscriber-only; update the Deployment command", crewKind, args)
}

// run is the glue that cannot be exercised by a unit test: it needs a network
// dial to chora-model-gateway, and it ends in the dispatch subscriber's
// blocking receive loop. Everything that could move out of it has, so what
// remains is process wiring.
func run(ctx context.Context, cfg Config, newAgent newAgentFunc) error {
	// OTel tracer + W3C propagator BEFORE any agent/plugin construction so
	// spans flow to the configured OTLP endpoint and continue the
	// orchestrator's inbound traceparent (carried on the dispatch envelope,
	// read by the inbound-trace plugin from session state).
	traceShutdown, err := tracing.Init(ctx, cfg.CrewKind)
	if err != nil {
		return fmt.Errorf("tracing.Init: %w", err)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			slog.Error("trace shutdown error", "err", err)
		}
	}()

	slog.Info(cfg.CrewKind+" boot", cfg.LogAttrs()...)

	// Single gateway-fronted LLM client per binary. The evaluator runs BOTH of
	// its modes (evaluate + assess_summary) through this one client: mode is a
	// per-turn instruction switch, not a separate model.
	llm, err := NewGatewayLLM(ctx, cfg)
	if err != nil {
		return err
	}

	root, err := newAgent(cfg, llm)
	if err != nil {
		return err
	}

	plugins, err := NewPlugins(cfg.CrewKind)
	if err != nil {
		return err
	}

	serveCfg, err := dispatchServeConfig(cfg, root, plugins)
	if err != nil {
		return err
	}

	// ADR-254 D6: the NATS JetStream dispatch subscriber is the ONLY transport.
	// The health port serves /healthz and /readyz, nothing else; a subscriber
	// that cannot start, or stops, returns here and main() exits non-zero, so
	// the container dies instead of idling healthy while every dispatch times
	// out.
	return agentdispatch.RunSubscriberOnly(ctx, serveCfg, agentdispatch.RunOptions{})
}

// dispatchServeConfig derives the lane identity the subscriber serves from the
// crew kind. Three strings are in play and deliberately different (see
// dispatch.go): the ADK agent name, the dispatch role the orchestrator keys
// topics on, and the service name the subscription is named after. An unknown
// crew kind is refused rather than guessed: a wrong role subscribes to a
// subject nobody publishes to, and the container would look healthy while
// every dispatch timed out.
func dispatchServeConfig(cfg Config, root agent.Agent, plugins []*plugin.Plugin) (agentdispatch.ServeConfig, error) {
	role := DispatchRole(cfg.CrewKind)
	if role == "" {
		return agentdispatch.ServeConfig{}, fmt.Errorf(
			"crew kind %q has no dispatch role: refusing to guess one", cfg.CrewKind)
	}
	return agentdispatch.ServeConfig{
		AgentRole:   role,
		ServiceName: ServiceName(cfg.CrewKind),
		AppName:     cfg.SessionAppName,
		RootAgent:   root,
		// In-memory sessions: one session per dispatch, keyed on the execution
		// id, created and discarded by the subscriber (see agentdispatch/run.go).
		// The OE pair is stateless single-turn per dispatch.
		Sessions: session.InMemoryService(),
		Plugins:  runner.PluginConfig{Plugins: plugins},
	}, nil
}

// CrewSurface is the crew id stamped as InvokeRequest.surface on every model
// call from either OE binary (ADR-254 D7): the gateway's surface gate and its
// suspension read key on it, so it is wire identity, not a label.
const CrewSurface = "oe_grading"

// gatewayConfig is the gateway client configuration for one binary: the crew
// kind is both the agent id (routing policy) and the crew tag; the surface is
// the crew id. Pure, so the identity strings are assertable without a live
// gateway connection.
func gatewayConfig(cfg Config) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:         cfg.GatewayEndpoint,
		LogicalModelID:   cfg.Model,
		FallbackModelIDs: cfg.FallbackModels,
		AgentID:          cfg.CrewKind,
		CrewKind:         cfg.CrewKind,
		Surface:          CrewSurface,
		TenantID:         cfg.GatewayTenantID,
		GCID:             cfg.GatewayGCID,
		Audience:         cfg.GatewayAudience,
	}
}

// NewGatewayLLM builds the chora-model-gateway-fronted adkmodel.LLM (ADR-163
// Phase 3.1). This is the single un-bypassable LLM chokepoint: the gateway's
// guardrail screen and the token-usage ledger both live behind it, so no agent
// may reach a model provider any other way. Constructing it opens a gRPC
// client (TLS + CHORA_GATEWAY_TOKEN in production, plaintext for local dev),
// which is why it stays out of agents.go.
func NewGatewayLLM(ctx context.Context, cfg Config) (adkmodel.LLM, error) {
	m, err := modelgatewayclient.New(ctx, gatewayConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.New(%s, %s @ %s): %w",
			cfg.CrewKind, cfg.Model, cfg.GatewayEndpoint, err)
	}
	return m, nil
}
