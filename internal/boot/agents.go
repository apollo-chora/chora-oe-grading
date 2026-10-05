package boot

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-adk-common/promptstamping"

	oeagent "github.com/apollo-chora/chora-oe-grading/internal/agent"
)

// EvaluatorInstructionProvider returns an llmagent.InstructionProvider that
// re-composes the evaluator's CREATE prompt PER TURN from session.State(),
// branching on the "mode" key. This is the qgen "critic never saw the candidate"
// lesson: a static boot-time compose would never inject the per-request learner
// answer / rubric / digest, so the evaluator would grade nothing. The builders
// fail loud (return an error) when required content is missing, surfacing the
// pipeline bug instead of silently grading an empty answer.
func EvaluatorInstructionProvider() llmagent.InstructionProvider {
	return func(ctx agent.ReadonlyContext) (string, error) {
		state := ctx.ReadonlyState()
		mode, err := oeagent.EvaluatorMode(state)
		if err != nil {
			return "", err
		}
		if mode == oeagent.ModeAssessSummary {
			sctx, err := oeagent.BuildSummaryTaskContextFromState(state)
			if err != nil {
				return "", err
			}
			return oeagent.ComposeSummaryInstruction(sctx), nil
		}
		ectx, err := oeagent.BuildEvaluatorTaskContextFromState(state)
		if err != nil {
			return "", err
		}
		return oeagent.ComposeEvaluateInstruction(ectx), nil
	}
}

// ModeratorInstructionProvider returns an llmagent.InstructionProvider that
// re-composes the moderator's CREATE prompt PER TURN from session.State(),
// injecting the evaluator output to judge (the qgen "critic never saw the
// candidate" lesson). Fails loud when required content is missing.
func ModeratorInstructionProvider() llmagent.InstructionProvider {
	return func(ctx agent.ReadonlyContext) (string, error) {
		mctx, err := oeagent.BuildModeratorTaskContextFromState(ctx.ReadonlyState())
		if err != nil {
			return "", err
		}
		return oeagent.ComposeModeratorInstruction(mctx), nil
	}
}

// NewLLMAgent builds a tool-less llmagent whose instruction is re-composed
// per-turn via the supplied InstructionProvider. Both OE-grading agents are
// tool-free: the content they work on is injected into the instruction from
// session state, because the executor sends only a "BEGIN" trigger as the user
// message. No external calls.
//
// The model arrives as the adkmodel.LLM interface, so the caller owns the
// choice of transport: production passes the chora-model-gateway client built
// in run.go, a test passes a fake.
func NewLLMAgent(name string, m adkmodel.LLM, provider llmagent.InstructionProvider) (agent.Agent, error) {
	a, err := llmagent.New(llmagent.Config{
		Name:                name,
		Model:               m,
		InstructionProvider: provider,
	})
	if err != nil {
		return nil, fmt.Errorf("llmagent.New(%s): %w", name, err)
	}
	return a, nil
}

// EvaluatorConditions adapts the crew's evaluate/summary condition extractor to
// promptstamping.ConditionExtractor. The conditions are the prompt-shaping
// discriminants (mode, attempt, subject, prior-feedback presence), never
// learner PII, so O+ can render which conditions produced a grading decision.
func EvaluatorConditions(s session.ReadonlyState) map[string]string {
	return oeagent.EvaluatorConditions(s)
}

// ModeratorConditions adapts the moderator's condition extractor to
// promptstamping.ConditionExtractor.
func ModeratorConditions(s session.ReadonlyState) map[string]string {
	return oeagent.ModeratorConditions(s)
}

// NewEvaluatorAgent builds the grader of record. ADR-197 M-A: the per-turn
// provider is wrapped with promptstamping, which stamps prompt_version +
// content_hash + conditions (mode / attempt_index / subject /
// has_prior_moderator_feedback) onto the active span. The prompt itself is
// unchanged by the wrapper.
func NewEvaluatorAgent(cfg Config, llm adkmodel.LLM) (agent.Agent, error) {
	return NewLLMAgent(CrewKindEvaluator, llm,
		promptstamping.WithStamping(
			cfg.PromptVersion,
			EvaluatorConditions,
			EvaluatorInstructionProvider(),
		))
}

// NewModeratorAgent builds the judge. ADR-197 M-A: promptstamping stamps
// prompt_version + content_hash + conditions (attempt_index / subject) onto the
// active span. Behaviour-neutral for the composed prompt.
func NewModeratorAgent(cfg Config, llm adkmodel.LLM) (agent.Agent, error) {
	return NewLLMAgent(CrewKindModerator, llm,
		promptstamping.WithStamping(
			cfg.PromptVersion,
			ModeratorConditions,
			ModeratorInstructionProvider(),
		))
}
