package agent

import "strconv"

// conditions.go — ADR-197 M-A condition extractors for the OE-grading crew.
//
// They surface the prompt-shaping discriminants (mode, attempt, subject,
// prior-feedback presence) — NOT learner PII — so O+ can render "which
// conditions produced this grading decision". Pure, best-effort, and reuse the
// Build*TaskContextFromState builders so they can't drift from the composed
// prompt. The parameter is the crew-local stateGetter; session.ReadonlyState
// satisfies it, so a main wraps these into a promptstamping.ConditionExtractor.

// EvaluatorConditions extracts the oe_evaluator prompt discriminants. The
// evaluator binary serves two modes (evaluate / assess_summary); mode is the
// primary discriminant.
func EvaluatorConditions(state stateGetter) map[string]string {
	mode, err := EvaluatorMode(state)
	if err != nil {
		return nil
	}
	c := map[string]string{"mode": string(mode)}
	switch mode {
	case ModeEvaluate:
		tc, err := BuildEvaluatorTaskContextFromState(state)
		if err != nil {
			return c
		}
		c["attempt_index"] = strconv.Itoa(tc.AttemptIndex)
		if tc.Subject != "" {
			c["subject"] = tc.Subject
		}
		if tc.PriorModeratorFeedback != "" {
			c["has_prior_moderator_feedback"] = "true"
		}
	case ModeAssessSummary:
		sc, err := BuildSummaryTaskContextFromState(state)
		if err != nil {
			return c
		}
		if sc.Subject != "" {
			c["subject"] = sc.Subject
		}
	}
	return c
}

// ModeratorConditions extracts the oe_moderator prompt discriminants.
func ModeratorConditions(state stateGetter) map[string]string {
	tc, err := BuildModeratorTaskContextFromState(state)
	if err != nil {
		return nil
	}
	c := map[string]string{"attempt_index": strconv.Itoa(tc.AttemptIndex)}
	if tc.Subject != "" {
		c["subject"] = tc.Subject
	}
	return c
}
