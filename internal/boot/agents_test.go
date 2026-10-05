// Tests for the per-turn instruction providers and the agent constructors.
//
// The provider tests are the highest-value ones in this package. The whole
// design rationale in the two main.go headers is the qgen "critic never saw the
// candidate" lesson: a boot-time compose would emit the same prompt no matter
// what the orchestrator stamped into session state, so the agent would grade
// nothing and nobody would notice, because the LLM still answers. These tests
// prove the provider genuinely re-reads state on every call and refuses to
// compose when the grading content is absent.
package boot

import (
	"strings"
	"testing"

	"google.golang.org/adk/agent"
)

// evaluateState is a complete evaluate-mode session state.
func evaluateState() map[string]any {
	return map[string]any{
		"mode":                 "evaluate",
		"tenant_id":            "tenant-oe",
		"user_gcid":            "gcid-learner",
		"submission_id":        "sub-1",
		"test_set_question_id": "tsq-1",
		"question_id":          "q-1",
		"points_possible":      float64(10),
		"subject":              "biology",
		"topic":                "photosynthesis",
		"prompt":               "Explain photosynthesis in your own words.",
		"learner_answer":       "Plants convert light energy into chemical energy stored as glucose.",
		"rubric_json":          `{"criteria":[{"id":"c1","weight":1.0,"descriptor":"identifies the energy conversion"}]}`,
		"model_answer":         "Photosynthesis converts light energy into chemical energy.",
		"attempt_index":        float64(1),
		"max_iterations":       float64(2),
	}
}

// summaryState is a complete assess_summary-mode session state.
func summaryState() map[string]any {
	return map[string]any{
		"mode":                      "assess_summary",
		"tenant_id":                 "tenant-oe",
		"user_gcid":                 "gcid-learner",
		"submission_id":             "sub-1",
		"assessment_id":             "assess-1",
		"subject":                   "biology",
		"passing_threshold_percent": float64(60),
		"results_digest":            "Q1 mcq correct. Q2 oe partial credit on the energy conversion criterion.",
	}
}

// moderatorState is a complete moderator session state.
func moderatorState() map[string]any {
	return map[string]any{
		"tenant_id":       "tenant-oe",
		"user_gcid":       "gcid-learner",
		"submission_id":   "sub-1",
		"question_id":     "q-1",
		"subject":         "biology",
		"prompt":          "Explain photosynthesis in your own words.",
		"learner_answer":  "Plants convert light energy into chemical energy stored as glucose.",
		"rubric_json":     `{"criteria":[{"id":"c1","weight":1.0,"descriptor":"identifies the energy conversion"}]}`,
		"model_answer":    "Photosynthesis converts light energy into chemical energy.",
		"evaluation_json": `{"criterion_scores":[{"criterion_id":"c1","score":0.8}],"comment":"Good grasp of the energy conversion."}`,
		"attempt_index":   float64(1),
		"max_iterations":  float64(2),
	}
}

// ---------------------------------------------------------------------------
// EvaluatorInstructionProvider
// ---------------------------------------------------------------------------

func TestEvaluatorInstructionProvider_evaluateModeInjectsTheLearnerAnswer(t *testing.T) {
	state := evaluateState()
	got, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err != nil {
		t.Fatalf("provider returned error: %v", err)
	}

	// The content blocks must carry the ACTUAL per-request grading material.
	// The executor sends only a "BEGIN" trigger as the user message, so an
	// empty block here means the LLM grades nothing.
	for _, want := range []string{
		"## [LEARNER ANSWER]",
		"## [RUBRIC]",
		"## [MODEL ANSWER]",
		"## [EXPECTED OUTPUT]",
		state["learner_answer"].(string),
		state["rubric_json"].(string),
		state["prompt"].(string),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("evaluate instruction missing %q", want)
		}
	}
	if strings.Contains(got, "## [ASSESSMENT RESULTS]") {
		t.Errorf("evaluate instruction carries the assess_summary results block: the mode branch is broken")
	}
}

func TestEvaluatorInstructionProvider_assessSummaryModeInjectsTheDigest(t *testing.T) {
	state := summaryState()
	got, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err != nil {
		t.Fatalf("provider returned error: %v", err)
	}

	if !strings.Contains(got, "## [ASSESSMENT RESULTS]") {
		t.Errorf("assess_summary instruction missing the results block")
	}
	if !strings.Contains(got, state["results_digest"].(string)) {
		t.Errorf("assess_summary instruction does not carry the all-answers digest")
	}
	// assess_summary has no rubric and no per-criterion scoring (ADR-172 §D5).
	if strings.Contains(got, "## [RUBRIC]") {
		t.Errorf("assess_summary instruction carries a rubric block: the mode branch is broken")
	}
}

func TestEvaluatorInstructionProvider_absentModeDefaultsToEvaluate(t *testing.T) {
	// The orchestrator always stamps mode explicitly on a real run, but the
	// per-answer path is the dominant one, so an absent key must not fail.
	state := evaluateState()
	delete(state, "mode")

	got, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err != nil {
		t.Fatalf("provider returned error for absent mode: %v", err)
	}
	if !strings.Contains(got, "## [LEARNER ANSWER]") {
		t.Errorf("absent mode did not compose the evaluate prompt")
	}
}

func TestEvaluatorInstructionProvider_recomposesPerTurn(t *testing.T) {
	// The ADK runtime calls the provider on EVERY model turn. Two calls with
	// different state must produce different prompts. This is the assertion
	// that distinguishes a per-turn provider from a boot-time compose, which
	// would return the same string forever.
	provider := EvaluatorInstructionProvider()

	first, err := provider(newFakeCtx(evaluateState()))
	if err != nil {
		t.Fatalf("evaluate call: %v", err)
	}

	second := evaluateState()
	second["learner_answer"] = "Photosynthesis happens in the mitochondria."
	secondOut, err := provider(newFakeCtx(second))
	if err != nil {
		t.Fatalf("second evaluate call: %v", err)
	}

	if first == secondOut {
		t.Fatalf("provider returned an identical instruction for a different learner answer: per-turn recomposition is broken")
	}
	if !strings.Contains(secondOut, "mitochondria") {
		t.Errorf("second instruction does not carry the second learner answer")
	}

	summaryOut, err := provider(newFakeCtx(summaryState()))
	if err != nil {
		t.Fatalf("summary call: %v", err)
	}
	if summaryOut == first {
		t.Errorf("assess_summary and evaluate produced the same instruction: the mode branch never fired")
	}
}

func TestEvaluatorInstructionProvider_unknownModeFailsLoud(t *testing.T) {
	state := evaluateState()
	state["mode"] = "grade_harder"

	_, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("provider accepted mode \"grade_harder\": an unknown mode must abort the turn, not coerce to evaluate")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("error %q does not name the invalid mode", err)
	}
}

func TestEvaluatorInstructionProvider_missingLearnerAnswerFailsLoud(t *testing.T) {
	// Blank, not merely absent: a whitespace answer is the shape a broken
	// orchestrator actually produces.
	state := evaluateState()
	state["learner_answer"] = "   "

	_, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("provider composed a prompt with no learner answer: the agent would grade nothing")
	}
	if !strings.Contains(err.Error(), "learner_answer") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

func TestEvaluatorInstructionProvider_missingRubricFailsLoud(t *testing.T) {
	state := evaluateState()
	delete(state, "rubric_json")

	_, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("provider composed a prompt with no rubric: ADR-172 §D4 makes it a grading precondition")
	}
	if !strings.Contains(err.Error(), "rubric_json") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

func TestEvaluatorInstructionProvider_missingResultsDigestFailsLoud(t *testing.T) {
	state := summaryState()
	delete(state, "results_digest")

	_, err := EvaluatorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("provider composed an assess_summary prompt with no digest: the summarizer would narrate nothing")
	}
	if !strings.Contains(err.Error(), "results_digest") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

// ---------------------------------------------------------------------------
// ModeratorInstructionProvider
// ---------------------------------------------------------------------------

func TestModeratorInstructionProvider_injectsTheEvaluationToJudge(t *testing.T) {
	state := moderatorState()
	got, err := ModeratorInstructionProvider()(newFakeCtx(state))
	if err != nil {
		t.Fatalf("provider returned error: %v", err)
	}

	for _, want := range []string{
		"## [EVALUATOR OUTPUT]",
		"## [LEARNER ANSWER]",
		"## [RUBRIC]",
		"## [EXPECTED OUTPUT]",
		state["evaluation_json"].(string),
		state["learner_answer"].(string),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("moderator instruction missing %q", want)
		}
	}
}

func TestModeratorInstructionProvider_recomposesPerTurn(t *testing.T) {
	provider := ModeratorInstructionProvider()

	first, err := provider(newFakeCtx(moderatorState()))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	second := moderatorState()
	second["evaluation_json"] = `{"criterion_scores":[{"criterion_id":"c1","score":0.1}],"comment":"Missed the point entirely."}`
	secondOut, err := provider(newFakeCtx(second))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if first == secondOut {
		t.Fatalf("moderator returned an identical instruction for a different evaluation: it never saw the candidate")
	}
	if !strings.Contains(secondOut, "Missed the point entirely.") {
		t.Errorf("second instruction does not carry the second evaluation")
	}
}

func TestModeratorInstructionProvider_missingEvaluationFailsLoud(t *testing.T) {
	state := moderatorState()
	delete(state, "evaluation_json")

	_, err := ModeratorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("moderator composed a prompt with nothing to judge: the orchestrator loop would run to max iterations")
	}
	if !strings.Contains(err.Error(), "evaluation_json") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

func TestModeratorInstructionProvider_missingLearnerAnswerFailsLoud(t *testing.T) {
	state := moderatorState()
	state["learner_answer"] = ""

	_, err := ModeratorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("moderator composed a prompt with no learner answer: it cannot check the feedback for hallucination")
	}
	if !strings.Contains(err.Error(), "learner_answer") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

func TestModeratorInstructionProvider_missingRubricFailsLoud(t *testing.T) {
	state := moderatorState()
	delete(state, "rubric_json")

	_, err := ModeratorInstructionProvider()(newFakeCtx(state))
	if err == nil {
		t.Fatalf("moderator composed a prompt with no rubric: it checks rubric fidelity")
	}
	if !strings.Contains(err.Error(), "rubric_json") {
		t.Errorf("error %q does not name the missing key", err)
	}
}

// ---------------------------------------------------------------------------
// Agent constructors
// ---------------------------------------------------------------------------

func TestNewLLMAgent_namesTheAgent(t *testing.T) {
	a, err := NewLLMAgent("oe_test_agent", fakeLLM{name: "fake"}, EvaluatorInstructionProvider())
	if err != nil {
		t.Fatalf("NewLLMAgent: %v", err)
	}
	if a.Name() != "oe_test_agent" {
		t.Errorf("agent name = %q, want %q", a.Name(), "oe_test_agent")
	}
}

func TestNewEvaluatorAgent_pinsTheAgentName(t *testing.T) {
	// The orchestrator terminal-reads the ADK event author, so the agent name
	// is wire contract, not cosmetics.
	a, err := NewEvaluatorAgent(Config{CrewKind: CrewKindEvaluator, PromptVersion: "v1"}, fakeLLM{name: "fake"})
	if err != nil {
		t.Fatalf("NewEvaluatorAgent: %v", err)
	}
	if a.Name() != "oe_evaluator" {
		t.Errorf("evaluator agent name = %q, want %q", a.Name(), "oe_evaluator")
	}
}

func TestNewModeratorAgent_pinsTheAgentName(t *testing.T) {
	a, err := NewModeratorAgent(Config{CrewKind: CrewKindModerator, PromptVersion: "v1"}, fakeLLM{name: "fake"})
	if err != nil {
		t.Fatalf("NewModeratorAgent: %v", err)
	}
	if a.Name() != "oe_moderator" {
		t.Errorf("moderator agent name = %q, want %q", a.Name(), "oe_moderator")
	}
}

func TestNewAgents_areToolFree(t *testing.T) {
	// Both OE-grading agents are tool-free by design: the grading content
	// rides in the instruction, and there are no external calls to make. A
	// sub-agent appearing here would mean somebody wired a tool loop.
	for _, tc := range []struct {
		name  string
		build func() (agent.Agent, error)
	}{
		{"evaluator", func() (agent.Agent, error) {
			return NewEvaluatorAgent(Config{CrewKind: CrewKindEvaluator, PromptVersion: "v1"}, fakeLLM{})
		}},
		{"moderator", func() (agent.Agent, error) {
			return NewModeratorAgent(Config{CrewKind: CrewKindModerator, PromptVersion: "v1"}, fakeLLM{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := tc.build()
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if n := len(a.SubAgents()); n != 0 {
				t.Errorf("%s has %d sub-agents, want 0", tc.name, n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Condition extractors (ADR-197 M-A)
// ---------------------------------------------------------------------------

func TestEvaluatorConditions_surfaceTheModeAndTheDiscriminants(t *testing.T) {
	state := evaluateState()
	state["prior_moderator_feedback"] = "Criterion c1 was scored above what the answer supports."

	got := EvaluatorConditions(newFakeState(state))

	for k, want := range map[string]string{
		"mode":                         "evaluate",
		"attempt_index":                "1",
		"subject":                      "biology",
		"has_prior_moderator_feedback": "true",
	} {
		if got[k] != want {
			t.Errorf("condition %q = %q, want %q", k, got[k], want)
		}
	}
	// Conditions are the prompt-shaping discriminants, never the learner
	// content itself. O+ renders these, so a leak here is a governance defect.
	for k, v := range got {
		if strings.Contains(v, state["learner_answer"].(string)) {
			t.Errorf("condition %q carries the learner answer", k)
		}
	}
}

func TestEvaluatorConditions_assessSummaryReportsItsOwnMode(t *testing.T) {
	got := EvaluatorConditions(newFakeState(summaryState()))
	if got["mode"] != "assess_summary" {
		t.Errorf("mode condition = %q, want %q", got["mode"], "assess_summary")
	}
	if _, ok := got["attempt_index"]; ok {
		t.Errorf("assess_summary reported an attempt_index: it has no moderator loop")
	}
}

func TestEvaluatorConditions_unknownModeYieldsNothing(t *testing.T) {
	state := evaluateState()
	state["mode"] = "grade_harder"
	if got := EvaluatorConditions(newFakeState(state)); got != nil {
		t.Errorf("conditions = %v for an invalid mode, want nil: the turn is aborting anyway", got)
	}
}

func TestModeratorConditions_surfaceTheAttemptAndSubject(t *testing.T) {
	got := ModeratorConditions(newFakeState(moderatorState()))
	if got["attempt_index"] != "1" {
		t.Errorf("attempt_index = %q, want %q", got["attempt_index"], "1")
	}
	if got["subject"] != "biology" {
		t.Errorf("subject = %q, want %q", got["subject"], "biology")
	}
}

func TestModeratorConditions_missingEvaluationYieldsNothing(t *testing.T) {
	state := moderatorState()
	delete(state, "evaluation_json")
	if got := ModeratorConditions(newFakeState(state)); got != nil {
		t.Errorf("conditions = %v with nothing to judge, want nil", got)
	}
}
