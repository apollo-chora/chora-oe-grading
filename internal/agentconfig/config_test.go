package agentconfig_test

// Tests for the embedded per-agent model + prompt config (ADR-172 §D2/§D3).
//
// The YAML is the single source of truth for the agent-declared model route: the
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
// (and on the assess_summary narrative). It runs the single text route.
func TestOEEvaluator_DeclaresTheLongcatRoute(t *testing.T) {
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
		PrimaryModel:   "longcat-2.5-preview",
		FallbackModels: []string{"longcat-2.5-preview"},
		PromptVersion:  "v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("evaluator sub-agent config drifted from oe_evaluator.yaml\n got: %+v\nwant: %+v", got, want)
	}
}

// The moderator is a tool-free accept/reject judge over one structured input.
// It runs the same single text route as the evaluator.
func TestOEModerator_DeclaresTheLongcatRoute(t *testing.T) {
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
		PrimaryModel:   "longcat-2.5-preview",
		FallbackModels: []string{"longcat-2.5-preview"},
		PromptVersion:  "v1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moderator sub-agent config drifted from oe_moderator.yaml\n got: %+v\nwant: %+v", got, want)
	}
}

// Single-provider deployment: both agents route every text call to the one
// LongCat-2.5-Preview logical model, primary and fallback alike. A config edit
// that re-introduces a second provider would silently re-route real grading
// traffic, so assert the collapse explicitly.
func TestOEAgents_ShareTheSingleLongcatRoute(t *testing.T) {
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
	if evSub.PrimaryModel != modSub.PrimaryModel {
		t.Errorf("evaluator runs %q but moderator runs %q; both must route to the one text model",
			evSub.PrimaryModel, modSub.PrimaryModel)
	}
	if evSub.PrimaryModel != "longcat-2.5-preview" {
		t.Errorf("primary_model = %q, want %q", evSub.PrimaryModel, "longcat-2.5-preview")
	}
	for _, sub := range []struct {
		agent string
		cfg   agentconfig.SubAgentConfig
	}{{"oe_evaluator", evSub}, {"oe_moderator", modSub}} {
		if len(sub.cfg.FallbackModels) != 1 || sub.cfg.FallbackModels[0] != sub.cfg.PrimaryModel {
			t.Errorf("%s fallback chain = %v; the single-provider deployment declares the same model as primary and only fallback",
				sub.agent, sub.cfg.FallbackModels)
		}
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
