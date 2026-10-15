package agentconfig_test

// Tests for the embedded per-agent tier + prompt config (ADR-172 §D2/§D3).
//
// The YAML is the single source of truth for the AGENT-DRIVEN tier ladder and
// the prompt version: the agent sends its env-provided model id as the gateway
// logical_model_id and the env-declared fallback chain as
// InvokeRequest.fallback_logical_model_ids, and the gateway HONOURS the declared
// chain rather than applying a per-tier ladder of its own. The model id itself
// is env-only (a blank REQUIRED model var fails the boot), so what these tests
// pin is that the YAML declares no model name, the tiers, and the prompt
// version — a silent drift in the tier split would re-price real grading
// traffic.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/apollo-chora/chora-oe-grading/internal/agentconfig"
)

// The evaluator is the grader of record: a learner's grade rides on its output
// (and on the assess_summary narrative), so it runs the HIGH text tier.
func TestOEEvaluator_HighTier(t *testing.T) {
	cfg, err := agentconfig.OEEvaluator()
	if err != nil {
		t.Fatalf("OEEvaluator: %v", err)
	}
	if cfg.Agent != "oe_evaluator" {
		t.Errorf("agent name: got %q want %q", cfg.Agent, "oe_evaluator")
	}
	if len(cfg.SubAgents) != 1 {
		t.Errorf("oe_evaluator declares %d sub-agents, want exactly 1 (evaluator): %v",
			len(cfg.SubAgents), cfg.SubAgents)
	}

	got, err := cfg.Sub("evaluator")
	if err != nil {
		t.Fatalf("Sub(evaluator): %v", err)
	}
	// The model id is env-only: the YAML declares tier + prompt version and
	// NO primary/fallback — boot fails loud when the REQUIRED
	// OE_EVALUATOR_MODEL is unset.
	want := agentconfig.SubAgentConfig{
		Tier:          "high",
		PromptVersion: "v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("evaluator sub-agent config drifted from oe_evaluator.yaml\n got: %+v\nwant: %+v", got, want)
	}
}

// The moderator is a tool-free accept/reject judge over one structured input,
// so it runs the CHEAP text tier (mirrors qgen_critic).
func TestOEModerator_CheapTier(t *testing.T) {
	cfg, err := agentconfig.OEModerator()
	if err != nil {
		t.Fatalf("OEModerator: %v", err)
	}
	if cfg.Agent != "oe_moderator" {
		t.Errorf("agent name: got %q want %q", cfg.Agent, "oe_moderator")
	}
	if len(cfg.SubAgents) != 1 {
		t.Errorf("oe_moderator declares %d sub-agents, want exactly 1 (moderator): %v",
			len(cfg.SubAgents), cfg.SubAgents)
	}

	got, err := cfg.Sub("moderator")
	if err != nil {
		t.Fatalf("Sub(moderator): %v", err)
	}
	want := agentconfig.SubAgentConfig{
		Tier:          "cheap",
		PromptVersion: "v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moderator sub-agent config drifted from oe_moderator.yaml\n got: %+v\nwant: %+v", got, want)
	}
}

// The two agents declare DIFFERENT tiers on purpose (ADR-172 §D2): the grader
// of record is expensive, the judge is cheap. The tier LABEL is what carries
// the split — never a model id in the YAML.
func TestOEAgents_TiersSplit(t *testing.T) {
	ev, err := agentconfig.OEEvaluator()
	if err != nil {
		t.Fatalf("OEEvaluator: %v", err)
	}
	mod, err := agentconfig.OEModerator()
	if err != nil {
		t.Fatalf("OEModerator: %v", err)
	}
	evSub, err := ev.Sub("evaluator")
	if err != nil {
		t.Fatalf("Sub(evaluator): %v", err)
	}
	modSub, err := mod.Sub("moderator")
	if err != nil {
		t.Fatalf("Sub(moderator): %v", err)
	}
	if evSub.Tier == modSub.Tier {
		t.Errorf("evaluator and moderator share tier %q; ADR-172 §D2 puts the grader on high and the judge on cheap",
			evSub.Tier)
	}
	if evSub.Tier != "high" || modSub.Tier != "cheap" {
		t.Errorf("tiers = %q/%q, want high/cheap", evSub.Tier, modSub.Tier)
	}
}

// A missing sub-agent is a build/config error, never a silent default: the
// agent boots against a name it hardcodes, so a typo in either side must abort
// the boot rather than fall back to an unconfigured model.
func TestSub_UnknownSubAgentFailsLoud(t *testing.T) {
	cases := []struct {
		name  string
		load  func() (agentconfig.AgentConfig, error)
		agent string
	}{
		{"oe_evaluator", agentconfig.OEEvaluator, "oe_evaluator"},
		{"oe_moderator", agentconfig.OEModerator, "oe_moderator"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := c.load()
			if err != nil {
				t.Fatalf("load %s: %v", c.name, err)
			}
			sub, err := cfg.Sub("critic")
			if err == nil {
				t.Fatalf("Sub(critic) on %s must fail loud; got config %+v", c.name, sub)
			}
			if !strings.Contains(err.Error(), "no sub-agent") {
				t.Errorf("error must name the failure mode: got %q", err)
			}
			if !strings.Contains(err.Error(), c.agent) {
				t.Errorf("error must name the agent %q for operator triage: got %q", c.agent, err)
			}
		})
	}
}
