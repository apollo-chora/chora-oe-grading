package boot

import "testing"

// ADR-254 D7: every Invoke from this crew is stamped surface=oe_grading (the
// crew id), and the agent id stays the binary's crew kind. Both strings are
// wire identity the gateway keys its surface gate and routing policy on.
func TestGatewayConfig_stampsTheCrewSurfaceAndAgentID(t *testing.T) {
	for _, crew := range []string{CrewKindEvaluator, CrewKindModerator} {
		cfg := Config{
			CrewKind: crew, GatewayEndpoint: "gateway.chora.site:443", Model: "gemini-3.1-pro-preview",
			FallbackModels: []string{"gemini-2.5-pro"}, GatewayTenantID: "t", GatewayGCID: "g",
		}
		gw := gatewayConfig(cfg)
		if gw.Surface != CrewSurface {
			t.Errorf("%s: Surface = %q, want %q", crew, gw.Surface, CrewSurface)
		}
		if CrewSurface != "oe_grading" {
			t.Errorf("CrewSurface = %q, want the crew id oe_grading", CrewSurface)
		}
		if gw.AgentID != crew || gw.CrewKind != crew {
			t.Errorf("%s: AgentID/CrewKind = %q/%q, want the crew kind", crew, gw.AgentID, gw.CrewKind)
		}
		if gw.Endpoint != cfg.GatewayEndpoint || gw.LogicalModelID != cfg.Model || len(gw.FallbackModelIDs) != 1 ||
			gw.TenantID != "t" || gw.GCID != "g" {
			t.Errorf("%s: gateway config not carried through: %+v", crew, gw)
		}
	}
}
