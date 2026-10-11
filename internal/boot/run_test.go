// Tests for the parts of run.go that are reachable without a live network.
//
// Most of run.go deliberately is not: tracing.Init wires the OTel exporter,
// the gateway client dials chora-model-gateway, and the subscriber ends in a
// blocking receive loop. What IS testable is the boundary: a config failure
// must abort BEFORE any of that happens, so a misconfigured container dies
// with a readable message instead of a network error that hides the real
// cause.
package boot

import (
	"context"
	"strings"
	"testing"
)

func TestRunEvaluator_returnsTheConfigErrorBeforeTouchingTheNetwork(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "")

	err := RunEvaluator(context.Background(), nil)
	if err == nil {
		t.Fatalf("RunEvaluator returned nil with no gateway tenant configured")
	}
	if !strings.Contains(err.Error(), "CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required") {
		t.Errorf("error %q is not the config guard: the run reached past config resolution", err)
	}
}

func TestRunModerator_returnsTheConfigErrorBeforeTouchingTheNetwork(t *testing.T) {
	minimalBootEnv(t)
	t.Setenv("CHORA_GATEWAY_GCID", "")

	err := RunModerator(context.Background(), nil)
	if err == nil {
		t.Fatalf("RunModerator returned nil with no gateway GCID configured")
	}
	assertGatewayGuardMessage(t, err, CrewKindModerator)
}

func TestNewGatewayLLM_refusesAnIncompleteConfig(t *testing.T) {
	// modelgatewayclient validates before it mints a token, so this path needs
	// no credentials. What it pins is that the wrap names the binary, the model
	// and the endpoint: with two agents in one image and a fallback chain, an
	// unqualified "gateway error" is not actionable.
	_, err := NewGatewayLLM(context.Background(), Config{
		CrewKind:        CrewKindEvaluator,
		GatewayEndpoint: "gateway.chora.site:443",
		Model:           "longcat-2.5-preview",
		// GatewayTenantID + GatewayGCID deliberately absent.
	})
	if err == nil {
		t.Fatalf("NewGatewayLLM built a client with no tenant and no GCID")
	}
	for _, want := range []string{"modelgatewayclient.New", CrewKindEvaluator, "longcat-2.5-preview", "gateway.chora.site:443"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}
