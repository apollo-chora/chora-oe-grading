package agent

// RED-first tests for the ADR-197 M-A condition extractors for the OE-grading
// crew (evaluator + moderator). They surface prompt discriminants (mode /
// attempt / subject / prior-feedback presence) — not learner PII — reusing the
// Build*TaskContextFromState builders so they can't drift.

import (
	"fmt"
	"testing"
)

type mapState struct{ m map[string]any }

func (s *mapState) Get(k string) (any, error) {
	if v, ok := s.m[k]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("missing key %q", k)
}

func TestEvaluatorConditions_evaluateMode(t *testing.T) {
	st := &mapState{m: map[string]any{
		"learner_answer":           "the mitochondria...",
		"rubric_json":              `{"criteria":[]}`,
		"attempt_index":            float64(1),
		"subject":                  "biology",
		"prior_moderator_feedback": "address criterion 2",
	}}
	c := EvaluatorConditions(st)
	if c["mode"] != "evaluate" {
		t.Errorf("mode: got %q", c["mode"])
	}
	if c["attempt_index"] != "1" {
		t.Errorf("attempt_index: got %q", c["attempt_index"])
	}
	if c["subject"] != "biology" {
		t.Errorf("subject: got %q", c["subject"])
	}
	if c["has_prior_moderator_feedback"] != "true" {
		t.Errorf("has_prior_moderator_feedback: got %q", c["has_prior_moderator_feedback"])
	}
}

func TestEvaluatorConditions_summaryMode(t *testing.T) {
	st := &mapState{m: map[string]any{
		"mode":           "assess_summary",
		"results_digest": "q1: pass; q2: fail",
		"subject":        "science",
	}}
	c := EvaluatorConditions(st)
	if c["mode"] != "assess_summary" {
		t.Errorf("mode: got %q", c["mode"])
	}
	if c["subject"] != "science" {
		t.Errorf("subject: got %q", c["subject"])
	}
}

func TestEvaluatorConditions_invalidModeReturnsNil(t *testing.T) {
	st := &mapState{m: map[string]any{"mode": "bogus"}}
	if c := EvaluatorConditions(st); c != nil {
		t.Errorf("invalid mode must yield nil; got %v", c)
	}
}

func TestModeratorConditions_basic(t *testing.T) {
	st := &mapState{m: map[string]any{
		"evaluation_json": `{"score":0.8}`,
		"learner_answer":  "...",
		"rubric_json":     `{"criteria":[]}`,
		"attempt_index":   float64(0),
		"subject":         "history",
	}}
	c := ModeratorConditions(st)
	if c["attempt_index"] != "0" {
		t.Errorf("attempt_index: got %q", c["attempt_index"])
	}
	if c["subject"] != "history" {
		t.Errorf("subject: got %q", c["subject"])
	}
}

func TestModeratorConditions_missingEvaluationReturnsNil(t *testing.T) {
	st := &mapState{m: map[string]any{"learner_answer": "x", "rubric_json": "{}"}}
	if c := ModeratorConditions(st); c != nil {
		t.Errorf("missing evaluation_json must yield nil; got %v", c)
	}
}
