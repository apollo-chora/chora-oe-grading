package agent

// Composer edge-branch tests: the re-grade block, the loop-budget default and
// the unset-provenance placeholders.
//
// These are the parts of the composed prompt that only appear on a non-default
// call, so the goldens in override_test.go (captured from a fully populated,
// first-attempt context) cannot see them. Each one changes what the model is
// told to do, so each is asserted on the composed text rather than on a flag.

import (
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// [PRIOR MODERATOR FEEDBACK]: the re-grade instruction
// -----------------------------------------------------------------------------

// On attempt 2 the evaluator must be told it is RE-GRADING and be handed the
// moderator's rejection verbatim. Without this block the second pass is
// indistinguishable from the first, so the loop burns its budget re-emitting
// the grade the moderator already rejected: the qgen loop-to-max-iterations
// failure mode.
func TestComposeEvaluate_PriorModeratorFeedback_EmitsRegradeBlock(t *testing.T) {
	const feedback = "criterion c2 feedback cites ATP which the learner never wrote"
	ctx := goldenEvaluatorCtx()
	ctx.AttemptIndex = 1
	ctx.PriorModeratorFeedback = feedback
	got := ComposeEvaluateInstruction(ctx)

	if !strings.Contains(got, "## [PRIOR MODERATOR FEEDBACK]\n") {
		t.Fatalf("re-grade context missing the [PRIOR MODERATOR FEEDBACK] block:\n%s", got)
	}
	if !strings.Contains(got, "You are RE-GRADING.") {
		t.Error("the block must tell the evaluator it is re-grading, not grading afresh")
	}
	if !strings.Contains(got, feedback) {
		t.Error("the moderator's feedback must be carried verbatim so the evaluator can address each point")
	}
	// The block belongs with the content blocks, ahead of the output contract.
	if strings.Index(got, "## [PRIOR MODERATOR FEEDBACK]") > strings.Index(got, "## [EXPECTED OUTPUT]") {
		t.Error("[PRIOR MODERATOR FEEDBACK] must precede the [EXPECTED OUTPUT] contract block")
	}
	// The attempt counter must move with it, or the prompt claims attempt 1
	// while carrying attempt-1 feedback.
	if !strings.Contains(got, "attempt=2/3.") {
		t.Errorf("attempt counter must report the re-grade attempt; got:\n%s", got)
	}
}

// First attempt (and a feedback string that is only whitespace) must produce NO
// re-grade block: telling a first-pass evaluator it is "re-grading" against an
// empty critique would invent a correction it was never asked for.
func TestComposeEvaluate_NoPriorFeedback_OmitsRegradeBlock(t *testing.T) {
	cases := []struct {
		name     string
		feedback string
	}{
		{"first attempt", ""},
		{"whitespace only", " \n\t "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := goldenEvaluatorCtx()
			ctx.PriorModeratorFeedback = c.feedback
			got := ComposeEvaluateInstruction(ctx)
			// The header, not the bare phrase: the embedded TASK block
			// legitimately names [PRIOR MODERATOR FEEDBACK] in its step 5
			// ("if a moderator block is present"), so only the emitted block
			// header distinguishes a re-grade prompt from a first pass.
			if strings.Contains(got, "## [PRIOR MODERATOR FEEDBACK]") {
				t.Errorf("no re-grade block may appear for %s:\n%s", c.name, got)
			}
			if strings.Contains(got, "You are RE-GRADING.") {
				t.Errorf("no re-grade instruction may appear for %s", c.name)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Loop budget: an unstamped max_iterations falls back to the ADR-172 default
// -----------------------------------------------------------------------------

// The attempt counter is transparency surface: it tells the model how much of
// the quality loop is left. An unstamped max_iterations must read as the
// documented default of 2 (so "attempt=1/3"), never as a budget of zero, which
// would read to the model as "this is your only and final pass".
func TestComposeInstructions_UnstampedMaxIterationsDefaultsToTwo(t *testing.T) {
	t.Run("evaluate", func(t *testing.T) {
		ctx := goldenEvaluatorCtx()
		ctx.MaxIterations = 0
		if got := ComposeEvaluateInstruction(ctx); !strings.Contains(got, "attempt=1/3.") {
			t.Errorf("unstamped max_iterations must render as the default budget (attempt=1/3); got:\n%s", got)
		}
	})

	t.Run("moderate", func(t *testing.T) {
		ctx := goldenModeratorCtx()
		ctx.MaxIterations = 0
		if got := ComposeModeratorInstruction(ctx); !strings.Contains(got, "attempt=1/3.") {
			t.Errorf("unstamped max_iterations must render as the default budget (attempt=1/3); got:\n%s", got)
		}
	})

	// An explicitly stamped budget is honoured, not overwritten by the default.
	t.Run("explicit budget honoured", func(t *testing.T) {
		ctx := goldenEvaluatorCtx()
		ctx.AttemptIndex, ctx.MaxIterations = 1, 5
		if got := ComposeEvaluateInstruction(ctx); !strings.Contains(got, "attempt=2/6.") {
			t.Errorf("an explicit max_iterations must be honoured; got:\n%s", got)
		}
	})
}

// -----------------------------------------------------------------------------
// Unset provenance: explicit placeholders, never a blank
// -----------------------------------------------------------------------------

// A missing correlation id must render as a visible <unset> marker. Rendering
// it blank would produce "tenant_id=, learner_gcid=," which reads to the model
// as an empty value and to a human auditor as a truncated prompt, and it is the
// D1 accountability trail that is being written.
func TestComposeEvaluate_UnsetProvenanceRendersPlaceholders(t *testing.T) {
	ctx := EvaluatorTaskContext{
		LearnerAnswer: "Mitochondria release energy as ATP.",
		RubricJSON:    `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
	}
	got := ComposeEvaluateInstruction(ctx)

	const wantCallContext = "Call context: tenant_id=<unset>, learner_gcid=<unset>, submission_id=<unset>, " +
		"test_set_question_id=<unset>, question_id=<unset>, points_possible=0, attempt=1/3."
	if !strings.Contains(got, wantCallContext) {
		t.Errorf("unset provenance must render explicit <unset> markers\nwant line: %s\n got:\n%s", wantCallContext, got)
	}
	if !strings.Contains(got, "## [QUESTION PROMPT]\n<unset>\n") {
		t.Error("an unset question prompt must render as <unset>, not as an empty block")
	}
	if !strings.Contains(got, "<no model answer supplied>") {
		t.Error("an absent model answer must say so, so the evaluator does not grade against silence")
	}
	// With neither subject nor topic set, the hint line is omitted entirely
	// rather than emitted empty.
	if strings.Contains(got, "Subject/topic:") {
		t.Errorf("no subject/topic hint line may be emitted when neither is set:\n%s", got)
	}
}

func TestComposeModerator_UnsetProvenanceRendersPlaceholders(t *testing.T) {
	ctx := ModeratorTaskContext{
		LearnerAnswer:  "Mitochondria release energy as ATP.",
		RubricJSON:     `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
		EvaluationJSON: `{"points_earned":8,"points_possible":10,"comment":"ok"}`,
	}
	got := ComposeModeratorInstruction(ctx)

	const wantCallContext = "Call context: tenant_id=<unset>, learner_gcid=<unset>, submission_id=<unset>, " +
		"test_set_question_id=<unset>, question_id=<unset>, attempt=1/3."
	if !strings.Contains(got, wantCallContext) {
		t.Errorf("unset provenance must render explicit <unset> markers\nwant line: %s\n got:\n%s", wantCallContext, got)
	}
	if !strings.Contains(got, "<no model answer supplied>") {
		t.Error("an absent model answer must say so, so the moderator does not judge against silence")
	}
}
