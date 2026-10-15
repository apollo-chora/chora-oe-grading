// Package agentconfig holds the build-time, per-agent tier + prompt
// configuration for the oe_grading crew's two ADK-Go agent binaries
// (oe_evaluator + oe_moderator) per ADR-172 §D2/§D3. AGENT-DRIVEN tiering —
// copies the qgen agentconfig shape (CR qgen 2026-06-01).
//
// Each agent owns its OWN embedded YAML (separation of concern) declaring,
// per sub-agent: tier / prompt_version. The model id is env-only: the boot
// layer reads the REQUIRED per-binary model env var and the optional
// *_FALLBACK_MODELS comma list (empty = no fallback) and:
//   - sends the model id as the gateway logical_model_id,
//   - sends the fallback chain as InvokeRequest.fallback_logical_model_ids (the
//     gateway honours the declared chain rather than hardcoding a per-tier
//     ladder),
//   - composes the prompt_version'd template (embedded v1).
//
// A blank model env var is a config error, never a silent default.
package agentconfig

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed oe_evaluator.yaml
var oeEvaluatorYAML []byte

//go:embed oe_moderator.yaml
var oeModeratorYAML []byte

// SubAgentConfig is one sub-agent's resolved tier + prompt selection. The
// model id is not declared here: boot reads it from the binary's REQUIRED
// model env var.
type SubAgentConfig struct {
	Tier          string `yaml:"tier"`
	PromptVersion string `yaml:"prompt_version"`
}

// AgentConfig is one agent binary's full per-sub-agent config.
type AgentConfig struct {
	Agent     string                    `yaml:"agent"`
	SubAgents map[string]SubAgentConfig `yaml:"sub_agents"`
}

// Sub returns the named sub-agent config, failing loud if the YAML omits it or
// leaves its prompt_version blank — a missing sub-agent or an unversioned
// prompt is a build/config error, never a silent default (per
// feedback_no_stubs_real_wiring). The model id is not declared here: boot
// reads it from the binary's REQUIRED model env var.
func (c AgentConfig) Sub(name string) (SubAgentConfig, error) {
	sc, ok := c.SubAgents[name]
	if !ok {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q has no sub-agent %q", c.Agent, name)
	}
	if sc.PromptVersion == "" {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q sub-agent %q has empty prompt_version", c.Agent, name)
	}
	return sc, nil
}

// OEEvaluator parses the embedded oe_evaluator config.
func OEEvaluator() (AgentConfig, error) { return parse(oeEvaluatorYAML) }

// OEModerator parses the embedded oe_moderator config.
func OEModerator() (AgentConfig, error) { return parse(oeModeratorYAML) }

func parse(raw []byte) (AgentConfig, error) {
	var c AgentConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return AgentConfig{}, fmt.Errorf("agentconfig: unmarshal: %w", err)
	}
	if c.Agent == "" {
		return AgentConfig{}, fmt.Errorf("agentconfig: missing top-level agent name")
	}
	if len(c.SubAgents) == 0 {
		return AgentConfig{}, fmt.Errorf("agentconfig: agent %q declares no sub_agents", c.Agent)
	}
	return c, nil
}
