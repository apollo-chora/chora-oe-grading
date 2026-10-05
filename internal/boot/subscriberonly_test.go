// Tests for the subscriber-only boot contract (ADR-254 D6) at the OE pair.
//
// The web launcher is gone, so the binary takes no arguments, the dispatch
// role is mandatory, and the lane identity the subscriber serves is derived
// from the crew kind and nothing else.
package boot

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/plugin"
)

func TestRefuseArgs_namesTheRemovedLauncher(t *testing.T) {
	if err := refuseArgs(CrewKindEvaluator, nil); err != nil {
		t.Fatalf("no args must be accepted, got %v", err)
	}
	if err := refuseArgs(CrewKindEvaluator, []string{}); err != nil {
		t.Fatalf("empty args must be accepted, got %v", err)
	}
	err := refuseArgs(CrewKindModerator, []string{"web", "-port", "8080", "agentengine"})
	if err == nil {
		t.Fatalf("a stale web-launcher command line must be refused")
	}
	for _, want := range []string{CrewKindModerator, "takes no arguments", "ADR-254", "web"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestRunEvaluator_refusesPositionalArgsBeforeConfig(t *testing.T) {
	// Args are checked FIRST: a stale container command must die with the
	// launcher message even when the rest of the env is broken too, so the
	// operator reads the real cause and not a downstream config error.
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "")
	err := RunEvaluator(context.Background(), []string{"web", "-port", "8080", "agentengine"})
	if err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("want the positional-args refusal, got %v", err)
	}
	err = RunModerator(context.Background(), []string{"agentengine"})
	if err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("want the positional-args refusal, got %v", err)
	}
}

func TestDispatchServeConfig_mapsTheCrewToItsLaneIdentity(t *testing.T) {
	plugins := []*plugin.Plugin{}
	for crew, want := range map[string]struct{ role, service string }{
		CrewKindEvaluator: {"oe_evaluate", "chora-oe-evaluator"},
		CrewKindModerator: {"oe_moderate", "chora-oe-moderator"},
	} {
		cfg := Config{CrewKind: crew, SessionAppName: "chora-oe-evaluator"}
		sc, err := dispatchServeConfig(cfg, fakeRoot{}, plugins)
		if err != nil {
			t.Fatalf("%s: %v", crew, err)
		}
		if sc.AgentRole != want.role {
			t.Errorf("%s: AgentRole = %q, want %q", crew, sc.AgentRole, want.role)
		}
		if sc.ServiceName != want.service {
			t.Errorf("%s: ServiceName = %q, want %q", crew, sc.ServiceName, want.service)
		}
		if sc.AppName != cfg.SessionAppName {
			t.Errorf("%s: AppName = %q, want the session app label %q", crew, sc.AppName, cfg.SessionAppName)
		}
		if sc.RootAgent == nil || sc.Sessions == nil {
			t.Errorf("%s: RootAgent/Sessions must be wired (got %v / %v)", crew, sc.RootAgent, sc.Sessions)
		}
		if len(sc.Plugins.Plugins) != len(plugins) {
			t.Errorf("%s: plugin chain not threaded through", crew)
		}
	}
}

func TestDispatchServeConfig_refusesAnUnknownCrew(t *testing.T) {
	_, err := dispatchServeConfig(Config{CrewKind: "something_else"}, fakeRoot{}, nil)
	if err == nil || !strings.Contains(err.Error(), "something_else") {
		t.Fatalf("an unknown crew kind must be refused by name (a guessed role would subscribe to nothing), got %v", err)
	}
}
