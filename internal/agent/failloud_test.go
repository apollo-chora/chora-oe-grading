package agent

// Fail-loud guard tests for the per-turn context builders + the degradation
// contract of the ADR-197 M-A condition extractors.
//
// Every guard asserted here exists because of the qgen "critic never saw the
// candidate" bug (project_qgen_critic_never_saw_candidate): an agent handed no
// candidate must ABORT the invocation. The failure mode these guards prevent is
// not a crash, it is worse: the LLM is called with an instruction that contains
// no learner answer and no rubric, produces a confident-looking grade of
// nothing, and the orchestrator records it against a real submission. So each
// test asserts (1) an error surfaces, (2) it names the state key an operator
// would grep for, and (3) no partially built context is handed back.
//
// mapState (the stateGetter double) is defined in conditions_test.go.

import (
	"reflect"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Nil state: the InstructionProvider was wired without a session
// -----------------------------------------------------------------------------

func TestEvaluatorMode_NilStateFailsLoud(t *testing.T) {
	mode, err := EvaluatorMode(nil)
	if err == nil {
		t.Fatalf("a nil state must fail loud; got mode %q", mode)
	}
	if !strings.Contains(err.Error(), "state must not be nil") {
		t.Errorf("error must name the nil state: got %q", err)
	}
	if mode != "" {
		t.Errorf("no mode may be inferred from a nil state; got %q", mode)
	}
}

func TestBuildTaskContextsFromState_NilStateFailLoud(t *testing.T) {
	t.Run("evaluate", func(t *testing.T) {
		ctx, err := BuildEvaluatorTaskContextFromState(nil)
		if err == nil {
			t.Fatalf("a nil state must fail loud; got ctx %+v", ctx)
		}
		if !strings.Contains(err.Error(), "oe_evaluator") || !strings.Contains(err.Error(), "state must not be nil") {
			t.Errorf("error must name the agent and the nil state: got %q", err)
		}
		if !reflect.DeepEqual(ctx, EvaluatorTaskContext{}) {
			t.Errorf("a failed build must return the zero context, not a partial one: %+v", ctx)
		}
	})

	t.Run("assess_summary", func(t *testing.T) {
		ctx, err := BuildSummaryTaskContextFromState(nil)
		if err == nil {
			t.Fatalf("a nil state must fail loud; got ctx %+v", ctx)
		}
		if !strings.Contains(err.Error(), "summary") || !strings.Contains(err.Error(), "state must not be nil") {
			t.Errorf("error must name the summary mode and the nil state: got %q", err)
		}
		if !reflect.DeepEqual(ctx, SummaryTaskContext{}) {
			t.Errorf("a failed build must return the zero context, not a partial one: %+v", ctx)
		}
	})

	t.Run("moderate", func(t *testing.T) {
		ctx, err := BuildModeratorTaskContextFromState(nil)
		if err == nil {
			t.Fatalf("a nil state must fail loud; got ctx %+v", ctx)
		}
		if !strings.Contains(err.Error(), "oe_moderator") || !strings.Contains(err.Error(), "state must not be nil") {
			t.Errorf("error must name the agent and the nil state: got %q", err)
		}
		if !reflect.DeepEqual(ctx, ModeratorTaskContext{}) {
			t.Errorf("a failed build must return the zero context, not a partial one: %+v", ctx)
		}
	})
}

// -----------------------------------------------------------------------------
// Missing grading inputs: absent, blank and whitespace-only are all "missing"
// -----------------------------------------------------------------------------

// blankForms are the three ways the orchestrator can fail to stamp a required
// string: never set it, set it empty, or set it to whitespace the JSON path
// preserved. All three must be treated as missing (the builders TrimSpace).
var blankForms = []struct {
	name string
	set  bool
	val  string
}{
	{"absent", false, ""},
	{"empty", true, ""},
	{"whitespace only", true, " \n\t "},
}

func TestBuildEvaluatorTaskContextFromState_MissingLearnerAnswerFailsLoud(t *testing.T) {
	for _, f := range blankForms {
		t.Run(f.name, func(t *testing.T) {
			m := map[string]any{"rubric_json": `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`}
			if f.set {
				m["learner_answer"] = f.val
			}
			ctx, err := BuildEvaluatorTaskContextFromState(&mapState{m: m})
			if err == nil {
				t.Fatalf("a missing learner answer must abort the invocation; got ctx %+v", ctx)
			}
			if !strings.Contains(err.Error(), "learner_answer empty") {
				t.Errorf("error must name the learner_answer state key: got %q", err)
			}
			if !reflect.DeepEqual(ctx, EvaluatorTaskContext{}) {
				t.Errorf("a failed build must return the zero context, not a partial one: %+v", ctx)
			}
		})
	}
}

func TestBuildEvaluatorTaskContextFromState_MissingRubricFailsLoud(t *testing.T) {
	for _, f := range blankForms {
		t.Run(f.name, func(t *testing.T) {
			m := map[string]any{"learner_answer": "Mitochondria release energy as ATP."}
			if f.set {
				m["rubric_json"] = f.val
			}
			ctx, err := BuildEvaluatorTaskContextFromState(&mapState{m: m})
			if err == nil {
				t.Fatalf("a missing rubric must abort the invocation; got ctx %+v", ctx)
			}
			if !strings.Contains(err.Error(), "rubric_json empty") {
				t.Errorf("error must name the rubric_json state key: got %q", err)
			}
			// ADR-172 §D4 is why the rubric is a precondition and not a
			// nice-to-have, so the error cites it for the operator.
			if !strings.Contains(err.Error(), "ADR-172") {
				t.Errorf("error must cite the ADR making a weighted rubric a grading precondition: got %q", err)
			}
		})
	}
}

func TestBuildSummaryTaskContextFromState_MissingDigestFailsLoud(t *testing.T) {
	for _, f := range blankForms {
		t.Run(f.name, func(t *testing.T) {
			m := map[string]any{"subject": "biology", "assessment_id": "asmt-1"}
			if f.set {
				m["results_digest"] = f.val
			}
			ctx, err := BuildSummaryTaskContextFromState(&mapState{m: m})
			if err == nil {
				t.Fatalf("a missing results digest must abort the invocation; got ctx %+v", ctx)
			}
			if !strings.Contains(err.Error(), "results_digest") {
				t.Errorf("error must name the results_digest state key: got %q", err)
			}
			if !reflect.DeepEqual(ctx, SummaryTaskContext{}) {
				t.Errorf("a failed build must return the zero context, not a partial one: %+v", ctx)
			}
		})
	}
}

func TestBuildModeratorTaskContextFromState_MissingLearnerAnswerFailsLoud(t *testing.T) {
	for _, f := range blankForms {
		t.Run(f.name, func(t *testing.T) {
			m := map[string]any{
				"evaluation_json": `{"points_earned":8,"points_possible":10,"comment":"ok"}`,
				"rubric_json":     `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
			}
			if f.set {
				m["learner_answer"] = f.val
			}
			ctx, err := BuildModeratorTaskContextFromState(&mapState{m: m})
			if err == nil {
				t.Fatalf("a missing learner answer must abort moderation; got ctx %+v", ctx)
			}
			if !strings.Contains(err.Error(), "learner_answer empty") {
				t.Errorf("error must name the learner_answer state key: got %q", err)
			}
			// The moderator's whole hallucination check is "is this feedback
			// grounded in what the learner wrote", so without the answer it
			// cannot do its job at all.
			if !strings.Contains(err.Error(), "hallucination") {
				t.Errorf("error must say why the moderator needs the answer: got %q", err)
			}
		})
	}
}

func TestBuildModeratorTaskContextFromState_MissingRubricFailsLoud(t *testing.T) {
	for _, f := range blankForms {
		t.Run(f.name, func(t *testing.T) {
			m := map[string]any{
				"evaluation_json": `{"points_earned":8,"points_possible":10,"comment":"ok"}`,
				"learner_answer":  "Mitochondria release energy as ATP.",
			}
			if f.set {
				m["rubric_json"] = f.val
			}
			ctx, err := BuildModeratorTaskContextFromState(&mapState{m: m})
			if err == nil {
				t.Fatalf("a missing rubric must abort moderation; got ctx %+v", ctx)
			}
			if !strings.Contains(err.Error(), "rubric_json empty") {
				t.Errorf("error must name the rubric_json state key: got %q", err)
			}
			if !reflect.DeepEqual(ctx, ModeratorTaskContext{}) {
				t.Errorf("a failed build must return the zero context, not a partial one: %+v", ctx)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Condition extractors degrade to the mode alone, never to a fabricated value
// -----------------------------------------------------------------------------

// The extractors are best-effort by design (they feed the O+ "which conditions
// produced this decision" panel, they do not gate the run). When the builder
// fails, the honest answer is "the mode, and nothing else": emitting a default
// attempt_index of 0 would put a fabricated discriminant into the audit trail.

func TestEvaluatorConditions_EvaluateBuildFailureYieldsModeOnly(t *testing.T) {
	// mode absent, so evaluate mode is inferred, but the learner answer the
	// builder requires was never stamped.
	st := &mapState{m: map[string]any{"subject": "biology", "attempt_index": float64(2)}}
	c := EvaluatorConditions(st)
	if len(c) != 1 || c["mode"] != "evaluate" {
		t.Fatalf("a failed evaluate build must yield the mode alone; got %v", c)
	}
	if _, ok := c["attempt_index"]; ok {
		t.Error("attempt_index must not be reported when the context build failed")
	}
	if _, ok := c["subject"]; ok {
		t.Error("subject must not be reported when the context build failed")
	}
}

func TestEvaluatorConditions_SummaryBuildFailureYieldsModeOnly(t *testing.T) {
	// assess_summary mode with no results_digest: the digest is the summary
	// builder's required input.
	st := &mapState{m: map[string]any{"mode": "assess_summary", "subject": "science"}}
	c := EvaluatorConditions(st)
	if len(c) != 1 || c["mode"] != "assess_summary" {
		t.Fatalf("a failed summary build must yield the mode alone; got %v", c)
	}
	if _, ok := c["subject"]; ok {
		t.Error("subject must not be reported when the context build failed")
	}
}
