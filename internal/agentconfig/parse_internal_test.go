package agentconfig

// Internal tests for the fail-loud parse + Sub guards.
//
// These branches are unreachable from the external test package because the two
// embedded YAML files are valid by construction. They are nonetheless the
// guards that stop a mis-edited YAML from composing an unversioned prompt —
// the model id guard itself lives in boot (REQUIRED *_MODEL env var), since
// the YAML declares no model name.

import (
	"strings"
	"testing"
)

func TestParse_MalformedYAMLFailsLoud(t *testing.T) {
	cfg, err := parse([]byte("agent: [oe_evaluator\nsub_agents:\n"))
	if err == nil {
		t.Fatalf("malformed YAML must fail loud; got config %+v", cfg)
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("error must name the unmarshal failure: got %q", err)
	}
}

func TestParse_MissingAgentNameFailsLoud(t *testing.T) {
	// Well-formed YAML, but no top-level `agent` key: the agent name is what
	// every downstream error message and O+ prompt stamp is keyed on.
	cfg, err := parse([]byte("sub_agents:\n  evaluator:\n    tier: high\n    prompt_version: v1\n"))
	if err == nil {
		t.Fatalf("YAML without a top-level agent name must fail loud; got config %+v", cfg)
	}
	if !strings.Contains(err.Error(), "missing top-level agent name") {
		t.Errorf("error must name the missing agent key: got %q", err)
	}
}

func TestParse_NoSubAgentsFailsLoud(t *testing.T) {
	// An agent that declares no sub_agents has nothing to run: every Sub()
	// lookup would fail later, at session time, on a real submission. Fail at
	// boot instead.
	cfg, err := parse([]byte("agent: oe_evaluator\n"))
	if err == nil {
		t.Fatalf("YAML declaring no sub_agents must fail loud; got config %+v", cfg)
	}
	if !strings.Contains(err.Error(), "declares no sub_agents") {
		t.Errorf("error must name the empty sub_agents map: got %q", err)
	}
	if !strings.Contains(err.Error(), "oe_evaluator") {
		t.Errorf("error must name the agent for operator triage: got %q", err)
	}
}

func TestParse_EmptySubAgentsMapFailsLoud(t *testing.T) {
	// The declared-but-empty form: `sub_agents: {}` parses cleanly into a
	// zero-length map, so the len check is the only thing standing between it
	// and a booted agent with no configured model.
	cfg, err := parse([]byte("agent: oe_moderator\nsub_agents: {}\n"))
	if err == nil {
		t.Fatalf("an empty sub_agents map must fail loud; got config %+v", cfg)
	}
	if !strings.Contains(err.Error(), "declares no sub_agents") {
		t.Errorf("error must name the empty sub_agents map: got %q", err)
	}
}

func TestSub_EmptyPromptVersionFailsLoud(t *testing.T) {
	// prompt_version selects the composer template version. Blank would compose
	// an unversioned prompt, breaking the ADR-141 D2 transparency stamp.
	c := AgentConfig{
		Agent: "oe_moderator",
		SubAgents: map[string]SubAgentConfig{
			"moderator": {Tier: "cheap"},
		},
	}
	sc, err := c.Sub("moderator")
	if err == nil {
		t.Fatalf("a blank prompt_version must fail loud; got config %+v", sc)
	}
	if !strings.Contains(err.Error(), "empty prompt_version") {
		t.Errorf("error must name the empty prompt_version: got %q", err)
	}
}
