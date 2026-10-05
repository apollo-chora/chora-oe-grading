package agent

// ADR-197 M-B.2 — oe_grading composer override-readiness tests.
//
// Two guarantees, mirroring the shipped qgen golden test:
//
//  1. BEHAVIOUR-NEUTRAL: with no overrides in state (Overrides nil), every
//     composed prompt is BYTE-IDENTICAL to the pre-override output captured in
//     testdata/*.golden. A nil/absent override map MUST never change the prompt.
//  2. OVERRIDE SEAM: an operator override for "role" / "task" / "examples"
//     substitutes ONLY that SAFE block; the [EXPECTED OUTPUT] JSON-contract
//     block stays byte-identical (never overridable).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonical contexts — MUST match the fixtures used to capture the goldens.
func goldenEvaluatorCtx() EvaluatorTaskContext {
	return EvaluatorTaskContext{
		TenantID: "tnt-1", LearnerGCID: "gcid-1",
		SubmissionID: "sub-1", TestSetQuestionID: "tsq-1", QuestionID: "q-1",
		PointsPossible: 10, Subject: "biology", Topic: "cells",
		Prompt:        "Explain the role of mitochondria.",
		LearnerAnswer: "The mitochondria is the powerhouse of the cell.",
		RubricJSON:    `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
		ModelAnswer:   "Mitochondria produce ATP via aerobic respiration.",
		AttemptIndex:  0, MaxIterations: 2,
	}
}

func goldenSummaryCtx() SummaryTaskContext {
	return SummaryTaskContext{
		TenantID: "tnt-1", LearnerGCID: "gcid-1",
		SubmissionID: "sub-1", AssessmentID: "asmt-1",
		Subject: "science", PassingThresholdPercent: 50,
		ResultsDigest: "q1 MCQ: correct; q2 OE: partial",
	}
}

func goldenModeratorCtx() ModeratorTaskContext {
	return ModeratorTaskContext{
		TenantID: "tnt-1", LearnerGCID: "gcid-1",
		SubmissionID: "sub-1", TestSetQuestionID: "tsq-1", QuestionID: "q-1",
		Subject: "biology", Topic: "cells",
		Prompt:         "Explain the role of mitochondria.",
		LearnerAnswer:  "The mitochondria is the powerhouse of the cell.",
		RubricJSON:     `{"criteria":[{"criterion_id":"c1","weight":1.0}]}`,
		ModelAnswer:    "Mitochondria produce ATP via aerobic respiration.",
		EvaluationJSON: `{"points_earned":8,"points_possible":10,"criterion_scores":[],"comment":"Good grasp of ATP."}`,
		AttemptIndex:   0, MaxIterations: 2,
	}
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(b)
}

// expectedOutputBlock returns the [EXPECTED OUTPUT] section verbatim (from the
// header to end-of-prompt) — the contract block that must never change.
func expectedOutputBlock(t *testing.T, prompt string) string {
	t.Helper()
	const hdr = "## [EXPECTED OUTPUT]\n"
	i := strings.Index(prompt, hdr)
	if i < 0 {
		t.Fatalf("prompt has no [EXPECTED OUTPUT] block:\n%s", prompt)
	}
	return prompt[i:]
}

// -----------------------------------------------------------------------------
// 1. Behaviour-neutral golden tests (Overrides nil → byte-identical)
// -----------------------------------------------------------------------------

func TestComposeEvaluate_NilOverrides_ByteIdenticalGolden(t *testing.T) {
	got := ComposeEvaluateInstruction(goldenEvaluatorCtx())
	if want := readGolden(t, "evaluate.golden"); got != want {
		t.Errorf("evaluate prompt drifted from golden (nil overrides must be byte-identical)\n--- got ---\n%s", got)
	}
}

func TestComposeSummary_NilOverrides_ByteIdenticalGolden(t *testing.T) {
	got := ComposeSummaryInstruction(goldenSummaryCtx())
	if want := readGolden(t, "summary.golden"); got != want {
		t.Errorf("summary prompt drifted from golden (nil overrides must be byte-identical)\n--- got ---\n%s", got)
	}
}

func TestComposeModerator_NilOverrides_ByteIdenticalGolden(t *testing.T) {
	got := ComposeModeratorInstruction(goldenModeratorCtx())
	if want := readGolden(t, "moderator.golden"); got != want {
		t.Errorf("moderator prompt drifted from golden (nil overrides must be byte-identical)\n--- got ---\n%s", got)
	}
}

// An EMPTY (non-nil) overrides map and empty per-segment values are also
// byte-identical — overrideOr falls back to embedded on "" (arch-clean).
func TestComposeEvaluate_EmptyOverrides_ByteIdenticalGolden(t *testing.T) {
	ctx := goldenEvaluatorCtx()
	ctx.Overrides = map[string]string{"role": "", "task": "", "examples": ""}
	got := ComposeEvaluateInstruction(ctx)
	if want := readGolden(t, "evaluate.golden"); got != want {
		t.Errorf("empty-value overrides must fall back to embedded (byte-identical)\n--- got ---\n%s", got)
	}
}

// -----------------------------------------------------------------------------
// 2. Override seam — SAFE blocks substitute, [EXPECTED OUTPUT] stays identical
// -----------------------------------------------------------------------------

const (
	sentRole = "OPERATOR-OVERRIDE-ROLE-SENTINEL"
	sentTask = "OPERATOR-OVERRIDE-TASK-SENTINEL"
	sentEx   = "OPERATOR-OVERRIDE-EXAMPLES-SENTINEL"
)

func TestComposeEvaluate_Overrides_SubstituteSafeBlocksOnly(t *testing.T) {
	golden := readGolden(t, "evaluate.golden")
	ctx := goldenEvaluatorCtx()
	ctx.Overrides = map[string]string{"role": sentRole, "task": sentTask, "examples": sentEx}
	got := ComposeEvaluateInstruction(ctx)

	for _, s := range []string{sentRole, sentTask, sentEx} {
		if !strings.Contains(got, s) {
			t.Errorf("override sentinel %q missing from composed prompt", s)
		}
	}
	// Embedded SAFE bodies replaced.
	if strings.Contains(got, "You are the Evaluator agent of the 2-agent OE-grading crew") {
		t.Error("embedded role survived a role override")
	}
	if strings.Contains(got, "Grade the learner's free-text answer") {
		t.Error("embedded task survived a task override")
	}
	// The [EXPECTED OUTPUT] contract block is NOT overridable.
	if got, want := expectedOutputBlock(t, got), expectedOutputBlock(t, golden); got != want {
		t.Errorf("[EXPECTED OUTPUT] changed under SAFE-block overrides\n got:\n%s\nwant:\n%s", got, want)
	}
	// Content blocks (rubric / learner answer) are NOT overridable.
	if !strings.Contains(got, ctx.LearnerAnswer) || !strings.Contains(got, ctx.RubricJSON) {
		t.Error("learner-answer / rubric content blocks must remain present under overrides")
	}
}

// CHO-2368 P2 mode split: the oe_evaluator's grade and summary modes share ONE
// override map (same agent, same session state), but their embedded bodies are
// DIFFERENT texts — a single generic `role` override would misdescribe one
// mode. Summary mode therefore reads its OWN `summary_role` / `summary_task` /
// `summary_examples` keys (matching the P1 catalogue segment vocabulary), and
// each mode's keys are inert in the other mode.

func TestComposeSummary_SummaryKeys_SubstituteSafeBlocksOnly(t *testing.T) {
	golden := readGolden(t, "summary.golden")
	ctx := goldenSummaryCtx()
	ctx.Overrides = map[string]string{
		"summary_role":     sentRole,
		"summary_task":     sentTask,
		"summary_examples": sentEx,
	}
	got := ComposeSummaryInstruction(ctx)

	for _, s := range []string{sentRole, sentTask, sentEx} {
		if !strings.Contains(got, s) {
			t.Errorf("override sentinel %q missing from composed prompt", s)
		}
	}
	if strings.Contains(got, "You are the Evaluator agent running in assess_summary mode") {
		t.Error("embedded summary role survived a summary_role override")
	}
	if strings.Contains(got, "Write the overall whole-assessment comment") {
		t.Error("embedded summary task survived a summary_task override")
	}
	if got, want := expectedOutputBlock(t, got), expectedOutputBlock(t, golden); got != want {
		t.Errorf("[EXPECTED OUTPUT] changed under SAFE-block overrides\n got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(got, ctx.ResultsDigest) {
		t.Error("results-digest content block must remain present under overrides")
	}
}

func TestComposeSummary_GenericGradeKeys_AreInert(t *testing.T) {
	// A grade-mode override (role/task/examples) must NOT leak into the
	// summary narration — byte-identical to the no-override golden.
	ctx := goldenSummaryCtx()
	ctx.Overrides = map[string]string{"role": sentRole, "task": sentTask, "examples": sentEx}
	got := ComposeSummaryInstruction(ctx)
	if want := readGolden(t, "summary.golden"); got != want {
		t.Errorf("generic grade keys leaked into summary mode\n--- got ---\n%s", got)
	}
}

func TestComposeEvaluate_SummaryKeys_AreInert(t *testing.T) {
	// Symmetric isolation: summary_* keys must NOT touch the grade prompt.
	ctx := goldenEvaluatorCtx()
	ctx.Overrides = map[string]string{
		"summary_role":     sentRole,
		"summary_task":     sentTask,
		"summary_examples": sentEx,
	}
	got := ComposeEvaluateInstruction(ctx)
	if want := readGolden(t, "evaluate.golden"); got != want {
		t.Errorf("summary_* keys leaked into grade mode\n--- got ---\n%s", got)
	}
}

func TestComposeModerator_Overrides_SubstituteSafeBlocksOnly(t *testing.T) {
	golden := readGolden(t, "moderator.golden")
	ctx := goldenModeratorCtx()
	ctx.Overrides = map[string]string{"role": sentRole, "task": sentTask, "examples": sentEx}
	got := ComposeModeratorInstruction(ctx)

	for _, s := range []string{sentRole, sentTask, sentEx} {
		if !strings.Contains(got, s) {
			t.Errorf("override sentinel %q missing from composed prompt", s)
		}
	}
	if strings.Contains(got, "You are the Moderator agent of the 2-agent OE-grading crew") {
		t.Error("embedded moderator role survived a role override")
	}
	if strings.Contains(got, "Judge the Evaluator's grading on exactly THREE axes") {
		t.Error("embedded moderator task survived a task override")
	}
	if got, want := expectedOutputBlock(t, got), expectedOutputBlock(t, golden); got != want {
		t.Errorf("[EXPECTED OUTPUT] changed under SAFE-block overrides\n got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(got, ctx.EvaluationJSON) {
		t.Error("evaluator-output content block must remain present under overrides")
	}
}

// A single-segment override leaves the OTHER SAFE blocks embedded (no bleed).
func TestComposeEvaluate_RoleOnlyOverride_LeavesTaskEmbedded(t *testing.T) {
	ctx := goldenEvaluatorCtx()
	ctx.Overrides = map[string]string{"role": sentRole}
	got := ComposeEvaluateInstruction(ctx)
	if !strings.Contains(got, sentRole) {
		t.Error("role override not applied")
	}
	if !strings.Contains(got, "Grade the learner's free-text answer") {
		t.Error("task block must stay embedded when only role is overridden")
	}
}

// -----------------------------------------------------------------------------
// 3. readPromptOverridesFromState — wire-form tolerance + fail-soft
// -----------------------------------------------------------------------------

func TestReadPromptOverridesFromState_Forms(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want map[string]string
	}{
		{"json string", `{"role":"R","task":"T"}`, map[string]string{"role": "R", "task": "T"}},
		{"native map[string]string", map[string]string{"examples": "E"}, map[string]string{"examples": "E"}},
		{"native map[string]any", map[string]any{"role": "R"}, map[string]string{"role": "R"}},
		{"empty string", "", nil},
		{"empty object", `{}`, nil},
		{"malformed json", `{not json`, nil},
		{"wrong type", 42, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &mapState{m: map[string]any{"prompt_overrides_json": c.val}}
			got := readPromptOverridesFromState(st, "prompt_overrides_json")
			if len(got) != len(c.want) {
				t.Fatalf("len mismatch: got %v want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("key %q: got %q want %q", k, got[k], v)
				}
			}
		})
	}
	// absent key → nil, never panic.
	if got := readPromptOverridesFromState(&mapState{m: map[string]any{}}, "prompt_overrides_json"); got != nil {
		t.Errorf("absent key must yield nil; got %v", got)
	}
}

// The builders thread prompt_overrides_json into ctx.Overrides.
func TestBuilders_ReadOverridesIntoContext(t *testing.T) {
	ov := `{"role":"R","task":"T","examples":"E"}`

	evSt := &mapState{m: map[string]any{
		"learner_answer": "ans", "rubric_json": "{}", "prompt_overrides_json": ov,
	}}
	ev, err := BuildEvaluatorTaskContextFromState(evSt)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Overrides["role"] != "R" || ev.Overrides["task"] != "T" || ev.Overrides["examples"] != "E" {
		t.Errorf("evaluator overrides not threaded: %v", ev.Overrides)
	}

	sumSt := &mapState{m: map[string]any{
		"results_digest": "d", "prompt_overrides_json": ov,
	}}
	sum, err := BuildSummaryTaskContextFromState(sumSt)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Overrides["role"] != "R" {
		t.Errorf("summary overrides not threaded: %v", sum.Overrides)
	}

	modSt := &mapState{m: map[string]any{
		"evaluation_json": "{}", "learner_answer": "ans", "rubric_json": "{}", "prompt_overrides_json": ov,
	}}
	mod, err := BuildModeratorTaskContextFromState(modSt)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Overrides["task"] != "T" {
		t.Errorf("moderator overrides not threaded: %v", mod.Overrides)
	}
}
