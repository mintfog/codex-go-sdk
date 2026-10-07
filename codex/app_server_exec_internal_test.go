package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fanwenlin/codex-go-sdk/types"
)

func TestIsExpectedReadCloseError(t *testing.T) {
	for _, err := range []error{nil, os.ErrClosed, io.ErrClosedPipe, &os.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}} {
		if !isExpectedReadCloseError(err) {
			t.Fatalf("error %v should be treated as an expected stream close", err)
		}
	}
	if isExpectedReadCloseError(errors.New("unexpected read failure")) {
		t.Fatal("unexpected read failure was suppressed")
	}
}

type captureWriteCloser struct {
	bytes.Buffer
}

func (c *captureWriteCloser) Close() error {
	return nil
}

func TestBuildThreadStartParamsIncludesPermissions(t *testing.T) {
	params := buildThreadStartParams(CodexExecArgs{
		Model:                 "test-model",
		ModelProvider:         "openai",
		SandboxMode:           string(types.SandboxModeFullAccess),
		ApprovalPolicy:        string(types.ApprovalModeNever),
		WorkingDirectory:      "/tmp/project",
		DeveloperInstructions: "MindFS instructions",
		FastService:           "on",
	})

	assertParam(t, params, "model", "test-model")
	assertParam(t, params, "modelProvider", "openai")
	assertParam(t, params, "sandbox", "danger-full-access")
	assertParam(t, params, "approvalPolicy", "never")
	assertParam(t, params, "cwd", "/tmp/project")
	assertParam(t, params, "developerInstructions", "MindFS instructions")
	assertParam(t, params, "serviceTier", "fast")
}

func TestBuildThreadResumeParamsIncludesPermissions(t *testing.T) {
	params := buildThreadResumeParams("thread-1", CodexExecArgs{
		Model:                 "test-model",
		SandboxMode:           string(types.SandboxModeWorkspaceWrite),
		ApprovalPolicy:        string(types.ApprovalModeOnRequest),
		WorkingDirectory:      "/tmp/project",
		DeveloperInstructions: "MindFS instructions",
	}, "openai")

	assertParam(t, params, "threadId", "thread-1")
	assertParam(t, params, "model", "test-model")
	assertParam(t, params, "modelProvider", "openai")
	assertParam(t, params, "sandbox", "workspace-write")
	assertParam(t, params, "approvalPolicy", "on-request")
	assertParam(t, params, "cwd", "/tmp/project")
	assertParam(t, params, "developerInstructions", "MindFS instructions")
}

func TestBuildThreadForkParamsIncludesDeveloperInstructions(t *testing.T) {
	params := buildThreadForkParams("thread-1", types.ThreadForkOptions{
		ThreadOptions: types.ThreadOptions{
			DeveloperInstructions: "MindFS instructions",
		},
	})

	assertParam(t, params, "threadId", "thread-1")
	assertParam(t, params, "developerInstructions", "MindFS instructions")
}

func TestNewAppServerExecPrependsSubcommandToExtraArgs(t *testing.T) {
	exec := NewAppServerExec("codex", []string{"--strict-config"}, nil, types.ClientInfo{}, "", "")
	want := []string{"app-server", "--strict-config"}
	if !reflect.DeepEqual(exec.args, want) {
		t.Fatalf("args = %#v, want %#v", exec.args, want)
	}
}

func TestBuildThreadResumeParamsIncludesCollaborationMode(t *testing.T) {
	params := buildThreadResumeParams("thread-1", CodexExecArgs{
		Model:             "test-model",
		CollaborationMode: types.NewCollaborationMode(types.CollaborationModeDefault),
	}, "openai")

	mode, ok := params["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode param has type %T", params["collaborationMode"])
	}
	if mode.Mode != types.CollaborationModeDefault {
		t.Fatalf("mode: got %q, want %q", mode.Mode, types.CollaborationModeDefault)
	}
	if mode.Settings.Model != "test-model" {
		t.Fatalf("model: got %q", mode.Settings.Model)
	}
}

func TestBuildTurnParamsIncludesCollaborationMode(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	effort := types.ModelReasoningEffortMedium

	params, err := exec.buildTurnParams("thread-1", CodexExecArgs{
		Input:                "plan this",
		Model:                "gpt-5.3-codex",
		ModelReasoningEffort: string(effort),
		CollaborationMode:    types.NewCollaborationMode(types.CollaborationModePlan),
	})
	if err != nil {
		t.Fatalf("buildTurnParams returned error: %v", err)
	}

	mode, ok := params["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode param has type %T", params["collaborationMode"])
	}
	if mode.Mode != types.CollaborationModePlan {
		t.Fatalf("mode: got %q, want %q", mode.Mode, types.CollaborationModePlan)
	}
	if mode.Settings.Model != "gpt-5.3-codex" {
		t.Fatalf("model: got %q", mode.Settings.Model)
	}
	if mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != effort {
		t.Fatalf("reasoning effort: got %v, want %q", mode.Settings.ReasoningEffort, effort)
	}
}

func TestBuildTurnParamsIncludesDefaultCollaborationMode(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")

	params, err := exec.buildTurnParams("thread-1", CodexExecArgs{
		Input:             "resume coding",
		Model:             "gpt-5.3-codex",
		CollaborationMode: types.NewCollaborationMode(types.CollaborationModeDefault),
	})
	if err != nil {
		t.Fatalf("buildTurnParams returned error: %v", err)
	}

	mode, ok := params["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode param has type %T", params["collaborationMode"])
	}
	if mode.Mode != types.CollaborationModeDefault {
		t.Fatalf("mode: got %q, want %q", mode.Mode, types.CollaborationModeDefault)
	}
	if mode.Settings.Model != "gpt-5.3-codex" {
		t.Fatalf("model: got %q", mode.Settings.Model)
	}
	if mode.Settings.ReasoningEffort != nil {
		t.Fatalf("reasoning effort: got %v, want nil", mode.Settings.ReasoningEffort)
	}
}

func TestNormalizeReasoningEffortForModelKeepsMaxUltraForSupportedModels(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-astra"} {
		for _, effort := range []types.ModelReasoningEffort{
			types.ModelReasoningEffortMax,
			types.ModelReasoningEffortUltra,
		} {
			got := normalizeReasoningEffortForModel(model, string(effort))
			if got != string(effort) {
				t.Fatalf("%s ModelReasoningEffort = %q, want %q", model, got, effort)
			}
		}
	}
}

func TestNormalizeReasoningEffortForModelDowngradesMaxUltraForOlderModels(t *testing.T) {
	for _, effort := range []types.ModelReasoningEffort{
		types.ModelReasoningEffortMax,
		types.ModelReasoningEffortUltra,
	} {
		got := normalizeReasoningEffortForModel("gpt-5.3-codex", string(effort))
		if got != string(types.ModelReasoningEffortXHigh) {
			t.Fatalf("ModelReasoningEffort = %q, want %q", got, types.ModelReasoningEffortXHigh)
		}
	}
}

func TestNormalizeReasoningEffortForModelKeepsDefaultModel(t *testing.T) {
	got := normalizeReasoningEffortForModel("", string(types.ModelReasoningEffortUltra))
	if got != string(types.ModelReasoningEffortUltra) {
		t.Fatalf("ModelReasoningEffort = %q, want %q", got, types.ModelReasoningEffortUltra)
	}
}

func TestBuildTurnParamsDowngradesMaxUltraForOlderModels(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")

	params, err := exec.buildTurnParams("thread-1", CodexExecArgs{
		Input:                "code",
		Model:                "gpt-5.3-codex",
		ModelReasoningEffort: string(types.ModelReasoningEffortUltra),
		CollaborationMode:    types.NewCollaborationMode(types.CollaborationModePlan),
	})
	if err != nil {
		t.Fatalf("buildTurnParams returned error: %v", err)
	}
	assertParam(t, params, "effort", "xhigh")
	mode, ok := params["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode param has type %T", params["collaborationMode"])
	}
	if mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != types.ModelReasoningEffortXHigh {
		t.Fatalf("reasoning effort: got %v, want %q", mode.Settings.ReasoningEffort, types.ModelReasoningEffortXHigh)
	}
}

func TestBuildTurnParamsDowngradesExplicitCollaborationModeEffort(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	effort := types.ModelReasoningEffortUltra

	params, err := exec.buildTurnParams("thread-1", CodexExecArgs{
		Input: "code",
		CollaborationMode: &types.CollaborationMode{
			Mode: types.CollaborationModePlan,
			Settings: types.CollaborationModeSettings{
				Model:           "gpt-5.3-codex",
				ReasoningEffort: &effort,
			},
		},
	})
	if err != nil {
		t.Fatalf("buildTurnParams returned error: %v", err)
	}
	mode, ok := params["collaborationMode"].(*types.CollaborationMode)
	if !ok {
		t.Fatalf("collaborationMode param has type %T", params["collaborationMode"])
	}
	if mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != types.ModelReasoningEffortXHigh {
		t.Fatalf("reasoning effort: got %v, want %q", mode.Settings.ReasoningEffort, types.ModelReasoningEffortXHigh)
	}
}

func TestHandleLineDispatchesServerRequestWithID(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	sub := exec.subscribe()
	defer exec.unsubscribe(sub)

	exec.handleLine(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}}`)

	event := <-sub
	if event.ID == nil || *event.ID != 7 {
		t.Fatalf("expected server request id 7, got %v", event.ID)
	}
	if event.Method != "item/commandExecution/requestApproval" {
		t.Fatalf("unexpected method %q", event.Method)
	}
}

func TestHandleLineThreadClosedEvictsKnownThread(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	exec.knownThreads["thread-1"] = struct{}{}

	exec.handleLine(`{"method":"thread/closed","params":{"threadId":"thread-1"}}`)

	exec.knownThreadsMu.Lock()
	_, known := exec.knownThreads["thread-1"]
	exec.knownThreadsMu.Unlock()
	if known {
		t.Fatal("thread/closed did not evict the known thread")
	}
}

func TestForgetKnownThreadIgnoresEmptyID(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	exec.knownThreads["thread-1"] = struct{}{}
	exec.forgetKnownThread("")
	if _, known := exec.knownThreads["thread-1"]; !known {
		t.Fatal("empty thread id changed the known thread cache")
	}
}

func TestApplyThreadUnsubscribeResultEvictsKnownThread(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	exec.knownThreads["thread-1"] = struct{}{}

	response, err := exec.applyThreadUnsubscribeResult("thread-1", json.RawMessage(`{"status":"unsubscribed"}`))
	if err != nil {
		t.Fatalf("applyThreadUnsubscribeResult: %v", err)
	}
	if response.Status != types.ThreadUnsubscribeStatusUnsubscribed {
		t.Fatalf("status = %q", response.Status)
	}
	if _, known := exec.knownThreads["thread-1"]; known {
		t.Fatal("successful thread/unsubscribe did not evict the known thread")
	}
}

func TestSubmitApprovalRespondsToCommandExecutionRequest(t *testing.T) {
	stdin := &captureWriteCloser{}
	exec := &AppServerExec{stdin: stdin}
	requestID := int64(7)
	event := appEvent{
		ID:     &requestID,
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}`),
	}

	called := false
	exec.submitApproval(context.Background(), event, func(req types.ApprovalRequest) (types.ApprovalDecision, error) {
		called = true
		if req.ItemID != "item-1" {
			t.Fatalf("unexpected item id %q", req.ItemID)
		}
		if req.ItemType != "commandExecution" {
			t.Fatalf("unexpected item type %q", req.ItemType)
		}
		return types.ApprovalDecisionApproved, nil
	})

	if !called {
		t.Fatal("approval handler was not called")
	}
	var response struct {
		ID     int64 `json:"id"`
		Result struct {
			Decision string `json:"decision"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if response.ID != 7 {
		t.Fatalf("unexpected response id %d", response.ID)
	}
	if response.Result.Decision != "accept" {
		t.Fatalf("unexpected decision %q", response.Result.Decision)
	}
}

func TestSubmitApprovalRespondsToFileChangeRequest(t *testing.T) {
	stdin := &captureWriteCloser{}
	exec := &AppServerExec{stdin: stdin}
	requestID := int64(9)
	event := appEvent{
		ID:     &requestID,
		Method: "item/fileChange/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-2"}`),
	}

	exec.submitApproval(context.Background(), event, func(req types.ApprovalRequest) (types.ApprovalDecision, error) {
		if req.ItemType != "fileChange" {
			t.Fatalf("unexpected item type %q", req.ItemType)
		}
		return types.ApprovalDecisionRejected, nil
	})

	var response struct {
		ID     int64 `json:"id"`
		Result struct {
			Decision string `json:"decision"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if response.ID != 9 {
		t.Fatalf("unexpected response id %d", response.ID)
	}
	if response.Result.Decision != "decline" {
		t.Fatalf("unexpected decision %q", response.Result.Decision)
	}
}

func TestSubmitAskUserResponseRespondsToRequestUserInput(t *testing.T) {
	stdin := &captureWriteCloser{}
	exec := &AppServerExec{stdin: stdin}
	requestID := int64(11)
	event := appEvent{
		ID:     &requestID,
		Method: "item/tool/requestUserInput",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"call-1","questions":[{"id":"choice","header":"Choose","question":"Pick one","options":[{"label":"A","description":"Use A"}]}]}`),
	}

	called := false
	exec.submitAskUserResponse(event, func(req types.AskUserRequest) (types.AskUserResponse, error) {
		called = true
		if req.ThreadID != "thread-1" {
			t.Fatalf("unexpected thread id %q", req.ThreadID)
		}
		if req.ItemID != "call-1" {
			t.Fatalf("unexpected item id %q", req.ItemID)
		}
		if len(req.Questions) != 1 || req.Questions[0].ID != "choice" {
			t.Fatalf("unexpected questions %#v", req.Questions)
		}
		return types.AskUserResponse{
			Answers: map[string]types.AskUserAnswer{
				"choice": {Answers: []string{"A"}},
			},
		}, nil
	})

	if !called {
		t.Fatal("ask user handler was not called")
	}
	var response struct {
		ID     int64 `json:"id"`
		Result struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if response.ID != 11 {
		t.Fatalf("unexpected response id %d", response.ID)
	}
	got := response.Result.Answers["choice"].Answers
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("unexpected answer %#v", got)
	}
}

func TestGoalContinuationIgnoresOtherThreadTurnBeforeOwnTurn(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	sub := make(chan appEvent, 8)
	output := make(chan ExecResult, 8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- exec.streamGoalContinuation(ctx, "thread-goal", CodexExecArgs{}, nil, sub, output)
	}()

	sub <- appEvent{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"thread-other","turn":{"id":"turn-other"}}`),
	}
	sub <- appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-other","turnId":"turn-other","item":{"id":"msg-other","type":"agentMessage","text":"wrong stream"}}`),
	}
	sub <- appEvent{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"thread-goal","turn":{"id":"turn-goal"}}`),
	}
	sub <- appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal","item":{"id":"msg-goal","type":"agentMessage","text":"right stream"}}`),
	}
	sub <- appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal","usage":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1}}`),
	}

	if err := <-errCh; err != nil {
		t.Fatalf("streamGoalContinuation returned error: %v", err)
	}
	close(output)

	var lines []string
	for result := range output {
		if result.Error != nil {
			t.Fatalf("unexpected output error: %v", result.Error)
		}
		lines = append(lines, result.Line)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 goal-thread lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if bytes.Contains([]byte(line), []byte("thread-other")) || bytes.Contains([]byte(line), []byte("wrong stream")) {
			t.Fatalf("cross-thread event leaked into goal stream: %s", line)
		}
	}
}

func TestGoalContinuationKeepsActiveGoalOpenAcrossPhysicalTurns(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	sub := make(chan appEvent, 16)
	output := make(chan ExecResult, 16)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	activeGoal := &types.ThreadGoal{
		ThreadID:  "thread-goal",
		Objective: "keep working",
		Status:    types.ThreadGoalStatusActive,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- exec.streamGoalContinuation(ctx, "thread-goal", CodexExecArgs{}, activeGoal, sub, output)
	}()

	sub <- appEvent{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"thread-goal","turn":{"id":"turn-goal-1"}}`),
	}
	sub <- appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal-1","item":{"id":"msg-goal-1","type":"agentMessage","text":"first"}}`),
	}
	sub <- appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal-1","usage":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1}}`),
	}

	select {
	case err := <-errCh:
		t.Fatalf("active goal stream ended after first physical turn: %v", err)
	default:
	}

	sub <- appEvent{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"thread-goal","turn":{"id":"turn-goal-2"}}`),
	}
	sub <- appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal-2","item":{"id":"msg-goal-2","type":"agentMessage","text":"second"}}`),
	}
	sub <- appEvent{
		Method: "thread/goal/updated",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal-2","goal":{"threadId":"thread-goal","objective":"keep working","status":"paused","createdAt":1,"updatedAt":2}}`),
	}
	sub <- appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-goal-2","usage":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1}}`),
	}

	if err := <-errCh; err != nil {
		t.Fatalf("streamGoalContinuation returned error: %v", err)
	}
	close(output)

	var lines []string
	for result := range output {
		if result.Error != nil {
			t.Fatalf("unexpected output error: %v", result.Error)
		}
		lines = append(lines, result.Line)
	}
	if len(lines) != 6 {
		t.Fatalf("expected 6 logical lines, got %d: %v", len(lines), lines)
	}
	if bytes.Contains([]byte(strings.Join(lines, "\n")), []byte(`"turnId":"turn-goal-1","type":"turn.completed"`)) {
		t.Fatalf("intermediate active goal completion leaked into logical stream: %v", lines)
	}
}

func TestGoalContinuationDoesNotSynthesizeActiveGoalBeforeDelayedTurn(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	sub := make(chan appEvent, 16)
	output := make(chan ExecResult, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	activeGoal := &types.ThreadGoal{
		ThreadID:  "thread-goal",
		Objective: "delayed start",
		Status:    types.ThreadGoalStatusActive,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- exec.streamGoalContinuation(ctx, "thread-goal", CodexExecArgs{}, activeGoal, sub, output)
	}()

	time.Sleep(defaultGoalContinuationStartTimeout + 100*time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("active goal stream ended before delayed turn: %v", err)
	default:
	}

	sub <- appEvent{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"thread-goal","turn":{"id":"turn-delayed"}}`),
	}
	sub <- appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-delayed","item":{"id":"msg-delayed","type":"agentMessage","text":"real output"}}`),
	}
	sub <- appEvent{
		Method: "thread/goal/updated",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-delayed","goal":{"threadId":"thread-goal","objective":"delayed start","status":"complete","createdAt":1,"updatedAt":2}}`),
	}
	sub <- appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-goal","turnId":"turn-delayed","usage":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1}}`),
	}

	if err := <-errCh; err != nil {
		t.Fatalf("streamGoalContinuation returned error: %v", err)
	}
	close(output)

	var joined string
	for result := range output {
		if result.Error != nil {
			t.Fatalf("unexpected output error: %v", result.Error)
		}
		joined += result.Line + "\n"
	}
	if strings.Contains(joined, "msg-synth") || strings.Contains(joined, "Goal: delayed start") {
		t.Fatalf("active goal emitted synthetic summary before real turn: %s", joined)
	}
	if !strings.Contains(joined, "real output") {
		t.Fatalf("real delayed turn output missing: %s", joined)
	}
}

func TestActiveTurnIDFromMismatchError(t *testing.T) {
	err := errors.New("app server error (-32600): expected active turn id TURN_OLD but found TURN_NEW")
	if got := activeTurnIDFromMismatchError(err); got != "TURN_NEW" {
		t.Fatalf("activeTurnIDFromMismatchError = %q, want %q", got, "TURN_NEW")
	}
}

func TestNoActiveTurnToInterruptError(t *testing.T) {
	err := errors.New("app server error (-32600): no active turn to interrupt")
	if !isNoActiveTurnToInterruptError(err) {
		t.Fatal("expected no-active-turn interrupt error to be recognized")
	}
}

func TestEventMatchesTurnRejectsMismatchedThread(t *testing.T) {
	event := appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-other","turnId":"turn-1","item":{"id":"msg-1","type":"agentMessage","text":"wrong"}}`),
	}

	if eventMatchesTurn(event, "thread-1", "turn-1") {
		t.Fatal("event with mismatched threadId and matching turnId should not match")
	}
}

func TestStreamTurnIgnoresOtherThreadEvents(t *testing.T) {
	exec := NewAppServerExec("", nil, nil, types.ClientInfo{}, "", "")
	output := make(chan ExecResult, 8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- exec.streamTurn(ctx, "thread-1", "turn-1", CodexExecArgs{}, output)
	}()
	waitForSubscribers(t, exec, 1)

	exec.dispatchEvent(appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-other","turnId":"turn-other","item":{"id":"msg-other","type":"agentMessage","text":"wrong stream"}}`),
	})
	exec.dispatchEvent(appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"msg-1","type":"agentMessage","text":"right stream"}}`),
	})
	exec.dispatchEvent(appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","usage":{"inputTokens":1,"cachedInputTokens":0,"outputTokens":1}}`),
	})

	if err := <-errCh; err != nil {
		t.Fatalf("streamTurn returned error: %v", err)
	}
	close(output)

	var lines []string
	for result := range output {
		if result.Error != nil {
			t.Fatalf("unexpected output error: %v", result.Error)
		}
		lines = append(lines, result.Line)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 own-thread lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if bytes.Contains([]byte(line), []byte("thread-other")) || bytes.Contains([]byte(line), []byte("wrong stream")) {
			t.Fatalf("cross-thread event leaked into normal stream: %s", line)
		}
	}
}

func TestAppEventToLegacyLineContextCompactionStatus(t *testing.T) {
	state := &turnState{items: make(map[string]map[string]interface{})}

	startedLine, done, err := appEventToLegacyLine(appEvent{
		Method: "item/started",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"compact-1","type":"contextCompaction"}}`),
	}, state)
	if err != nil {
		t.Fatalf("started event conversion failed: %v", err)
	}
	if done {
		t.Fatal("started event should not complete the stream")
	}
	assertContextCompactionStatus(t, startedLine, "running")

	completedLine, done, err := appEventToLegacyLine(appEvent{
		Method: "item/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"id":"compact-1","type":"contextCompaction"}}`),
	}, state)
	if err != nil {
		t.Fatalf("completed event conversion failed: %v", err)
	}
	if done {
		t.Fatal("item completed event should not be treated as turn completion")
	}
	assertContextCompactionStatus(t, completedLine, "complete")
}

func TestAppEventToLegacyLineAccumulatesTokenUsageIntoTurnCompleted(t *testing.T) {
	state := &turnState{items: make(map[string]map[string]interface{})}
	_, done, err := appEventToLegacyLine(appEvent{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turnId":"turn-1",
			"tokenUsage":{
				"total":{"totalTokens":200,"inputTokens":150,"cachedInputTokens":100,"outputTokens":50},
				"last":{"totalTokens":130,"inputTokens":100,"cachedInputTokens":80,"outputTokens":30},
				"modelContextWindow":200000
			}
		}`),
	}, state)
	if err != nil || done {
		t.Fatalf("token usage conversion: done=%v err=%v", done, err)
	}
	_, done, err = appEventToLegacyLine(appEvent{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turnId":"turn-1",
			"tokenUsage":{
				"total":{"totalTokens":260,"inputTokens":190,"cachedInputTokens":120,"outputTokens":70},
				"last":{"totalTokens":60,"inputTokens":40,"cachedInputTokens":20,"outputTokens":20},
				"modelContextWindow":200000
			}
		}`),
	}, state)
	if err != nil || done {
		t.Fatalf("second token usage conversion: done=%v err=%v", done, err)
	}
	_, done, err = appEventToLegacyLine(appEvent{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{
			"threadId":"thread-1",
			"turnId":"turn-1",
			"tokenUsage":{
				"total":{"totalTokens":260,"inputTokens":190,"cachedInputTokens":120,"outputTokens":70},
				"last":{"totalTokens":60,"inputTokens":40,"cachedInputTokens":20,"outputTokens":20},
				"modelContextWindow":200000
			}
		}`),
	}, state)
	if err != nil || done {
		t.Fatalf("duplicate token usage conversion: done=%v err=%v", done, err)
	}

	completedLine, done, err := appEventToLegacyLine(appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}`),
	}, state)
	if err != nil || !done {
		t.Fatalf("turn completion conversion: done=%v err=%v", done, err)
	}
	var completed struct {
		Usage types.Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(completedLine), &completed); err != nil {
		t.Fatalf("unmarshal completed line: %v", err)
	}
	if completed.Usage.InputTokens != 140 || completed.Usage.CachedInputTokens != 100 || completed.Usage.OutputTokens != 50 {
		t.Fatalf("completed usage = %#v", completed.Usage)
	}
}

func TestAppEventToLegacyLineResetsTokenUsageAtNextTurn(t *testing.T) {
	state := &turnState{
		items: make(map[string]map[string]interface{}),
		usage: map[string]interface{}{"inputTokens": float64(100)},
	}
	if _, _, err := appEventToLegacyLine(appEvent{
		Method: "turn/started",
		Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-2"}}`),
	}, state); err != nil {
		t.Fatalf("turn start conversion: %v", err)
	}
	completedLine, _, err := appEventToLegacyLine(appEvent{
		Method: "turn/completed",
		Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-2","status":"completed","items":[]}}`),
	}, state)
	if err != nil {
		t.Fatalf("turn completion conversion: %v", err)
	}
	var completed map[string]interface{}
	if err := json.Unmarshal([]byte(completedLine), &completed); err != nil {
		t.Fatalf("unmarshal completed line: %v", err)
	}
	if _, ok := completed["usage"]; ok {
		t.Fatalf("stale usage leaked into next turn: %s", completedLine)
	}
}

func TestParseAccountLoginEvents(t *testing.T) {
	completed, ok, err := parseAccountLoginCompletedEvent(appEvent{
		Method: "account/login/completed",
		Params: json.RawMessage(`{"loginId":"login-1","success":true,"error":null}`),
	})
	if err != nil {
		t.Fatalf("parse completed failed: %v", err)
	}
	if !ok {
		t.Fatal("completed event was not recognized")
	}
	if completed.LoginID == nil || *completed.LoginID != "login-1" || !completed.Success || completed.Error != nil {
		t.Fatalf("completed = %#v", completed)
	}

	updated, ok, err := parseAccountUpdatedEvent(appEvent{
		Method: "account/updated",
		Params: json.RawMessage(`{"authMode":"chatgpt","planType":"plus"}`),
	})
	if err != nil {
		t.Fatalf("parse updated failed: %v", err)
	}
	if !ok {
		t.Fatal("updated event was not recognized")
	}
	if updated.AuthMode == nil || *updated.AuthMode != "chatgpt" || updated.PlanType == nil || *updated.PlanType != types.PlanTypePlus {
		t.Fatalf("updated = %#v", updated)
	}
}

func assertContextCompactionStatus(t *testing.T, line string, want string) {
	t.Helper()
	var payload struct {
		Item struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("failed to unmarshal line %q: %v", line, err)
	}
	if payload.Item.Type != "contextCompaction" {
		t.Fatalf("item type = %q, want contextCompaction", payload.Item.Type)
	}
	if payload.Item.Status != want {
		t.Fatalf("item status = %q, want %q", payload.Item.Status, want)
	}
}

func assertParam(t *testing.T, params map[string]interface{}, key string, want interface{}) {
	t.Helper()
	got, ok := params[key]
	if !ok {
		t.Fatalf("missing param %q", key)
	}
	if got != want {
		t.Fatalf("param %q: got %v, want %v", key, got, want)
	}
}

var _ io.WriteCloser = (*captureWriteCloser)(nil)

func waitForSubscribers(t *testing.T, exec *AppServerExec, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		exec.subsMu.RLock()
		got := len(exec.subs)
		exec.subsMu.RUnlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d subscribers", want)
}
