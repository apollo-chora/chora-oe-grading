package agent

// Session-state read-helper tests: numeric wire-form tolerance and the
// fail-soft posture of the ADR-197 M-B override reader.
//
// The same state map reaches these helpers over two different wires: the
// ReasoningEngine JSON path, which turns every number into a float64, and
// native Go callers, which hand int / int64. A helper that only understood one
// of them would silently read every attempt counter as 0.

import (
	"strings"
	"testing"
)

func TestReadStateInt_NumericWireForms(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		{"float64 (the ReasoningEngine JSON path)", float64(3), 3},
		{"float32", float32(4), 4},
		{"int (native Go caller)", 5, 5},
		{"int64", int64(6), 6},
		{"fractional float truncates toward zero", float64(2.9), 2},
		{"numeric string is NOT parsed", "7", 0},
		{"non-numeric type degrades to zero", true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &mapState{m: map[string]any{"attempt_index": c.val}}
			if got := readStateInt(st, "attempt_index"); got != c.want {
				t.Errorf("readStateInt(%#v): got %d want %d", c.val, got, c.want)
			}
		})
	}

	// An absent key is a zero, not a panic: attempt_index is optional on the
	// first pass.
	if got := readStateInt(&mapState{m: map[string]any{}}, "attempt_index"); got != 0 {
		t.Errorf("absent key must read as 0; got %d", got)
	}
}

// The wire-form tolerance has to survive the public builder too, not just the
// helper: attempt_index is what selects the re-grade framing in the composed
// prompt.
func TestBuildEvaluatorTaskContextFromState_AttemptIndexAcceptsNativeInt(t *testing.T) {
	st := &mapState{m: map[string]any{
		"learner_answer": "Mitochondria release energy as ATP.",
		"rubric_json":    `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
		"attempt_index":  2,
		"max_iterations": int64(4),
	}}
	ctx, err := BuildEvaluatorTaskContextFromState(st)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if ctx.AttemptIndex != 2 {
		t.Errorf("AttemptIndex: got %d want 2", ctx.AttemptIndex)
	}
	if ctx.MaxIterations != 4 {
		t.Errorf("MaxIterations: got %d want 4", ctx.MaxIterations)
	}
}

// -----------------------------------------------------------------------------
// readPromptOverridesFromState: fail-soft on anything it cannot use
// -----------------------------------------------------------------------------

// An operator override map that cannot be read must degrade to the embedded
// prompt blocks, never to a partial or panicking one: a grading run must not
// fail because someone typed a bad override in O+.

func TestReadPromptOverridesFromState_EmptyNativeMapIsNil(t *testing.T) {
	st := &mapState{m: map[string]any{"prompt_overrides_json": map[string]string{}}}
	if got := readPromptOverridesFromState(st, "prompt_overrides_json"); got != nil {
		t.Errorf("an empty native override map must read as nil (embedded blocks); got %v", got)
	}
}

func TestReadPromptOverridesFromState_UnserialisableValueIsNil(t *testing.T) {
	// A value no JSON encoder can represent is the hardest form of "unusable":
	// it must return nil rather than panic on the marshal error.
	st := &mapState{m: map[string]any{"prompt_overrides_json": make(chan int)}}
	got := readPromptOverridesFromState(st, "prompt_overrides_json")
	if got != nil {
		t.Errorf("an unserialisable override value must read as nil (embedded blocks); got %v", got)
	}
}

// The composed prompt is the actual contract: an unusable override must leave
// the embedded SAFE blocks byte-identical to the no-override golden.
func TestComposeEvaluate_UnusableOverrideFallsBackToEmbedded(t *testing.T) {
	st := &mapState{m: map[string]any{
		"learner_answer":        "The mitochondria is the powerhouse of the cell.",
		"rubric_json":           `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
		"prompt_overrides_json": make(chan int),
	}}
	ctx, err := BuildEvaluatorTaskContextFromState(st)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if ctx.Overrides != nil {
		t.Fatalf("an unusable override map must not reach the composer; got %v", ctx.Overrides)
	}
	if got := ComposeEvaluateInstruction(ctx); !strings.Contains(got, "Grade the learner's free-text answer") {
		t.Errorf("the embedded task block must survive an unusable override:\n%s", got)
	}
}
