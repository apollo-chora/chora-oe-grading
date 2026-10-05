package agentconfig_test

// Tests for the embedded per-agent model + prompt config (ADR-172 §D2/§D3).
//
// The YAML is the single source of truth for the AGENT-DRIVEN tier ladder: the
// agent sends primary_model as the gateway logical_model_id and fallback_models
// as InvokeRequest.fallback_logical_model_ids, and the gateway HONOURS the
// declared chain rather than applying a per-tier ladder of its own. A silent
// drift in these declared values therefore re-routes real grading traffic to a
// different model, so the tests assert the declared values verbatim.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/apollo-chora/chora-oe-grading/internal/agentconfig"
)

// The evaluator is the grader of record: a learner's grade rides on its output
// (and on the assess_summary narrative), so it runs the HIGH text tier.
func TestOEEvaluator_HighTierLadder(t *testing.T) {
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
	want := agentconfig.SubAgentConfig{
		Tier:           "high",
		PrimaryModel:   "gemini-3.1-pro-preview",
		FallbackModels: []string{"gemini-2.5-pro"},
		PromptVersion:  "v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("evaluator sub-agent config drifted from oe_evaluator.yaml\n got: %+v\nwant: %+v", got, want)
	}
}

// The moderator is a tool-free accept/reject judge over one structured input,
// so it runs the CHEAP text tier (mirrors qgen_critic).
func TestOEModerator_CheapTierLadder(t *testing.T) {
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
		Tier:           "cheap",
		PrimaryModel:   "gemini-3.5-flash",
		FallbackModels: []string{"gemini-2.5-flash"},
		PromptVersion:  "v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moderator sub-agent config drifted from oe_moderator.yaml\n got: %+v\nwant: %+v", got, want)
	}
}

// The two agents declare DIFFERENT tiers on purpose (ADR-172 §D2): the grader
// of record is expensive, the judge is cheap. A config edit that collapses them
// onto one model erases that decision, so assert the split explicitly.
func TestOEAgents_EvaluatorAndModeratorRunDifferentPrimaries(t *testing.T) {
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
	if evSub.PrimaryModel == modSub.PrimaryModel {
		t.Errorf("evaluator and moderator share primary_model %q; the tier split must reach the model id",
			evSub.PrimaryModel)
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
