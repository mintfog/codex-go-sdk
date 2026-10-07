package codex

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/fanwenlin/codex-go-sdk/types"
)

const captureReasoningArgsEnv = "CODEX_TEST_CAPTURE_REASONING_ARGS"

// CodexExec supplies CLI arguments directly, so intercept its helper subprocess
// before the testing package tries to parse them as test flags.
func TestMain(m *testing.M) {
	if os.Getenv(captureReasoningArgsEnv) == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		if err := json.NewEncoder(os.Stdout).Encode(os.Args[1:]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var reasoningEffortCases = []struct {
	name, model, effort, want string
}{
	{"default/max", "", " max ", "max"},
	{"default/ultra", "", " ultra ", "ultra"},
	{"gpt-6.1-sol/max", "gpt-6.1-sol", " max ", "max"},
	{"gpt-6.1-sol/ultra", "gpt-6.1-sol", " ultra ", "ultra"},
	{"gpt-6-sol/max", "gpt-6-sol", " max ", "max"},
	{"gpt-6-sol/ultra", "gpt-6-sol", " ultra ", "ultra"},
	{"gpt-6-astra/max", "gpt-6-astra", " max ", "max"},
	{"gpt-6-astra/ultra", "gpt-6-astra", " ultra ", "ultra"},
	{"gpt-6-astra-latest/max", "gpt-6-astra-latest", " max ", "max"},
	{"gpt-6-astra-latest/ultra", "gpt-6-astra-latest", " ultra ", "ultra"},
	{"gpt-6-luna/max", "gpt-6-luna", " max ", "max"},
	{"gpt-6-luna/ultra", "gpt-6-luna", " ultra ", "ultra"},
	{"future-alias/max", "my-next-model", " max ", "max"},
	{"future-alias/ultra", "my-next-model", " ultra ", "ultra"},
	{"legacy-name-suffix/max", "gpt-5.3-codex-latest", " max ", "max"},
	{"legacy-name-suffix/ultra", "gpt-5.3-codex-latest", " ultra ", "ultra"},
	{"provider-alias/max", "custom/gpt-5.3-codex", " max ", "max"},
	{"provider-alias/ultra", "custom/gpt-5.3-codex", " ultra ", "ultra"},
	{"legacy/max", "gpt-5.3-codex", " max ", "xhigh"},
	{"legacy/ultra", "gpt-5.3-codex", " ultra ", "xhigh"},
}

var collaborationReasoningCases = []struct {
	name                  string
	model, effort         string
	modeModel, modeEffort string
	wantTurn, wantMode    string
}{
	{"default/inherited/max", "", " max ", "", "", "max", "max"},
	{"default/explicit/max", "", " max ", "", " max ", "max", "max"},
	{"default/inherited/ultra", "", " ultra ", "", "", "ultra", "ultra"},
	{"default/explicit/ultra", "", " ultra ", "", " ultra ", "ultra", "ultra"},
	{"gpt-6.1-sol/inherited/max", "gpt-6.1-sol", " max ", "", "", "max", "max"},
	{"gpt-6.1-sol/explicit/max", "gpt-6.1-sol", " max ", "gpt-6.1-sol", " max ", "max", "max"},
	{"gpt-6.1-sol/inherited/ultra", "gpt-6.1-sol", " ultra ", "", "", "ultra", "ultra"},
	{"gpt-6.1-sol/explicit/ultra", "gpt-6.1-sol", " ultra ", "gpt-6.1-sol", " ultra ", "ultra", "ultra"},
	{"gpt-6-sol/inherited/max", "gpt-6-sol", " max ", "", "", "max", "max"},
	{"gpt-6-sol/explicit/max", "gpt-6-sol", " max ", "gpt-6-sol", " max ", "max", "max"},
	{"gpt-6-sol/inherited/ultra", "gpt-6-sol", " ultra ", "", "", "ultra", "ultra"},
	{"gpt-6-sol/explicit/ultra", "gpt-6-sol", " ultra ", "gpt-6-sol", " ultra ", "ultra", "ultra"},
	{"gpt-6-astra/inherited/max", "gpt-6-astra", " max ", "", "", "max", "max"},
	{"gpt-6-astra/explicit/max", "gpt-6-astra", " max ", "gpt-6-astra", " max ", "max", "max"},
	{"gpt-6-astra/inherited/ultra", "gpt-6-astra", " ultra ", "", "", "ultra", "ultra"},
	{"gpt-6-astra/explicit/ultra", "gpt-6-astra", " ultra ", "gpt-6-astra", " ultra ", "ultra", "ultra"},
	{"gpt-6-astra-latest/inherited/max", "gpt-6-astra-latest", " max ", "", "", "max", "max"},
	{"gpt-6-astra-latest/explicit/max", "gpt-6-astra-latest", " max ", "gpt-6-astra-latest", " max ", "max", "max"},
	{"gpt-6-astra-latest/inherited/ultra", "gpt-6-astra-latest", " ultra ", "", "", "ultra", "ultra"},
	{"gpt-6-astra-latest/explicit/ultra", "gpt-6-astra-latest", " ultra ", "gpt-6-astra-latest", " ultra ", "ultra", "ultra"},
	{"gpt-6-luna/inherited/max", "gpt-6-luna", " max ", "", "", "max", "max"},
	{"gpt-6-luna/explicit/max", "gpt-6-luna", " max ", "gpt-6-luna", " max ", "max", "max"},
	{"gpt-6-luna/inherited/ultra", "gpt-6-luna", " ultra ", "", "", "ultra", "ultra"},
	{"gpt-6-luna/explicit/ultra", "gpt-6-luna", " ultra ", "gpt-6-luna", " ultra ", "ultra", "ultra"},
	{"future-alias/inherited/max", "my-next-model", " max ", "", "", "max", "max"},
	{"future-alias/explicit/max", "my-next-model", " max ", "my-next-model", " max ", "max", "max"},
	{"future-alias/inherited/ultra", "my-next-model", " ultra ", "", "", "ultra", "ultra"},
	{"future-alias/explicit/ultra", "my-next-model", " ultra ", "my-next-model", " ultra ", "ultra", "ultra"},
	{"legacy-name-suffix/inherited/max", "gpt-5.3-codex-latest", " max ", "", "", "max", "max"},
	{"legacy-name-suffix/explicit/max", "gpt-5.3-codex-latest", " max ", "gpt-5.3-codex-latest", " max ", "max", "max"},
	{"legacy-name-suffix/inherited/ultra", "gpt-5.3-codex-latest", " ultra ", "", "", "ultra", "ultra"},
	{"legacy-name-suffix/explicit/ultra", "gpt-5.3-codex-latest", " ultra ", "gpt-5.3-codex-latest", " ultra ", "ultra", "ultra"},
	{"provider-alias/inherited/max", "custom/gpt-5.3-codex", " max ", "", "", "max", "max"},
	{"provider-alias/explicit/max", "custom/gpt-5.3-codex", " max ", "custom/gpt-5.3-codex", " max ", "max", "max"},
	{"provider-alias/inherited/ultra", "custom/gpt-5.3-codex", " ultra ", "", "", "ultra", "ultra"},
	{"provider-alias/explicit/ultra", "custom/gpt-5.3-codex", " ultra ", "custom/gpt-5.3-codex", " ultra ", "ultra", "ultra"},
	{"legacy/inherited/max", "gpt-5.3-codex", " max ", "", "", "xhigh", "xhigh"},
	{"legacy/explicit/max", "gpt-5.3-codex", " max ", "gpt-5.3-codex", " max ", "xhigh", "xhigh"},
	{"legacy/inherited/ultra", "gpt-5.3-codex", " ultra ", "", "", "xhigh", "xhigh"},
	{"legacy/explicit/ultra", "gpt-5.3-codex", " ultra ", "gpt-5.3-codex", " ultra ", "xhigh", "xhigh"},
	{"legacy-to-new/inherited", "gpt-5.3-codex", " ultra ", "gpt-6.1-sol", "", "xhigh", "ultra"},
	{"legacy-to-new/explicit", "gpt-5.3-codex", " ultra ", "gpt-6.1-sol", " ultra ", "xhigh", "ultra"},
	{"new-to-legacy/inherited", "gpt-6.1-sol", " ultra ", "gpt-5.3-codex", "", "ultra", "xhigh"},
	{"new-to-legacy/explicit", "gpt-6.1-sol", " ultra ", "gpt-5.3-codex", " ultra ", "ultra", "xhigh"},
}

func TestReasoningEffortTurnParameters(t *testing.T) {
	exec := NewAppServerExec("test-codex", nil, nil, types.ClientInfo{}, "", "")
	for _, tc := range reasoningEffortCases {
		t.Run(tc.name, func(t *testing.T) {
			args := CodexExecArgs{Model: tc.model, ModelReasoningEffort: tc.effort}
			params, err := exec.buildTurnParams("thread-1", args)
			if err != nil {
				t.Fatal(err)
			}
			assertParam(t, params, "effort", tc.want)
		})
	}
}

func TestReasoningEffortTurnCollaborationParameters(t *testing.T) {
	exec := NewAppServerExec("test-codex", nil, nil, types.ClientInfo{}, "", "")
	for _, tc := range collaborationReasoningCases {
		t.Run(tc.name, func(t *testing.T) {
			mode := reasoningCollaborationMode(tc.modeModel, tc.modeEffort)
			original := mode.Settings
			args := CodexExecArgs{
				Model: tc.model, ModelReasoningEffort: tc.effort, CollaborationMode: mode,
			}
			params, err := exec.buildTurnParams("thread-1", args)
			if err != nil {
				t.Fatal(err)
			}
			assertParam(t, params, "effort", tc.wantTurn)
			assertReasoningModeParam(t, params, tc.wantMode)
			assertReasoningModeUnchanged(t, mode, original, tc.modeEffort)
		})
	}
}

func TestReasoningEffortThreadStartParameters(t *testing.T) {
	for _, tc := range collaborationReasoningCases {
		t.Run(tc.name, func(t *testing.T) {
			mode := reasoningCollaborationMode(tc.modeModel, tc.modeEffort)
			original := mode.Settings
			args := CodexExecArgs{
				Model: tc.model, ModelReasoningEffort: tc.effort, CollaborationMode: mode,
			}
			params := buildThreadStartParams(args)
			assertReasoningModeParam(t, params, tc.wantMode)
			assertReasoningModeUnchanged(t, mode, original, tc.modeEffort)
		})
	}
}

func TestReasoningEffortThreadResumeParameters(t *testing.T) {
	for _, tc := range collaborationReasoningCases {
		t.Run(tc.name, func(t *testing.T) {
			mode := reasoningCollaborationMode(tc.modeModel, tc.modeEffort)
			original := mode.Settings
			args := CodexExecArgs{
				Model: tc.model, ModelReasoningEffort: tc.effort, CollaborationMode: mode,
			}
			params := buildThreadResumeParams("thread-1", args, "openai")
			assertReasoningModeParam(t, params, tc.wantMode)
			assertReasoningModeUnchanged(t, mode, original, tc.modeEffort)
		})
	}
}

func TestReasoningEffortForkParameters(t *testing.T) {
	for _, tc := range reasoningEffortCases {
		t.Run(tc.name, func(t *testing.T) {
			options := types.ThreadForkOptions{ThreadOptions: types.ThreadOptions{
				Model: tc.model, ModelReasoningEffort: types.ModelReasoningEffort(tc.effort),
			}}
			params := buildThreadForkParams("thread-1", options)
			config, ok := params["config"].(map[string]interface{})
			if !ok {
				t.Fatalf("config has type %T", params["config"])
			}
			assertParam(t, config, "model_reasoning_effort", tc.want)
		})
	}
}

func TestReasoningEffortCLIParameters(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, want string
	}{
		{"gpt-6.1-sol/max", "gpt-6.1-sol", " max ", "max"},
		{"gpt-6.1-sol/ultra", "gpt-6.1-sol", " ultra ", "ultra"},
		{"gpt-6-astra-latest/max", "gpt-6-astra-latest", " max ", "max"},
		{"gpt-6-astra-latest/ultra", "gpt-6-astra-latest", " ultra ", "ultra"},
		{"future-alias/max", "my-next-model", " max ", "max"},
		{"future-alias/ultra", "my-next-model", " ultra ", "ultra"},
		{"legacy-name-suffix/max", "gpt-5.3-codex-latest", " max ", "max"},
		{"legacy-name-suffix/ultra", "gpt-5.3-codex-latest", " ultra ", "ultra"},
		{"legacy/max", "gpt-5.3-codex", " max ", "xhigh"},
		{"legacy/ultra", "gpt-5.3-codex", " ultra ", "xhigh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := CodexExecArgs{Input: "test", Model: tc.model, ModelReasoningEffort: tc.effort}
			captured := captureReasoningCLIArgs(t, args)
			assertReasoningCLIConfig(t, captured, tc.want)
		})
	}
}

func TestReasoningEffortCLIIgnoresCollaborationMode(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, modeModel, want string
	}{
		{"new-model/max", "gpt-6.1-sol", " max ", "gpt-5.3-codex", "max"},
		{"new-model/ultra", "gpt-6.1-sol", " ultra ", "gpt-5.3-codex", "ultra"},
		{"legacy-model/max", "gpt-5.3-codex", " max ", "gpt-6.1-sol", "xhigh"},
		{"legacy-model/ultra", "gpt-5.3-codex", " ultra ", "gpt-6.1-sol", "xhigh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := reasoningCollaborationMode(tc.modeModel, "")
			original := mode.Settings
			args := CodexExecArgs{
				Input: "test", Model: tc.model, ModelReasoningEffort: tc.effort, CollaborationMode: mode,
			}
			captured := captureReasoningCLIArgs(t, args)
			assertReasoningCLIConfig(t, captured, tc.want)
			assertReasoningModeUnchanged(t, mode, original, "")
		})
	}
}

func TestReasoningEffortEnsureThreadUsesCollaborationModel(t *testing.T) {
	threadID := "thread-1"
	for _, tc := range []struct {
		name       string
		threadID   *string
		wantMethod string
	}{
		{"start", nil, "thread/start"},
		{"resume", &threadID, "thread/resume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := NewAppServerExec("test-codex", nil, nil, types.ClientInfo{}, "", "")
			recorder := &reasoningEffortRPCRecorder{exec: exec}
			exec.stdin = recorder
			mode := reasoningCollaborationMode("gpt-6.1-sol", "")
			original := mode.Settings
			args := CodexExecArgs{
				ThreadId: tc.threadID, Model: "gpt-5.3-codex", ModelProvider: "openai",
				ModelReasoningEffort: " ultra ", CollaborationMode: mode,
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, _, err := exec.ensureThread(ctx, args); err != nil {
				t.Fatal(err)
			}
			if recorder.method != tc.wantMethod {
				t.Fatalf("RPC method = %q, want %q", recorder.method, tc.wantMethod)
			}
			assertReasoningModeParam(t, map[string]interface{}{"collaborationMode": recorder.mode}, "ultra")
			assertReasoningModeUnchanged(t, mode, original, "")
		})
	}
}

func TestReasoningEffortNilCollaborationMode(t *testing.T) {
	if got := buildCollaborationMode(nil, "gpt-6.1-sol", "ultra"); got != nil {
		t.Fatalf("nil collaboration mode became %+v", got)
	}
}

func TestReasoningEffortCollaborationOptionalEffort(t *testing.T) {
	empty := types.ModelReasoningEffort("")
	blank := types.ModelReasoningEffort(" ")
	for _, tc := range []struct {
		name, parentEffort string
		explicit, want     *types.ModelReasoningEffort
	}{
		{"nil-with-empty-parent", "", nil, nil},
		{"nil-with-blank-parent", " ", nil, nil},
		{"explicit-empty", "ultra", &empty, &empty},
		{"explicit-blank", "ultra", &blank, &empty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := types.NewCollaborationMode(types.CollaborationModePlan)
			mode.Settings.Model = "gpt-6.1-sol"
			mode.Settings.ReasoningEffort = tc.explicit
			original := mode.Settings
			var originalEffort string
			if tc.explicit != nil {
				originalEffort = string(*tc.explicit)
			}
			next := buildCollaborationMode(mode, "gpt-6.1-sol", tc.parentEffort)
			assertReasoningModeUnchanged(t, mode, original, originalEffort)
			gotIsNil := next.Settings.ReasoningEffort == nil
			wantIsNil := tc.want == nil
			if gotIsNil != wantIsNil {
				t.Fatalf("effort pointer is nil: got %t, want %t", gotIsNil, wantIsNil)
			}
			if !gotIsNil && *next.Settings.ReasoningEffort != *tc.want {
				t.Fatalf("effort = %q, want %q", *next.Settings.ReasoningEffort, *tc.want)
			}
		})
	}
}

func reasoningCollaborationMode(model, effort string) *types.CollaborationMode {
	mode := types.NewCollaborationMode(types.CollaborationModePlan)
	mode.Settings.Model = model
	if effort != "" {
		value := types.ModelReasoningEffort(effort)
		mode.Settings.ReasoningEffort = &value
	}
	return mode
}

func captureReasoningCLIArgs(t *testing.T, args CodexExecArgs) []string {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args.Context = ctx
	exec := NewCodexExec(testBinary, map[string]string{captureReasoningArgsEnv: "1"})
	var captured []string
	for result := range exec.Run(args) {
		if result.Error != nil {
			t.Fatal(result.Error)
		}
		if err := json.Unmarshal([]byte(result.Line), &captured); err != nil {
			t.Fatalf("decode helper output %q: %v", result.Line, err)
		}
	}
	return captured
}

func assertReasoningCLIConfig(t *testing.T, captured []string, want string) {
	t.Helper()
	config := `model_reasoning_effort="` + want + `"`
	index := slices.Index(captured, config)
	if index < 1 || captured[index-1] != "--config" {
		t.Fatalf("CLI arguments = %q, want --config %q", captured, config)
	}
}

func assertReasoningModeParam(t *testing.T, params map[string]interface{}, want string) {
	t.Helper()
	mode, ok := params["collaborationMode"].(*types.CollaborationMode)
	if !ok || mode == nil {
		t.Fatalf("collaborationMode has type %T", params["collaborationMode"])
	}
	if mode.Settings.ReasoningEffort == nil {
		t.Fatalf("collaboration mode effort is nil, want %q", want)
	}
	if got := string(*mode.Settings.ReasoningEffort); got != want {
		t.Fatalf("collaboration mode effort = %q, want %q", got, want)
	}
}

func assertReasoningModeUnchanged(t *testing.T, mode *types.CollaborationMode, original types.CollaborationModeSettings, effort string) {
	t.Helper()
	if mode.Settings != original {
		t.Fatalf("caller's collaboration settings changed: got %+v, want %+v", mode.Settings, original)
	}
	if original.ReasoningEffort != nil && string(*mode.Settings.ReasoningEffort) != effort {
		t.Fatalf("caller's collaboration effort changed: got %q, want %q", *mode.Settings.ReasoningEffort, effort)
	}
}

type reasoningEffortRPCRecorder struct {
	exec   *AppServerExec
	method string
	mode   *types.CollaborationMode
}

func (r *reasoningEffortRPCRecorder) Write(data []byte) (int, error) {
	var request struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params struct {
			CollaborationMode *types.CollaborationMode `json:"collaborationMode"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return 0, err
	}
	r.method = request.Method
	r.mode = request.Params.CollaborationMode
	response, err := json.Marshal(map[string]interface{}{
		"id": request.ID, "result": map[string]interface{}{"thread": map[string]interface{}{"id": "thread-1"}},
	})
	if err != nil {
		return 0, err
	}
	r.exec.handleLine(string(response))
	return len(data), nil
}

func (r *reasoningEffortRPCRecorder) Close() error { return nil }
