// Package agent holds the OE-grading crew's two-role prompt composers + the
// rubric→composite scoring math + the per-turn mode-branch InstructionProvider
// + the moderator accept/reject parser, per ADR-172 §D2/§D4/§D5.
//
// Two ADK-Go agents back the orchestrator's per-OE-answer quality loop
// (ADR-172 §D3 — the hosted agent runtime is DECOMMISSIONED; both run as
// plain container Deployments dispatched over the NATS event lane by the
// orchestrator):
//
//   - oe_evaluator (cmd/oe_evaluator) — grades ONE OE answer against a weighted
//     rubric + model_answer → per-criterion sub-scores, composite points_earned,
//     an ALWAYS-present per-question comment. ALSO runs assess_summary mode
//     (per-turn instruction mode, NO moderator loop) for the holistic
//     whole-assessment narrative.
//   - oe_moderator (cmd/oe_moderator) — a single tool-free JUDGE of the
//     evaluator's score+rationale against the rubric → accept | reject+feedback
//     (rubric fidelity, hallucination, internal consistency). Does NOT rewrite
//     scores.
//
// Mirrors the qgen 2-agent loop (ADR-153 question→critic): the LangGraph
// orchestrator owns the loop (≤2 iterations default), the moderator is the
// judge, the evaluator is the grader. Composers are PURE functions (same input
// → same output) per ADR-141 D2 transparency.
//
// The per-turn InstructionProvider injects the per-request grading context from
// ADK session state (learner_answer / rubric / model_answer for evaluate mode;
// the all-answers summary input for assess_summary mode) — NOT a static system
// prompt. This is the qgen "critic never saw the candidate" lesson
// (project_qgen_critic_never_saw_candidate): a deferred/empty InstructionProvider
// makes the agent grade nothing.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// -----------------------------------------------------------------------------
// Mode — evaluator-vs-summary branch (ADR-172 §D5)
// -----------------------------------------------------------------------------

// Mode is the per-turn evaluator mode, read from session-state key "mode".
type Mode string

const (
	// ModeEvaluate — grade ONE OE answer against its rubric + model_answer.
	// The default when the "mode" state key is absent (the dominant per-answer
	// path; the orchestrator always stamps it explicitly on a real run).
	ModeEvaluate Mode = "evaluate"

	// ModeAssessSummary — produce the holistic whole-assessment narrative over
	// ALL answers (MCQ + OE). No rubric, no per-criterion scoring, NO moderator
	// loop (ADR-172 §D5).
	ModeAssessSummary Mode = "assess_summary"
)

// assertValidMode refuses anything outside the 2-value enum. Fail-loud — the
// qgen "critic never saw the candidate" lesson: silently coercing an unknown
// mode would make the agent grade the wrong thing.
func assertValidMode(m Mode) error {
	switch m {
	case ModeEvaluate, ModeAssessSummary:
		return nil
	}
	return fmt.Errorf("oe_evaluator: invalid mode %q (want %q or %q)", m, ModeEvaluate, ModeAssessSummary)
}

// -----------------------------------------------------------------------------
// stateGetter — the minimal session-state read surface
// -----------------------------------------------------------------------------

// stateGetter is the minimum subset of session.ReadonlyState the per-turn
// InstructionProvider builders need. Mirrors the pattern in qgen's
// composer_question.go so the builders are pure-testable without the full ADK
// session interface.
type stateGetter interface {
	Get(string) (any, error)
}

// readStateString reads a string-valued state key, returning "" on miss / type
// mismatch — same defensive posture as manaplugin.getStateString + qgen.
func readStateString(state stateGetter, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// readStateInt reads an integer-valued state key, tolerating the float64 the
// executor's JSON path hands us. Returns 0 on miss.
func readStateInt(state stateGetter, key string) int {
	v, err := state.Get(key)
	if err != nil {
		return 0
	}
	return int(asFloat(v))
}

// asFloat extracts a float64 from a numeric `any`. The Python executor hands us
// float64 via the ReasoningEngine JSON path; int/int64 accepted for native Go
// callers in tests.
func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// safe returns fallback when s is blank, else s.
func safe(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// -----------------------------------------------------------------------------
// ADR-197 M-B — operator prompt-override seam (mirrors qgen composer_question.go)
// -----------------------------------------------------------------------------
//
// The pinned contract: the orchestrator stamps a session-state key
// `prompt_overrides_json` = a JSON string of map[string]string keyed by
// segment_id. The OE-grading SAFE segments are "role", "task", "examples". The
// [EXPECTED OUTPUT] JSON-contract block, the per-call content blocks (rubric /
// learner-answer / model-answer / evaluator-output / results-digest), and any
// safety preamble are NEVER routed through an override. Versioning is owned by
// the shared promptstamping primitive (resolved_prompt_version) — NOT touched here.

// readPromptOverridesFromState decodes the operator SAFE-block overrides the
// orchestrator threads into session state under prompt_overrides_json. Two wire
// forms are tolerated: a JSON-object STRING (json.dumps of a map[string]string),
// OR a native map (map[string]string / map[string]any) from the ReasoningEngine
// JSON round-trip. Both normalise to map[string]string. Absent / empty /
// malformed ⇒ nil — fail-soft: a broken override map MUST degrade to the embedded
// blocks (byte-identical default), never panic and never partially apply.
func readPromptOverridesFromState(state stateGetter, key string) map[string]string {
	raw, err := state.Get(key)
	if err != nil {
		return nil
	}
	var jsonBytes []byte
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		jsonBytes = []byte(v)
	case map[string]string:
		if len(v) == 0 {
			return nil
		}
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		jsonBytes = b
	}
	var out map[string]string
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		return nil // malformed → embedded blocks (fail-soft)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// overrideOr returns the operator override for segmentID when present and
// non-empty, else the embedded block. ADR-197 M-B SAFE-block seam: applied ONLY
// at the role / task / examples assembly sites — NEVER to the [EXPECTED OUTPUT]
// JSON-contract block, a content block, or a safety preamble. A nil / absent
// override map (the dominant path) always returns embedded ⇒ byte-identical output.
func overrideOr(overrides map[string]string, segmentID, embedded string) string {
	if overrides != nil {
		if v, ok := overrides[segmentID]; ok && v != "" {
			return v
		}
	}
	return embedded
}
