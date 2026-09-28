package tests

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

type forkRPCExec struct {
	methods     []string
	rollbackErr error
	forkErr     error
	legacy      bool
	omitTurns   bool
	turns       string
	params      map[string]map[string]any
}

func (e *forkRPCExec) Run(codexsdk.CodexExecArgs) <-chan codexsdk.ExecResult {
	return make(chan codexsdk.ExecResult)
}

func (e *forkRPCExec) RPCCall(_ context.Context, method string, params interface{}) (json.RawMessage, error) {
	e.methods = append(e.methods, method)
	if e.params == nil {
		e.params = make(map[string]map[string]any)
	}
	e.params[method], _ = params.(map[string]any)
	switch method {
	case "thread/fork":
		if e.forkErr != nil {
			return nil, e.forkErr
		}
		var turns []codexForkTurn
		if err := json.Unmarshal([]byte(e.turns), &turns); err != nil {
			return nil, err
		}
		if !e.legacy {
			for i, turn := range turns {
				if turn.ID == e.params[method]["lastTurnId"] {
					turns = turns[:i+1]
					break
				}
			}
		}
		if e.omitTurns {
			turns = nil
		}
		return json.Marshal(map[string]any{"thread": map[string]any{"id": "forked", "turns": turns}})
	case "thread/read":
		return json.RawMessage(`{"thread":{"turns":` + e.turns + `}}`), nil
	case "thread/rollback":
		return json.RawMessage(`{}`), e.rollbackErr
	default:
		return nil, errors.New("unexpected RPC: " + method)
	}
}

func TestForkCodexThreadTruncation(t *testing.T) {
	turns := `[{"id":"turn-1","items":[{"type":"userMessage"}]},{"id":"turn-2","items":[{"type":"user_message"}]},{"id":"turn-3","items":[{"type":"message","role":"user"}]}]`
	for _, tc := range []struct {
		name                 string
		ordinal              int
		legacy               bool
		rollbackErr, forkErr error
		methods              []string
		wantErr              bool
	}{
		{"legacy ignores lastTurnId", 1, true, nil, nil, []string{"thread/read", "thread/fork", "thread/rollback"}, false},
		{"current", 1, false, nil, nil, []string{"thread/read", "thread/fork"}, false},
		{"latest turn", 3, false, nil, nil, []string{"thread/read", "thread/fork"}, false},
		{"legacy latest turn", 3, true, nil, nil, []string{"thread/read", "thread/fork"}, false},
		{"rollback failure", 1, true, errors.New("permission denied"), nil, []string{"thread/read", "thread/fork", "thread/rollback"}, true},
		{"fork failure does not retry", 1, false, nil, errors.New("turn is in progress"), []string{"thread/read", "thread/fork"}, true},
		{"invalid ordinal", -1, false, nil, nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &forkRPCExec{turns: turns, legacy: tc.legacy, rollbackErr: tc.rollbackErr, forkErr: tc.forkErr}
			client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
			id, err := forkThreadID(context.Background(), client, codexsdk.ThreadOptions{}, "source", &tc.ordinal)
			if (err != nil) != tc.wantErr {
				t.Fatalf("id=%q err=%v", id, err)
			}
			if !tc.wantErr && id != "forked" {
				t.Fatalf("id=%q", id)
			}
			if !reflect.DeepEqual(exec.methods, tc.methods) {
				t.Fatalf("methods=%v, want %v", exec.methods, tc.methods)
			}
			if params := exec.params["thread/rollback"]; params != nil {
				if params["threadId"] != "forked" || params["numTurns"] != 2 {
					t.Fatalf("rollback params=%v", params)
				}
			}
			if params := exec.params["thread/fork"]; params != nil {
				wantTurn := "turn-1"
				if tc.ordinal == 3 {
					wantTurn = "turn-3"
				}
				if params["threadId"] != "source" || params["lastTurnId"] != wantTurn {
					t.Fatalf("fork params=%v", params)
				}
			}
		})
	}
}

func TestForkCodexThreadRejectsMissingHistory(t *testing.T) {
	for _, turns := range []string{`[]`} {
		exec := &forkRPCExec{turns: turns}
		client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
		ordinal := 1
		if _, err := forkThreadID(context.Background(), client, codexsdk.ThreadOptions{}, "source", &ordinal); err == nil {
			t.Fatal("expected error for incomplete history")
		}
	}
}

func TestForkCodexThreadReadsOmittedForkHistory(t *testing.T) {
	exec := &forkRPCExec{turns: `[{"id":"turn-1","items":[{"type":"userMessage"}]}]`, omitTurns: true}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	ordinal := 1
	if _, err := forkThreadID(context.Background(), client, codexsdk.ThreadOptions{}, "source", &ordinal); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exec.methods, []string{"thread/read", "thread/fork", "thread/read"}) {
		t.Fatal(exec.methods)
	}
	if exec.params["thread/read"]["threadId"] != "forked" {
		t.Fatal("must hydrate fork, not source")
	}
}

func TestForkCodexThreadPreservesOptions(t *testing.T) {
	exec := &forkRPCExec{turns: `[{"id":"turn-1","items":[{"type":"userMessage"}]}]`}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	ordinal := 1
	_, err := forkThreadID(context.Background(), client, codexsdk.ThreadOptions{
		Model: "gpt-6-astra", ModelProvider: "provider", WorkingDirectory: "/project",
		DeveloperInstructions: "instructions", ApprovalPolicy: codexsdk.ApprovalModeNever,
		SandboxMode: codexsdk.SandboxModeFullAccess, ModelReasoningEffort: "high", FastService: "off",
	}, "source", &ordinal)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"threadId": "source", "lastTurnId": "turn-1", "model": "gpt-6-astra", "modelProvider": "provider",
		"cwd": "/project", "developerInstructions": "instructions", "approvalPolicy": "never",
		"sandbox": "danger-full-access", "serviceTier": nil, "config": map[string]any{"model_reasoning_effort": "high"},
	}
	if !reflect.DeepEqual(exec.params["thread/fork"], want) {
		t.Fatalf("params=%v", exec.params["thread/fork"])
	}
}

func TestForkCodexThreadWithoutBoundary(t *testing.T) {
	exec := &forkRPCExec{turns: `[]`}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	id, err := forkThreadID(context.Background(), client, codexsdk.ThreadOptions{}, "source", nil)
	if err != nil || id != "forked" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if !reflect.DeepEqual(exec.methods, []string{"thread/fork"}) {
		t.Fatal(exec.methods)
	}
}

func TestForkThreadLastTurnID(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		exec := &forkRPCExec{legacy: legacy, turns: `[{"id":"turn-1","items":[]},{"id":"turn-2","items":[]}]`}
		client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
		thread, err := client.ForkThread(context.Background(), "source", codexsdk.ThreadForkOptions{LastTurnID: " turn-1 "})
		if err != nil || thread == nil || thread.ID() == nil || *thread.ID() != "forked" {
			t.Fatalf("thread=%v err=%v", thread, err)
		}
		want := []string{"thread/fork"}
		if legacy {
			want = append(want, "thread/rollback")
		}
		if !reflect.DeepEqual(exec.methods, want) {
			t.Fatalf("methods=%v, want %v", exec.methods, want)
		}
		if exec.params["thread/fork"]["lastTurnId"] != "turn-1" {
			t.Fatal(exec.params)
		}
		if legacy && exec.params["thread/rollback"]["numTurns"] != 1 {
			t.Fatal(exec.params)
		}
	}
}

func TestForkThreadRejectsConflictingBoundaries(t *testing.T) {
	exec := &forkRPCExec{}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	ordinal := 1
	_, err := client.ForkThread(context.Background(), "source", codexsdk.ThreadForkOptions{LastTurnID: "turn-1", TruncateBeforeNthUserMessage: &ordinal})
	if err == nil || len(exec.methods) != 0 {
		t.Fatalf("err=%v calls=%v", err, exec.methods)
	}
}

func TestForkThreadRejectsMissingBoundary(t *testing.T) {
	exec := &forkRPCExec{turns: `[{"id":"other","items":[]}]`}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	_, err := client.ForkThread(context.Background(), "source", codexsdk.ThreadForkOptions{LastTurnID: "turn-1"})
	if err == nil {
		t.Fatal("must not accept an unrelated fork history")
	}
	if !reflect.DeepEqual(exec.methods, []string{"thread/fork"}) {
		t.Fatal(exec.methods)
	}
}

func TestForkThreadZeroOrdinalRetainsLegacyBehavior(t *testing.T) {
	exec := &forkRPCExec{legacy: true, turns: `[{"id":"turn-1","items":[{"type":"userMessage"}]}]`}
	client := codexsdk.NewCodexWithExec(exec, codexsdk.CodexOptions{})
	ordinal := 0
	_, err := client.ForkThread(context.Background(), "source", codexsdk.ThreadForkOptions{TruncateBeforeNthUserMessage: &ordinal})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exec.methods, []string{"thread/fork", "thread/rollback"}) {
		t.Fatal(exec.methods)
	}
	if exec.params["thread/rollback"]["numTurns"] != 1 {
		t.Fatal(exec.params)
	}
}

type codexForkTurn struct {
	ID    string            `json:"id"`
	Items []json.RawMessage `json:"items"`
}

func forkThreadID(ctx context.Context, client *codexsdk.Codex, opts codexsdk.ThreadOptions, source string, ordinal *int) (string, error) {
	thread, err := client.ForkThread(ctx, source, codexsdk.ThreadForkOptions{ThreadOptions: opts, TruncateBeforeNthUserMessage: ordinal})
	if err != nil {
		return "", err
	}
	if thread == nil || thread.ID() == nil {
		return "", errors.New("missing fork id")
	}
	return *thread.ID(), nil
}
