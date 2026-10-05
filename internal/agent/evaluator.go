// evaluator.go — oe_evaluator prompt composer + mode-branch context builder
// (ADR-172 §D2/§D4/§D5).
//
// The evaluator has TWO modes, selected per-turn from the session-state "mode"
// key (NOT a static system prompt — the qgen "critic never saw the candidate"
// lesson, project_qgen_critic_never_saw_candidate):
//
//   - evaluate       — grade ONE OE answer against a weighted rubric +
//     model_answer → per-criterion sub-scores + an
//     always-present per-question comment.
//   - assess_summary — the holistic whole-assessment narrative over ALL answers
//     (MCQ + OE). No rubric, no per-criterion scoring, no
//     moderator loop.
//
// Composers are PURE functions (same input → same output) per ADR-141 D2
// transparency. The 6-block CREATE shape (CONTEXT / ROLE / EXAMPLES / AUDIENCE
// / TASK / EXPECTED OUTPUT) mirrors qgen's composer so the agents read uniformly.
package agent

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed prompts/v1/evaluator_role.txt
var evaluatorRoleV1 string

//go:embed prompts/v1/evaluator_task.txt
var evaluatorTaskV1 string

//go:embed prompts/v1/evaluator_output.txt
var evaluatorOutputV1 string

//go:embed prompts/v1/summary_role.txt
var summaryRoleV1 string

//go:embed prompts/v1/summary_task.txt
var summaryTaskV1 string

//go:embed prompts/v1/summary_output.txt
var summaryOutputV1 string

// -----------------------------------------------------------------------------
// EvaluatorTaskContext — per-call inputs for evaluate mode
// -----------------------------------------------------------------------------

// EvaluatorTaskContext carries the per-call grading context the composer folds
// into the prompt for ModeEvaluate. The orchestrator stamps these into ADK
// session state per OE answer; the per-turn InstructionProvider reads them via
// BuildEvaluatorTaskContextFromState.
type EvaluatorTaskContext struct {
	// TenantID + LearnerGCID for trace span attribution + IMDA D1
	// accountability evidence.
	TenantID    string
	LearnerGCID string

	// SubmissionID + TestSetQuestionID + QuestionID — provenance / correlation.
	SubmissionID      string
	TestSetQuestionID string
	QuestionID        string

	// PointsPossible — surfaced for transparency; the deterministic Go scorer
	// (ComputeComposite) maps the LLM's per-criterion sub-scores to this.
	PointsPossible int

	// Subject + Topic — let the evaluator calibrate domain expectations.
	Subject string
	Topic   string

	// Prompt — the question stem the learner answered.
	Prompt string

	// LearnerAnswer — the learner's free-text OE response (REQUIRED; the
	// "candidate" of the qgen lesson — fail loud if missing).
	LearnerAnswer string

	// RubricJSON — the mandatory weighted rubric snapshot (REQUIRED §D4).
	RubricJSON string

	// ModelAnswer — the reference answer snapshot.
	ModelAnswer string

	// AttemptIndex — 0-based re-grade counter (0 = first attempt).
	AttemptIndex int

	// MaxIterations — the orchestrator's loop budget (default 2; surfaced for
	// transparency only — the orchestrator owns the loop decision).
	MaxIterations int

	// PriorModeratorFeedback — the moderator's rejection feedback from the prior
	// attempt (empty on AttemptIndex=0). The evaluator addresses each point.
	PriorModeratorFeedback string

	// Overrides carries operator-supplied SAFE-block replacements (ADR-197 M-B)
	// keyed by segment_id. Only the SAFE blocks are overridable: "role", "task",
	// "examples". The [EXPECTED OUTPUT] JSON-contract block, the content blocks
	// (rubric / learner-answer / model-answer), and any safety preamble are NEVER
	// routed through an override. Absent / nil / empty ⇒ the embedded prompt
	// fragments are used, keeping the composed prompt byte-identical to
	// pre-override behaviour (proven by the golden test). The orchestrator stamps
	// prompt_overrides_json into session state (read by
	// BuildEvaluatorTaskContextFromState).
	Overrides map[string]string
}

// SummaryTaskContext — per-call input for assess_summary mode.
type SummaryTaskContext struct {
	TenantID     string
	LearnerGCID  string
	SubmissionID string
	AssessmentID string

	// Subject — assessment-level subject for theming.
	Subject string

	// PassingThresholdPercent — surfaced so the narrative can state pass/fail.
	PassingThresholdPercent int

	// ResultsDigest — the verbatim all-answers digest the orchestrator builds
	// (every question's type / prompt / subject / topic / outcome). REQUIRED —
	// without it the summarizer has nothing to narrate (fail loud).
	ResultsDigest string

	// Overrides — operator SAFE-block replacements (ADR-197 M-B) keyed by
	// segment_id. CHO-2368 P2 mode split: assess_summary reads ONLY the
	// "summary_role" / "summary_task" / "summary_examples" keys (the catalogue
	// segment vocabulary); the generic grade-mode keys are inert here so one
	// oe_evaluator plan can revise each mode independently. The
	// [EXPECTED OUTPUT] block, the results-digest content block, and any safety
	// preamble are NEVER overridable. nil / empty ⇒ embedded fragments ⇒
	// byte-identical to pre-override.
	Overrides map[string]string
}

// -----------------------------------------------------------------------------
// Per-turn context builders — read ADK session state (fail-loud)
// -----------------------------------------------------------------------------

// EvaluatorMode reads + validates the per-turn evaluator mode from session
// state. Defaults to ModeEvaluate when the "mode" key is absent (the dominant
// per-answer path); an explicit unknown value fails loud.
func EvaluatorMode(state stateGetter) (Mode, error) {
	if state == nil {
		return "", fmt.Errorf("oe_evaluator: state must not be nil")
	}
	raw := readStateString(state, "mode")
	if raw == "" {
		return ModeEvaluate, nil
	}
	m := Mode(raw)
	if err := assertValidMode(m); err != nil {
		return "", err
	}
	return m, nil
}

// BuildEvaluatorTaskContextFromState assembles the evaluate-mode context from
// ADK session state. State keys (set by the orchestrator's grading crew node):
//
//	tenant_id / user_gcid (or learner_gcid)
//	submission_id / test_set_question_id / question_id
//	points_possible       — int (float64 over the JSON path)
//	subject / topic / prompt
//	learner_answer        — string (REQUIRED; fail loud if blank — the qgen lesson)
//	rubric_json           — string (REQUIRED §D4; fail loud if blank)
//	model_answer          — string
//	attempt_index / max_iterations — int
//	prior_moderator_feedback — string (re-grade path)
//
// Fail-loud posture (feedback_no_stubs_real_wiring): a nil state, a missing
// learner answer, or a missing rubric returns an error so the InstructionProvider
// aborts the invocation rather than grading nothing — the exact bug qgen's
// critic hit (project_qgen_critic_never_saw_candidate).
func BuildEvaluatorTaskContextFromState(state stateGetter) (EvaluatorTaskContext, error) {
	if state == nil {
		return EvaluatorTaskContext{}, fmt.Errorf("oe_evaluator: state must not be nil")
	}

	learnerAnswer := readStateString(state, "learner_answer")
	if strings.TrimSpace(learnerAnswer) == "" {
		return EvaluatorTaskContext{}, fmt.Errorf(
			"oe_evaluator: no learner answer in session state (learner_answer empty) — " +
				"the orchestrator must stamp the OE answer before evaluation")
	}

	rubricJSON := readStateString(state, "rubric_json")
	if strings.TrimSpace(rubricJSON) == "" {
		return EvaluatorTaskContext{}, fmt.Errorf(
			"oe_evaluator: no rubric in session state (rubric_json empty) — ADR-172 §D4 " +
				"makes a weighted rubric a grading precondition")
	}

	learnerGCID := readStateString(state, "learner_gcid")
	if learnerGCID == "" {
		learnerGCID = readStateString(state, "user_gcid")
	}

	return EvaluatorTaskContext{
		TenantID:               readStateString(state, "tenant_id"),
		LearnerGCID:            learnerGCID,
		SubmissionID:           readStateString(state, "submission_id"),
		TestSetQuestionID:      readStateString(state, "test_set_question_id"),
		QuestionID:             readStateString(state, "question_id"),
		PointsPossible:         readStateInt(state, "points_possible"),
		Subject:                readStateString(state, "subject"),
		Topic:                  readStateString(state, "topic"),
		Prompt:                 readStateString(state, "prompt"),
		LearnerAnswer:          learnerAnswer,
		RubricJSON:             rubricJSON,
		ModelAnswer:            readStateString(state, "model_answer"),
		AttemptIndex:           readStateInt(state, "attempt_index"),
		MaxIterations:          readStateInt(state, "max_iterations"),
		PriorModeratorFeedback: readStateString(state, "prior_moderator_feedback"),
		// ADR-197 M-B (read side) — operator SAFE-block overrides. Absent on the
		// dominant path ⇒ nil ⇒ byte-identical to pre-override (golden test).
		Overrides: readPromptOverridesFromState(state, "prompt_overrides_json"),
	}, nil
}

// BuildSummaryTaskContextFromState assembles the assess_summary-mode context.
// State keys:
//
//	tenant_id / user_gcid (or learner_gcid)
//	submission_id / assessment_id / subject
//	passing_threshold_percent — int
//	results_digest            — string (REQUIRED; fail loud if blank)
//
// Fail-loud on a missing digest — the summarizer cannot narrate nothing.
func BuildSummaryTaskContextFromState(state stateGetter) (SummaryTaskContext, error) {
	if state == nil {
		return SummaryTaskContext{}, fmt.Errorf("oe_evaluator(summary): state must not be nil")
	}

	digest := readStateString(state, "results_digest")
	if strings.TrimSpace(digest) == "" {
		return SummaryTaskContext{}, fmt.Errorf(
			"oe_evaluator(summary): no results_digest in session state — the orchestrator " +
				"must stamp the all-answers digest before assess_summary")
	}

	learnerGCID := readStateString(state, "learner_gcid")
	if learnerGCID == "" {
		learnerGCID = readStateString(state, "user_gcid")
	}

	return SummaryTaskContext{
		TenantID:                readStateString(state, "tenant_id"),
		LearnerGCID:             learnerGCID,
		SubmissionID:            readStateString(state, "submission_id"),
		AssessmentID:            readStateString(state, "assessment_id"),
		Subject:                 readStateString(state, "subject"),
		PassingThresholdPercent: readStateInt(state, "passing_threshold_percent"),
		ResultsDigest:           digest,
		// ADR-197 M-B (read side) — operator SAFE-block overrides. Absent ⇒ nil
		// ⇒ byte-identical to pre-override (golden test).
		Overrides: readPromptOverridesFromState(state, "prompt_overrides_json"),
	}, nil
}

// -----------------------------------------------------------------------------
// Composers — pure functions (D2 transparency)
// -----------------------------------------------------------------------------

// ComposeEvaluateInstruction emits the deterministic 6-block CREATE prompt for
// evaluate mode, embedding the per-call learner answer + rubric + model_answer
// so the LLM grades the ACTUAL content (the executor sends only a "BEGIN"
// trigger as the user message — the content rides in the instruction, the qgen
// lesson). Pure function.
func ComposeEvaluateInstruction(ctx EvaluatorTaskContext) string {
	var b strings.Builder

	b.WriteString("## [CONTEXT]\n")
	b.WriteString("You are running inside the Chora oe_grading crew — the per-submission OE-grading " +
		"quality loop (ADR-172). The orchestrator (Python LangGraph) invokes you once per OE " +
		"answer, then a separate moderator agent judges your output and may reject it for re-grading " +
		"(≤2 iterations). Every output is audited against IMDA Model AI Governance criteria — D1 " +
		"accountability + D2 transparency in particular.\n")
	fmt.Fprintf(&b, "Call context: tenant_id=%s, learner_gcid=%s, submission_id=%s, "+
		"test_set_question_id=%s, question_id=%s, points_possible=%d, attempt=%d/%d.\n",
		safe(ctx.TenantID, "<unset>"), safe(ctx.LearnerGCID, "<unset>"),
		safe(ctx.SubmissionID, "<unset>"), safe(ctx.TestSetQuestionID, "<unset>"),
		safe(ctx.QuestionID, "<unset>"), ctx.PointsPossible,
		ctx.AttemptIndex+1, zeroOr(ctx.MaxIterations, 2)+1)
	if hints := subjectTopicHints(ctx.Subject, ctx.Topic); hints != "" {
		fmt.Fprintf(&b, "Subject/topic: %s\n", hints)
	}
	b.WriteString("\n")

	// [ROLE] — ADR-197 M-B: SAFE block, operator-overridable.
	b.WriteString("## [ROLE]\n")
	b.WriteString(overrideOr(ctx.Overrides, "role", strings.TrimSpace(evaluatorRoleV1)))
	b.WriteString("\n\n")

	// [EXAMPLES] — ADR-197 M-B: SAFE block, operator-overridable (the embedded
	// body incl. trailing spacing is the no-override fallback, byte-identical).
	b.WriteString("## [EXAMPLES]\n")
	b.WriteString(overrideOr(ctx.Overrides, "examples",
		"Strong answer covering 3 of 3 rubric criteria → each criterion_scores.score near its "+
			"max_score with evidence-grounded feedback + an affirming comment naming a stretch. "+
			"Partial answer addressing 1 of 3 criteria → that criterion scored high, the other two scored "+
			"low with feedback naming the missing concept + a comment stating concretely what would raise "+
			"the grade.\n\n"))

	b.WriteString("## [AUDIENCE]\n")
	b.WriteString("Your JSON is consumed by (1) the moderator agent (judges rubric fidelity / " +
		"hallucination / consistency), then (2) the chora-delivery executor's deterministic Go scorer " +
		"(derives points_earned from your sub-scores + the rubric weights). The per-question comment is " +
		"surfaced to the LEARNER only after the instructor releases results. NEVER address the learner " +
		"directly in feedback fields — those are grading rationale.\n\n")

	// [TASK] — ADR-197 M-B: SAFE block, operator-overridable.
	b.WriteString("## [TASK]\n")
	b.WriteString(overrideOr(ctx.Overrides, "task", strings.TrimSpace(evaluatorTaskV1)))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## [QUESTION PROMPT]\n%s\n\n", safe(strings.TrimSpace(ctx.Prompt), "<unset>"))

	b.WriteString("## [RUBRIC]\n")
	b.WriteString("Grade against EXACTLY these weighted criteria (verbatim JSON snapshot):\n")
	b.WriteString(strings.TrimSpace(ctx.RubricJSON))
	b.WriteString("\n\n")

	b.WriteString("## [MODEL ANSWER]\n")
	b.WriteString("Reference answer (compare for correctness; do NOT require the learner to match its " +
		"wording — credit equivalent correct reasoning):\n")
	b.WriteString(safe(strings.TrimSpace(ctx.ModelAnswer), "<no model answer supplied>"))
	b.WriteString("\n\n")

	b.WriteString("## [LEARNER ANSWER]\n")
	b.WriteString("Grade EXACTLY this learner free-text answer:\n")
	b.WriteString(strings.TrimSpace(ctx.LearnerAnswer))
	b.WriteString("\n\n")

	if fb := strings.TrimSpace(ctx.PriorModeratorFeedback); fb != "" {
		b.WriteString("## [PRIOR MODERATOR FEEDBACK]\n")
		b.WriteString("You are RE-GRADING. The moderator rejected your previous attempt with this " +
			"feedback — address each point (correct your scoring/feedback, or justify where you stand by it):\n")
		b.WriteString(fb)
		b.WriteString("\n\n")
	}

	b.WriteString("## [EXPECTED OUTPUT]\n")
	b.WriteString(strings.TrimSpace(evaluatorOutputV1))
	b.WriteString("\n")

	return b.String()
}

// ComposeSummaryInstruction emits the deterministic 6-block CREATE prompt for
// assess_summary mode, embedding the all-answers digest. Pure function.
func ComposeSummaryInstruction(ctx SummaryTaskContext) string {
	var b strings.Builder

	b.WriteString("## [CONTEXT]\n")
	b.WriteString("You are running inside the Chora oe_grading crew in assess_summary mode (ADR-172 §D5). " +
		"After every OE answer in a submission has been graded, you write ONE holistic whole-assessment " +
		"narrative spanning MCQ + OE. No moderator reviews this output; it is AI-drafted, " +
		"instructor-editable, and learner-visible on release. Audited against IMDA D2 transparency + D4 " +
		"human-oversight.\n")
	fmt.Fprintf(&b, "Call context: tenant_id=%s, learner_gcid=%s, submission_id=%s, assessment_id=%s, "+
		"subject=%s, passing_threshold_percent=%d.\n",
		safe(ctx.TenantID, "<unset>"), safe(ctx.LearnerGCID, "<unset>"),
		safe(ctx.SubmissionID, "<unset>"), safe(ctx.AssessmentID, "<unset>"),
		safe(ctx.Subject, "<unset>"), ctx.PassingThresholdPercent)
	b.WriteString("\n")

	// [ROLE] — ADR-197 M-B: SAFE block, operator-overridable. CHO-2368 P2 mode
	// split: grade + summary share ONE override map but carry DIFFERENT embedded
	// bodies, so summary mode reads its OWN summary_* keys (the P1 catalogue
	// segment vocabulary) and the generic grade keys stay inert here.
	b.WriteString("## [ROLE]\n")
	b.WriteString(overrideOr(ctx.Overrides, "summary_role", strings.TrimSpace(summaryRoleV1)))
	b.WriteString("\n\n")

	// [EXAMPLES] — ADR-197 M-B: SAFE block, operator-overridable (embedded body
	// incl. trailing spacing is the no-override fallback, byte-identical).
	b.WriteString("## [EXAMPLES]\n")
	b.WriteString(overrideOr(ctx.Overrides, "summary_examples",
		"\"You scored 70% — a solid pass. Your grasp of cell structure came through clearly in "+
			"the MCQ section. There's room to improve articulating open-ended answers in photosynthesis and "+
			"animal biology; aim to name the specific process steps and link cause to effect next time.\"\n\n"))

	b.WriteString("## [AUDIENCE]\n")
	b.WriteString("The instructor reviews + lightly edits your narrative, then releases it to the learner. " +
		"Write it learner-facing (second person), encouraging, specific.\n\n")

	// [TASK] — ADR-197 M-B: SAFE block, operator-overridable (summary_* key per
	// the mode split above).
	b.WriteString("## [TASK]\n")
	b.WriteString(overrideOr(ctx.Overrides, "summary_task", strings.TrimSpace(summaryTaskV1)))
	b.WriteString("\n\n")

	b.WriteString("## [ASSESSMENT RESULTS]\n")
	b.WriteString("Narrate from EXACTLY these per-question results (verbatim digest):\n")
	b.WriteString(strings.TrimSpace(ctx.ResultsDigest))
	b.WriteString("\n\n")

	b.WriteString("## [EXPECTED OUTPUT]\n")
	b.WriteString(strings.TrimSpace(summaryOutputV1))
	b.WriteString("\n")

	return b.String()
}

// -----------------------------------------------------------------------------
// Small helpers
// -----------------------------------------------------------------------------

// subjectTopicHints joins subject + topic into a single line. Empty when neither set.
func subjectTopicHints(subject, topic string) string {
	var parts []string
	if v := strings.TrimSpace(subject); v != "" {
		parts = append(parts, fmt.Sprintf("subject=%s", v))
	}
	if v := strings.TrimSpace(topic); v != "" {
		parts = append(parts, fmt.Sprintf("topic=%s", v))
	}
	return strings.Join(parts, ", ")
}

// zeroOr returns fallback when v == 0, else v.
func zeroOr(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
