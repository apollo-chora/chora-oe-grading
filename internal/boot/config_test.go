package boot

import (
	"errors"
	"strings"
	"testing"

	"github.com/apollo-chora/chora-oe-grading/internal/agentconfig"
)

// clearBootEnv blanks every env var the loaders read, so each case starts from
// a known baseline instead of inheriting the developer's shell. t.Setenv with
// an empty value reads as "unset" to EnvOr and to the guards, and still
// registers the restore, so the process env is left as it was found.
func clearBootEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CHORA_SESSION_APP_NAME",
		"CHORA_GATEWAY_ENDPOINT",
		"CHORA_GATEWAY_TENANT_ID",
		"CHORA_GATEWAY_GCID",
		"CHORA_GATEWAY_AUDIENCE",
		"OE_EVALUATOR_MODEL",
		"OE_EVALUATOR_FALLBACK_MODELS",
		"OE_MODERATOR_MODEL",
		"OE_MODERATOR_FALLBACK_MODELS",
		"CHORA_ENV",
	} {
		t.Setenv(k, "")
	}
}

// minimalBootEnv sets only what the fail-loud guards demand, so a test that is
// about defaults does not accidentally assert an override. The model ids are
// env-only and REQUIRED, so both binaries' model vars are set here; the
// fallback chains stay unset (the default declares no fallback).
func minimalBootEnv(t *testing.T) {
	t.Helper()
	clearBootEnv(t)
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "tenant-oe")
	t.Setenv("CHORA_GATEWAY_GCID", "0192f4c1-aaaa-bbbb-cccc-ddddeeeeffff")
	t.Setenv("OE_EVALUATOR_MODEL", "test-evaluator-model")
	t.Setenv("OE_MODERATOR_MODEL", "test-moderator-model")
}

// ---------------------------------------------------------------------------
// EnvOr
// ---------------------------------------------------------------------------

func TestEnvOr(t *testing.T) {
	t.Setenv("BOOT_TEST_SET", "value")
	t.Setenv("BOOT_TEST_EMPTY", "")

	if got := EnvOr("BOOT_TEST_SET", "fallback"); got != "value" {
		t.Errorf("EnvOr(set) = %q, want %q", got, "value")
	}
	if got := EnvOr("BOOT_TEST_EMPTY", "fallback"); got != "fallback" {
		t.Errorf("EnvOr(empty) = %q, want %q, an empty var must read as unset", got, "fallback")
	}
	if got := EnvOr("BOOT_TEST_NEVER_SET_AT_ALL", "fallback"); got != "fallback" {
		t.Errorf("EnvOr(missing) = %q, want %q", got, "fallback")
	}
}

// ---------------------------------------------------------------------------
// SafePrefix
// ---------------------------------------------------------------------------

func TestSafePrefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than n", "abc", 8, "abc"},
		{"exactly n is not truncated", "abcdefgh", 8, "abcdefgh"},
		{"one over n truncates", "abcdefghi", 8, "abcdefgh…"},
		{"empty", "", 8, ""},
		{"zero width", "abc", 0, "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafePrefix(tc.in, tc.n); got != tc.want {
				t.Errorf("SafePrefix(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fail-loud guards. Each one is a deployment defect that must stop the process,
// never a value the agent invents for itself.
// ---------------------------------------------------------------------------

func TestLoadConfig_missingGatewayTenantFailsLoud(t *testing.T) {
	for _, tc := range []struct {
		name    string
		load    func() (Config, error)
		crewKnd string
	}{
		{"evaluator", LoadEvaluatorConfig, CrewKindEvaluator},
		{"moderator", LoadModeratorConfig, CrewKindModerator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			minimalBootEnv(t)
			t.Setenv("CHORA_GATEWAY_TENANT_ID", "")

			_, err := tc.load()
			if err == nil {
				t.Fatalf("load succeeded without CHORA_GATEWAY_TENANT_ID: the ADR-163 Phase 3.1 guard is gone")
			}
			assertGatewayGuardMessage(t, err, tc.crewKnd)
		})
	}
}

func TestLoadConfig_missingGatewayGCIDFailsLoud(t *testing.T) {
	for _, tc := range []struct {
		name    string
		load    func() (Config, error)
		crewKnd string
	}{
		{"evaluator", LoadEvaluatorConfig, CrewKindEvaluator},
		{"moderator", LoadModeratorConfig, CrewKindModerator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			minimalBootEnv(t)
			t.Setenv("CHORA_GATEWAY_GCID", "")

			_, err := tc.load()
			if err == nil {
				t.Fatalf("load succeeded without CHORA_GATEWAY_GCID: the ADR-163 Phase 3.1 guard is gone")
			}
			assertGatewayGuardMessage(t, err, tc.crewKnd)
		})
	}
}

// assertGatewayGuardMessage pins the rationale in the guard message. The
// message is the only place an operator learns WHY there is no fallback, so a
// silent rewording that dropped the ADR reference would be a real loss.
func assertGatewayGuardMessage(t *testing.T, err error, crewKind string) {
	t.Helper()
	for _, want := range []string{
		crewKind,
		"CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required",
		"ADR-163",
		"no in-memory fallback per feedback_no_stubs_real_wiring",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("guard message %q missing %q", err, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

func TestLoadEvaluatorConfig_defaults(t *testing.T) {
	minimalBootEnv(t)

	cfg, err := LoadEvaluatorConfig()
	if err != nil {
		t.Fatalf("LoadEvaluatorConfig: %v", err)
	}

	assertEqual(t, "CrewKind", cfg.CrewKind, CrewKindEvaluator)
	assertEqual(t, "SessionAppName", cfg.SessionAppName, "chora-oe-evaluator")
	assertEqual(t, "GatewayEndpoint", cfg.GatewayEndpoint, "gateway.chora.site:443")
	assertEqual(t, "GatewayTenantID", cfg.GatewayTenantID, "tenant-oe")
	assertEqual(t, "ChoraEnv", cfg.ChoraEnv, "dev")

	// The embedded oe_evaluator.yaml is the single source of truth for the
	// HIGH text tier and the prompt version. The model id is env-only: the
	// REQUIRED OE_EVALUATOR_MODEL flows through verbatim, and the fallback
	// chain is empty unless OE_EVALUATOR_FALLBACK_MODELS is set.
	assertEqual(t, "Model", cfg.Model, "test-evaluator-model")
	assertEqual(t, "PromptVersion", cfg.PromptVersion, "v1")
	assertStringSlice(t, "FallbackModels", cfg.FallbackModels, nil)
}

func TestLoadModeratorConfig_defaults(t *testing.T) {
	minimalBootEnv(t)

	cfg, err := LoadModeratorConfig()
	if err != nil {
		t.Fatalf("LoadModeratorConfig: %v", err)
	}

	assertEqual(t, "CrewKind", cfg.CrewKind, CrewKindModerator)
	assertEqual(t, "SessionAppName", cfg.SessionAppName, "chora-oe-moderator")
	assertEqual(t, "GatewayEndpoint", cfg.GatewayEndpoint, "gateway.chora.site:443")
	assertEqual(t, "ChoraEnv", cfg.ChoraEnv, "dev")

	// The moderator is the CHEAP text tier: a fast accept/reject judge.
	assertEqual(t, "Model", cfg.Model, "test-moderator-model")
	assertEqual(t, "PromptVersion", cfg.PromptVersion, "v1")
	assertStringSlice(t, "FallbackModels", cfg.FallbackModels, nil)
}

// ---------------------------------------------------------------------------
// Model env guards. A blank *_MODEL var is a deployment defect that must stop
// the process, never a value the agent invents for itself.
// ---------------------------------------------------------------------------

func TestLoadConfig_missingModelFailsLoud(t *testing.T) {
	for _, tc := range []struct {
		name    string
		load    func() (Config, error)
		crewKnd string
		modelEnv string
	}{
		{"evaluator", LoadEvaluatorConfig, CrewKindEvaluator, "OE_EVALUATOR_MODEL"},
		{"moderator", LoadModeratorConfig, CrewKindModerator, "OE_MODERATOR_MODEL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			minimalBootEnv(t)
			t.Setenv(tc.modelEnv, "")

			_, err := tc.load()
			if err == nil {
				t.Fatalf("load succeeded without %s: the model id is env-only, there is no default", tc.modelEnv)
			}
			if !strings.Contains(err.Error(), tc.modelEnv) {
				t.Errorf("error %q must name the missing env var %s", err, tc.modelEnv)
			}
		})
	}
}

func TestLoadConfig_fallbackChainIsOptionalAndCommaSeparated(t *testing.T) {
	// Unset (the default) declares no fallback: the gateway is asked for the
	// primary target only.
	minimalBootEnv(t)
	cfg, err := LoadEvaluatorConfig()
	if err != nil {
		t.Fatalf("LoadEvaluatorConfig: %v", err)
	}
	assertStringSlice(t, "FallbackModels", cfg.FallbackModels, nil)

	// A comma-separated list becomes the declared chain, forwarded to the
	// gateway as fallback_logical_model_ids; blank entries are dropped.
	t.Setenv("OE_EVALUATOR_FALLBACK_MODELS", "stub-fb-1, stub-fb-2 ,")
	cfg, err = LoadEvaluatorConfig()
	if err != nil {
		t.Fatalf("LoadEvaluatorConfig: %v", err)
	}
	assertEqual(t, "Model", cfg.Model, "test-evaluator-model")
	assertStringSlice(t, "FallbackModels", cfg.FallbackModels, []string{"stub-fb-1", "stub-fb-2"})
}

// ---------------------------------------------------------------------------
// Overrides
// ---------------------------------------------------------------------------

func TestLoadEvaluatorConfig_everyOverride(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("CHORA_SESSION_APP_NAME", "oe-eval-experiment")
	t.Setenv("CHORA_GATEWAY_ENDPOINT", "gateway.internal:9090")
	t.Setenv("OE_EVALUATOR_MODEL", "env-experiment-model")
	t.Setenv("OE_EVALUATOR_FALLBACK_MODELS", "stub-fb-1,stub-fb-2")
	t.Setenv("CHORA_ENV", "prod")

	cfg, err := LoadEvaluatorConfig()
	if err != nil {
		t.Fatalf("LoadEvaluatorConfig: %v", err)
	}

	assertEqual(t, "SessionAppName", cfg.SessionAppName, "oe-eval-experiment")
	assertEqual(t, "GatewayEndpoint", cfg.GatewayEndpoint, "gateway.internal:9090")
	assertEqual(t, "Model", cfg.Model, "env-experiment-model")
	assertEqual(t, "ChoraEnv", cfg.ChoraEnv, "prod")

	// The env override replaces the primary only; tier and prompt version stay
	// YAML-declared, and the fallback chain comes from its own env var.
	assertStringSlice(t, "FallbackModels", cfg.FallbackModels, []string{"stub-fb-1", "stub-fb-2"})
	assertEqual(t, "PromptVersion", cfg.PromptVersion, "v1")
}

func TestLoadModeratorConfig_modelOverride(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("OE_MODERATOR_MODEL", "env-moderator-model")

	cfg, err := LoadModeratorConfig()
	if err != nil {
		t.Fatalf("LoadModeratorConfig: %v", err)
	}
	assertEqual(t, "Model", cfg.Model, "env-moderator-model")
	assertStringSlice(t, "FallbackModels", cfg.FallbackModels, nil)
}

func TestLoadConfig_evaluatorOverrideDoesNotLeakIntoModerator(t *testing.T) {
	// Both binaries ship in one image and read the same environment. An
	// evaluator override must not select the moderator's model.
	minimalBootEnv(t)
	t.Setenv("OE_EVALUATOR_MODEL", "env-experiment-model")

	cfg, err := LoadModeratorConfig()
	if err != nil {
		t.Fatalf("LoadModeratorConfig: %v", err)
	}
	if cfg.Model == "env-experiment-model" {
		t.Errorf("moderator picked up OE_EVALUATOR_MODEL: the two binaries share an env")
	}
	assertEqual(t, "Model", cfg.Model, "test-moderator-model")
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// LogRole + LogAttrs
// ---------------------------------------------------------------------------

func TestLogRole(t *testing.T) {
	assertEqual(t, "evaluator", Config{CrewKind: CrewKindEvaluator}.LogRole(), "evaluator")
	assertEqual(t, "moderator", Config{CrewKind: CrewKindModerator}.LogRole(), "moderator")
	// An unrecognised crew kind logs under its own name rather than a blank
	// key: a mislabelled boot line is still greppable.
	assertEqual(t, "unknown", Config{CrewKind: "oe_future"}.LogRole(), "oe_future")
}

func TestLogAttrs_evaluatorKeysAndOrder(t *testing.T) {
	cfg := Config{
		CrewKind:        CrewKindEvaluator,
		SessionAppName:  "chora-oe-evaluator",
		Model:           "test-evaluator-model",
		GatewayEndpoint: "gateway.chora.site:443",
		GatewayTenantID: "tenant-oe",
		GatewayGCID:     "0192f4c1-aaaa-bbbb-cccc-ddddeeeeffff",
		ChoraEnv:        "dev",
	}

	attrs := cfg.LogAttrs()
	if len(attrs)%2 != 0 {
		t.Fatalf("LogAttrs returned %d elements, want an even key/value count", len(attrs))
	}
	wantKeys := []string{
		"session_app_name",
		"evaluator_model", "evaluator_fallback",
		"gateway_endpoint", "gateway_tenant_id", "gateway_gcid_prefix",
		"chora_env",
	}
	for i, want := range wantKeys {
		got, ok := attrs[i*2].(string)
		if !ok {
			t.Fatalf("attr %d key is %T, want string", i*2, attrs[i*2])
		}
		if got != want {
			t.Errorf("attr %d key = %q, want %q", i, got, want)
		}
	}
}

func TestLogAttrs_moderatorUsesItsOwnModelKeys(t *testing.T) {
	attrs := Config{CrewKind: CrewKindModerator, Model: "test-moderator-model"}.LogAttrs()
	m := attrsToMap(t, attrs)
	if _, ok := m["moderator_model"]; !ok {
		t.Errorf("moderator boot line has no moderator_model key: %v", m)
	}
	if _, ok := m["evaluator_model"]; ok {
		t.Errorf("moderator boot line carries an evaluator_model key")
	}
}

func TestLogAttrs_gcidIsTruncatedNeverLoggedInFull(t *testing.T) {
	full := "0192f4c1-aaaa-bbbb-cccc-ddddeeeeffff"
	attrs := Config{CrewKind: CrewKindEvaluator, GatewayGCID: full}.LogAttrs()
	m := attrsToMap(t, attrs)

	prefix, ok := m["gateway_gcid_prefix"].(string)
	if !ok {
		t.Fatalf("gateway_gcid_prefix is %T, want string", m["gateway_gcid_prefix"])
	}
	if prefix != "0192f4c1…" {
		t.Errorf("gateway_gcid_prefix = %q, want %q", prefix, "0192f4c1…")
	}
	for k, v := range m {
		if s, ok := v.(string); ok && s == full {
			t.Errorf("attr %q logs the GCID in full", k)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func attrsToMap(t *testing.T, attrs []any) map[string]any {
	t.Helper()
	if len(attrs)%2 != 0 {
		t.Fatalf("odd attr count %d", len(attrs))
	}
	m := make(map[string]any, len(attrs)/2)
	for i := 0; i < len(attrs); i += 2 {
		k, ok := attrs[i].(string)
		if !ok {
			t.Fatalf("attr %d key is %T, want string", i, attrs[i])
		}
		m[k] = attrs[i+1]
	}
	return m
}

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertStringSlice(t *testing.T, field string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", field, i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Error wrapping in the shared loader. These paths cannot be reached through
// LoadEvaluatorConfig / LoadModeratorConfig because both embedded YAMLs are
// valid, so they are driven through the unexported loader directly. What they
// pin is the contract that a broken config surfaces WHICH binary failed, which
// is the only thing an operator has when two agents share one image.
// ---------------------------------------------------------------------------

func TestLoadConfig_agentconfigLoadFailureNamesTheBinary(t *testing.T) {
	minimalBootEnv(t)

	_, err := loadConfig(CrewKindEvaluator, subAgentEvaluator, envEvaluatorModel, envEvaluatorFallback,
		func() (agentconfig.AgentConfig, error) {
			return agentconfig.AgentConfig{}, errors.New("embedded YAML is corrupt")
		})
	if err == nil {
		t.Fatalf("loadConfig swallowed an agentconfig load failure")
	}
	for _, want := range []string{CrewKindEvaluator, "load agent config", "embedded YAML is corrupt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestLoadConfig_missingSubAgentNamesTheBinary(t *testing.T) {
	minimalBootEnv(t)

	_, err := loadConfig(CrewKindModerator, "no_such_sub_agent", envModeratorModel, envModeratorFallback, agentconfig.OEModerator)
	if err == nil {
		t.Fatalf("loadConfig accepted a sub-agent the YAML does not declare")
	}
	for _, want := range []string{CrewKindModerator, "no_such_sub_agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
