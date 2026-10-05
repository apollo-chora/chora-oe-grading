// Test doubles for the boot package. They implement only the ADK surfaces the
// boot wiring actually reads, so a test can exercise an InstructionProvider or
// a plugin callback without booting the ADK runtime, minting an ID token or
// dialling chora-model-gateway.
//
// Shape borrowed from ../../../qgen_adk_go/cmd/qgen_question/main_test.go so
// the two crews stay symmetric.
package boot

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
)

// ---------------------------------------------------------------------------
// Session state
// ---------------------------------------------------------------------------

type fakeState struct {
	data map[string]any
}

func newFakeState(data map[string]any) *fakeState {
	if data == nil {
		data = map[string]any{}
	}
	return &fakeState{data: data}
}

func (f *fakeState) Get(k string) (any, error) {
	v, ok := f.data[k]
	if !ok {
		return nil, fmt.Errorf("key %q not found", k)
	}
	return v, nil
}

func (f *fakeState) Set(k string, v any) error {
	f.data[k] = v
	return nil
}

func (f *fakeState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range f.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

var (
	_ session.ReadonlyState = (*fakeState)(nil)
	_ session.State         = (*fakeState)(nil)
)

// ---------------------------------------------------------------------------
// Contexts
// ---------------------------------------------------------------------------

type fakeReadonlyContext struct {
	context.Context
	state session.ReadonlyState
}

func (f *fakeReadonlyContext) UserContent() *genai.Content { return nil }
func (f *fakeReadonlyContext) InvocationID() string        { return "test-invocation" }
func (f *fakeReadonlyContext) AgentName() string           { return "oe_test_agent" }
func (f *fakeReadonlyContext) ReadonlyState() session.ReadonlyState {
	return f.state
}
func (f *fakeReadonlyContext) UserID() string    { return "test-user" }
func (f *fakeReadonlyContext) AppName() string   { return "oe_grading" }
func (f *fakeReadonlyContext) SessionID() string { return "test-session" }
func (f *fakeReadonlyContext) Branch() string    { return "" }

var _ agent.ReadonlyContext = (*fakeReadonlyContext)(nil)

func newFakeCtx(state map[string]any) *fakeReadonlyContext {
	return &fakeReadonlyContext{
		Context: context.Background(),
		state:   newFakeState(state),
	}
}

// fakeCallbackContext adds the mutable-state + artifacts surface that
// agent.CallbackContext requires, which is what a BeforeAgentCallback receives.
type fakeCallbackContext struct {
	*fakeReadonlyContext
	state *fakeState
}

func (f *fakeCallbackContext) State() session.State       { return f.state }
func (f *fakeCallbackContext) Artifacts() agent.Artifacts { return noopArtifacts{} }

var _ agent.CallbackContext = (*fakeCallbackContext)(nil)

func newFakeCallbackCtx(state map[string]any) *fakeCallbackContext {
	s := newFakeState(state)
	return &fakeCallbackContext{
		fakeReadonlyContext: &fakeReadonlyContext{Context: context.Background(), state: s},
		state:               s,
	}
}

// noopArtifacts satisfies agent.Artifacts. The inbound-trace callback never
// touches artifacts, so every method refuses rather than pretending to work:
// a silent empty response here would hide a future callback that started
// reading artifacts without a real store behind it.
type noopArtifacts struct{}

var errNoArtifacts = errors.New("fake: artifact store not wired for this test")

func (noopArtifacts) Save(context.Context, string, *genai.Part) (*artifact.SaveResponse, error) {
	return nil, errNoArtifacts
}
func (noopArtifacts) List(context.Context) (*artifact.ListResponse, error) {
	return nil, errNoArtifacts
}
func (noopArtifacts) Load(context.Context, string) (*artifact.LoadResponse, error) {
	return nil, errNoArtifacts
}
func (noopArtifacts) LoadVersion(context.Context, string, int) (*artifact.LoadResponse, error) {
	return nil, errNoArtifacts
}

// ---------------------------------------------------------------------------
// adkmodel.LLM
// ---------------------------------------------------------------------------

// fakeLLM is the injection seam that makes agent construction testable. In
// production the same slot holds the chora-model-gateway client built in
// run.go; nothing here ever ships in a non-test binary.
type fakeLLM struct {
	name string
}

func (f fakeLLM) Name() string { return f.name }

func (f fakeLLM) GenerateContent(context.Context, *adkmodel.LLMRequest, bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(nil, errors.New("fakeLLM: GenerateContent must not be called by a boot-wiring test"))
	}
}

var _ adkmodel.LLM = fakeLLM{}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// agent.Agent
// ---------------------------------------------------------------------------

// fakeRoot is a root agent that is wired but never run: the subscriber-only
// boot tests assert the lane identity around it, not its behaviour.
type fakeRoot struct{ agent.Agent }

func (fakeRoot) Name() string { return "oe_fake_root" }
