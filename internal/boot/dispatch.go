package boot

// Dispatch identity mapping for the ADR-253 event-driven agent-dispatch lane.
//
// Three identity strings are in play and they are deliberately different:
//
//	CrewKind      oe_evaluator        the ADK agent name; the event AUTHOR
//	DispatchRole  oe_evaluate         what the orchestrator keys subjects on
//	ServiceName   chora-oe-evaluator  the service name; names the subscription
//
// The role names come from the orchestrator's ROLE_OE_EVALUATE /
// ROLE_OE_MODERATE constants, which are the producer side of the wire.

const (
	// DispatchRoleEvaluate matches ROLE_OE_EVALUATE in the Python executor.
	DispatchRoleEvaluate = "oe_evaluate"
	// DispatchRoleModerate matches ROLE_OE_MODERATE.
	DispatchRoleModerate = "oe_moderate"
)

// DispatchRole is the dispatch role for a crew kind, or "" when unknown.
// Empty is refused by agentdispatch.Serve rather than guessed: a wrong role
// subscribes to a subject nobody publishes to, and the container would come
// up healthy while every dispatch silently timed out.
func DispatchRole(crewKind string) string {
	switch crewKind {
	case CrewKindEvaluator:
		return DispatchRoleEvaluate
	case CrewKindModerator:
		return DispatchRoleModerate
	}
	return ""
}

// ServiceName is the service name backing a crew kind.
func ServiceName(crewKind string) string {
	switch crewKind {
	case CrewKindEvaluator:
		return "chora-oe-evaluator"
	case CrewKindModerator:
		return "chora-oe-moderator"
	}
	return ""
}
