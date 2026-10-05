// moderator.go — oe_moderator prompt composer + per-turn context builder
// (ADR-172 §D2).
//
// The moderator is the JUDGE of the 2-agent OE-grading crew (mirrors qgen's
// critic, ADR-153): it reviews the evaluator's grading of ONE open-ended answer
// (per-criterion sub-scores + feedback + comment) against the rubric + learner
// answer and decides accept | reject+feedback for rubric fidelity, hallucination,
// and internal consistency. It does NOT re-grade or emit its own sub-scores — on
// reject the evaluator re-grades with the feedback. The LangGraph orchestrator
// owns the ≤2-iteration loop.
//
// Like evaluator.go the composer is a PURE function (same input → same output)
// per ADR-141 D2 transparency, and the per-turn InstructionProvider
// (cmd/oe_moderator) injects the per-request judging context from ADK session
// state — NOT a static system prompt. This is the qgen "critic never saw the
// candidate" lesson (project_qgen_critic_never_saw_candidate): a deferred/empty
// InstructionProvider makes the moderator judge nothing and answer
// "input_unparseable", forcing the orchestrator loop to max iterations on every
// answer.
package agent

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed prompts/v1/moderator_role.txt
var moderatorRoleV1 string

//go:embed prompts/v1/moderator_task.txt
var moderatorTaskV1 string

//go:embed prompts/v1/moderator_output.txt
var moderatorOutputV1 string

// -----------------------------------------------------------------------------
// ModeratorTaskContext — per-call inputs
// -----------------------------------------------------------------------------

// ModeratorTaskContext carries the per-call judging context the composer folds
// into the prompt. The orchestrator stamps these into ADK session state per
// re-grade attempt; the per-turn InstructionProvider reads them via
// BuildModeratorTaskContextFromState.
type ModeratorTaskContext struct {
	// TenantID + LearnerGCID for trace span attribution + IMDA D1
	// accountability evidence.
	TenantID    string
	LearnerGCID string

	// SubmissionID + TestSetQuestionID + QuestionID — provenance / correlation.
	SubmissionID      string
	TestSetQuestionID string
	QuestionID        string

	// Subject + Topic — domain context (transparency only).
	Subject string
	Topic   string

	// Prompt — the question stem the learner answered.
	Prompt string

	// LearnerAnswer — the learner's free-text answer (REQUIRED — the moderator
	// checks the evaluator's feedback for hallucination AGAINST it; fail loud
	// if missing).
	LearnerAnswer string

	// RubricJSON — the mandatory weighted rubric snapshot (REQUIRED — the
	// moderator checks the evaluator scored EVERY criterion).
	RubricJSON string

	// ModelAnswer — the reference answer snapshot.
	ModelAnswer string

	// EvaluationJSON — the evaluator's grading output being judged (REQUIRED;
	// the "candidate" of the qgen lesson — fail loud if missing). Shape:
	// {points_earned, points_possible, criterion_scores:[...], comment}.
	EvaluationJSON string

	// AttemptIndex — 0-based re-grade counter (0 = first attempt).
	AttemptIndex int

	// MaxIterations — the orchestrator's loop budget (default 2; surfaced for
	// transparency only — the orchestrator owns the loop decision).
	MaxIterations int

	// Overrides carries operator-supplied SAFE-block replacements (ADR-197 M-B)
	// keyed by segment_id. Only the SAFE blocks are overridable: "role", "task",
	// "examples". The [EXPECTED OUTPUT] JSON-contract block, the content blocks
	// (rubric / learner-answer / model-answer / evaluator-output), and any safety
	// preamble are NEVER routed through an override. Absent / nil / empty ⇒ the
	// embedded prompt fragments are used, keeping the composed prompt
	// byte-identical to pre-override behaviour (proven by the golden test). The
	// orchestrator stamps prompt_overrides_json into session state (read by
	// BuildModeratorTaskContextFromState).
	Overrides map[string]string
}

// -----------------------------------------------------------------------------
// Per-turn context builder — reads ADK session state (fail-loud)
// -----------------------------------------------------------------------------

// BuildModeratorTaskContextFromState assembles the judging context from ADK
// session state. State keys (set by the orchestrator's grading crew moderate
// node):
//
//	tenant_id / user_gcid (or learner_gcid)
//	submission_id / test_set_question_id / question_id
//	subject / topic / prompt
//	learner_answer   — string (REQUIRED; fail loud if blank — the qgen lesson)
//	rubric_json      — string (REQUIRED; the moderator checks rubric fidelity)
//	model_answer     — string
//	evaluation_json  — string (REQUIRED; the evaluator output to judge — fail loud)
//	attempt_index / max_iterations — int
//
// Fail-loud posture (feedback_no_stubs_real_wiring): a nil state, a missing
// learner answer, a missing rubric, or a missing evaluation returns an error so
// the InstructionProvider aborts the invocation rather than judging nothing —
// the exact bug qgen's critic hit (project_qgen_critic_never_saw_candidate).
func BuildModeratorTaskContextFromState(state stateGetter) (ModeratorTaskContext, error) {
	if state == nil {
		return ModeratorTaskContext{}, fmt.Errorf("oe_moderator: state must not be nil")
	}

	evaluationJSON := readStateString(state, "evaluation_json")
	if strings.TrimSpace(evaluationJSON) == "" {
		return ModeratorTaskContext{}, fmt.Errorf(
			"oe_moderator: no evaluator output in session state (evaluation_json empty) — " +
				"the orchestrator must stamp the evaluator grading before moderation")
	}

	learnerAnswer := readStateString(state, "learner_answer")
	if strings.TrimSpace(learnerAnswer) == "" {
		return ModeratorTaskContext{}, fmt.Errorf(
			"oe_moderator: no learner answer in session state (learner_answer empty) — " +
				"the moderator cannot check the evaluator's feedback for hallucination without it")
	}

	rubricJSON := readStateString(state, "rubric_json")
	if strings.TrimSpace(rubricJSON) == "" {
		return ModeratorTaskContext{}, fmt.Errorf(
			"oe_moderator: no rubric in session state (rubric_json empty) — ADR-172 §D4 " +
				"makes a weighted rubric a grading precondition + the moderator checks rubric fidelity")
	}

	learnerGCID := readStateString(state, "learner_gcid")
	if learnerGCID == "" {
		learnerGCID = readStateString(state, "user_gcid")
	}

	return ModeratorTaskContext{
		TenantID:          readStateString(state, "tenant_id"),
		LearnerGCID:       learnerGCID,
		SubmissionID:      readStateString(state, "submission_id"),
		TestSetQuestionID: readStateString(state, "test_set_question_id"),
		QuestionID:        readStateString(state, "question_id"),
		Subject:           readStateString(state, "subject"),
		Topic:             readStateString(state, "topic"),
		Prompt:            readStateString(state, "prompt"),
		LearnerAnswer:     learnerAnswer,
		RubricJSON:        rubricJSON,
		ModelAnswer:       readStateString(state, "model_answer"),
		EvaluationJSON:    evaluationJSON,
		AttemptIndex:      readStateInt(state, "attempt_index"),
		MaxIterations:     readStateInt(state, "max_iterations"),
		// ADR-197 M-B (read side) — operator SAFE-block overrides. Absent ⇒ nil
		// ⇒ byte-identical to pre-override (golden test).
		Overrides: readPromptOverridesFromState(state, "prompt_overrides_json"),
	}, nil
}

// -----------------------------------------------------------------------------
// Composer — pure function (D2 transparency)
// -----------------------------------------------------------------------------

// ComposeModeratorInstruction emits the deterministic 6-block CREATE prompt for
// the moderator, embedding the per-call evaluator output + rubric + learner
// answer + model_answer so the LLM judges the ACTUAL grading (the executor sends
// only a "BEGIN" trigger as the user message — the content rides in the
// instruction, the qgen lesson). Pure function.
func ComposeModeratorInstruction(ctx ModeratorTaskContext) string {
	var b strings.Builder

	b.WriteString("## [CONTEXT]\n")
	b.WriteString("You are running inside the Chora oe_grading crew — the per-submission OE-grading " +
		"quality loop (ADR-172 §D2). The evaluator agent has just graded ONE open-ended answer; you judge " +
		"its grading. On reject, the evaluator re-grades with your feedback (≤2 iterations); on accept, the " +
		"grade is recorded and a human instructor reviews + may override it downstream in the R+ grading " +
		"queue (that gate lives in chora-delivery, not in your loop). Every output is audited against IMDA " +
		"Model AI Governance criteria — D1 accountability + D2 transparency in particular.\n")
	fmt.Fprintf(&b, "Call context: tenant_id=%s, learner_gcid=%s, submission_id=%s, "+
		"test_set_question_id=%s, question_id=%s, attempt=%d/%d.\n",
		safe(ctx.TenantID, "<unset>"), safe(ctx.LearnerGCID, "<unset>"),
		safe(ctx.SubmissionID, "<unset>"), safe(ctx.TestSetQuestionID, "<unset>"),
		safe(ctx.QuestionID, "<unset>"), ctx.AttemptIndex+1, zeroOr(ctx.MaxIterations, 2)+1)
	if hints := subjectTopicHints(ctx.Subject, ctx.Topic); hints != "" {
		fmt.Fprintf(&b, "Subject/topic: %s\n", hints)
	}
	b.WriteString("\n")

	// [ROLE] — ADR-197 M-B: SAFE block, operator-overridable.
	b.WriteString("## [ROLE]\n")
	b.WriteString(overrideOr(ctx.Overrides, "role", strings.TrimSpace(moderatorRoleV1)))
	b.WriteString("\n\n")

	// [EXAMPLES] — ADR-197 M-B: SAFE block, operator-overridable. The embedded
	// body is assembled into exBuf so the no-override path stays byte-identical.
	b.WriteString("## [EXAMPLES]\n")
	var exBuf strings.Builder
	exBuf.WriteString("Accept: every rubric criterion is scored, each sub-score is grounded in a phrase the " +
		"learner actually wrote, the comment is present and consistent with the scores → " +
		"{\"accepted\": true, \"feedback\": \"\"}.\n")
	exBuf.WriteString("Reject (dropped criterion): the rubric has 3 criteria but criterion_scores has 2 → " +
		"{\"accepted\": false, \"feedback\": \"criterion c3 was not scored; score every rubric criterion " +
		"by criterion_id\"}.\n")
	exBuf.WriteString("Reject (hallucination): feedback credits \"the learner's mention of ATP\" but the " +
		"learner answer never mentions ATP → {\"accepted\": false, \"feedback\": \"criterion c2 feedback " +
		"cites ATP which the learner never wrote; lower the score and cite only evidence present in the " +
		"answer\"}.\n")
	exBuf.WriteString("Reject (missing comment): comment is empty → {\"accepted\": false, \"feedback\": " +
		"\"comment is empty — ADR-172 §D4 requires a per-question comment for every answer\"}.\n\n")
	b.WriteString(overrideOr(ctx.Overrides, "examples", exBuf.String()))

	b.WriteString("## [AUDIENCE]\n")
	b.WriteString("Your JSON is consumed by the chora-ai-kernel-orchestrator's quality_gate node. It routes " +
		"on `accepted`: true → record the grade; false + iterations left → the evaluator re-grades with " +
		"your feedback as [PRIOR MODERATOR FEEDBACK]; false + iterations exhausted → the last evaluator " +
		"output ships flagged for priority human review. NEVER address the learner — your feedback is for " +
		"the evaluator's next pass.\n\n")

	// [TASK] — ADR-197 M-B: SAFE block, operator-overridable.
	b.WriteString("## [TASK]\n")
	b.WriteString(overrideOr(ctx.Overrides, "task", strings.TrimSpace(moderatorTaskV1)))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## [QUESTION PROMPT]\n%s\n\n", safe(strings.TrimSpace(ctx.Prompt), "<unset>"))

	b.WriteString("## [RUBRIC]\n")
	b.WriteString("The weighted criteria the evaluator was required to score (verbatim JSON snapshot):\n")
	b.WriteString(strings.TrimSpace(ctx.RubricJSON))
	b.WriteString("\n\n")

	b.WriteString("## [MODEL ANSWER]\n")
	b.WriteString(safe(strings.TrimSpace(ctx.ModelAnswer), "<no model answer supplied>"))
	b.WriteString("\n\n")

	b.WriteString("## [LEARNER ANSWER]\n")
	b.WriteString("The learner free-text answer the evaluator graded — check the evaluator's feedback " +
		"against EXACTLY this (no credit/penalty for content not present here):\n")
	b.WriteString(strings.TrimSpace(ctx.LearnerAnswer))
	b.WriteString("\n\n")

	b.WriteString("## [EVALUATOR OUTPUT]\n")
	b.WriteString("Judge EXACTLY this evaluator grading (verbatim JSON — per-criterion sub-scores + " +
		"feedback + comment):\n")
	b.WriteString(strings.TrimSpace(ctx.EvaluationJSON))
	b.WriteString("\n\n")

	b.WriteString("## [EXPECTED OUTPUT]\n")
	b.WriteString(strings.TrimSpace(moderatorOutputV1))
	b.WriteString("\n")

	return b.String()
}
