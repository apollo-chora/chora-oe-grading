package boot

import "testing"

// The three identity strings in play are DELIBERATELY different and are easy to
// confuse: the ADK agent name (oe_evaluator), the dispatch role the orchestrator
// keys subjects on (oe_evaluate), and the service name (chora-oe-evaluator).
// Getting any of them wrong subscribes the binary to a subject nobody
// publishes to, and the container comes up healthy while every dispatch times
// out.

func TestDispatchRoleMapsFromTheCrewKind(t *testing.T) {
	for crew, want := range map[string]string{
		CrewKindEvaluator: "oe_evaluate",
		CrewKindModerator: "oe_moderate",
	} {
		if got := DispatchRole(crew); got != want {
			t.Errorf("DispatchRole(%q) = %q, want %q", crew, got, want)
		}
	}
}

func TestDispatchRoleIsEmptyForAnUnknownCrew(t *testing.T) {
	// Empty is refused by Serve, which is the right outcome: a guessed role
	// would subscribe to a topic that does not exist.
	if got := DispatchRole("something_else"); got != "" {
		t.Errorf("DispatchRole(unknown) = %q, want empty", got)
	}
}

func TestServiceNameMatchesTheDeployedServices(t *testing.T) {
	for crew, want := range map[string]string{
		CrewKindEvaluator: "chora-oe-evaluator",
		CrewKindModerator: "chora-oe-moderator",
	} {
		if got := ServiceName(crew); got != want {
			t.Errorf("ServiceName(%q) = %q, want %q", crew, got, want)
		}
	}
}
