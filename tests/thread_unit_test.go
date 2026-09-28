package tests

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fanwenlin/codex-go-sdk/codex"
	"github.com/fanwenlin/codex-go-sdk/types"
)

type mockRPCExec struct {
	MockExec
	calls []rpcCall
}

type rpcCall struct {
	method string
	params interface{}
}

func (m *mockRPCExec) RPCCall(_ context.Context, method string, params interface{}) (json.RawMessage, error) {
	m.calls = append(m.calls, rpcCall{method: method, params: params})
	switch method {
	case "thread/start":
		return json.RawMessage(`{"thread":{"id":"thread-goal-1"}}`), nil
	case "thread/read", "thread/fork":
		return json.RawMessage(`{"thread":{"id":"thread-fork-1","turns":[{"items":[{"type":"userMessage","text":"one"}]},{"items":[{"type":"userMessage","text":"two"}]},{"items":[{"type":"message","role":"user","text":"three"}]},{"items":[{"type":"user_message","text":"four"}]},{"items":[{"type":"agentMessage","text":"done"}]}]}}`), nil
	case "thread/rollback":
		return json.RawMessage(`{"thread":{"id":"thread-fork-1"}}`), nil
	case "thread/goal/get":
		return json.RawMessage(`{"goal":{"threadId":"thread-goal-1","objective":"Ship goal support","status":"active","tokenBudget":1000,"tokensUsed":25,"timeUsedSeconds":90,"createdAt":1,"updatedAt":2}}`), nil
	case "thread/goal/set":
		return json.RawMessage(`{"goal":{"threadId":"thread-goal-1","objective":"Ship goal support","status":"active","tokenBudget":null,"tokensUsed":0,"timeUsedSeconds":0,"createdAt":1,"updatedAt":2}}`), nil
	case "thread/goal/clear":
		return json.RawMessage(`{"cleared":true}`), nil
	case "thread/unsubscribe":
		return json.RawMessage(`{"status":"unsubscribed"}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func TestThreadCloseUnsubscribesWithoutDeletingHistory(t *testing.T) {
	exec := &mockRPCExec{}
	client := codex.NewCodexWithExec(exec, types.CodexOptions{Transport: types.TransportAppServer})
	thread := client.ResumeThread("thread-close-1", types.ThreadOptions{})

	if err := thread.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("RPC calls = %d, want 1", len(exec.calls))
	}
	if exec.calls[0].method != "thread/unsubscribe" {
		t.Fatalf("method = %q, want thread/unsubscribe", exec.calls[0].method)
	}
	params, ok := exec.calls[0].params.(map[string]interface{})
	if !ok || params["threadId"] != "thread-close-1" {
		t.Fatalf("params = %#v", exec.calls[0].params)
	}
}

type mockShellExec struct {
	MockExec
	commands []string
}

func (m *mockShellExec) RunShellCommand(_ codex.CodexExecArgs, command string) <-chan codex.ExecResult {
	m.commands = append(m.commands, command)
	ch := make(chan codex.ExecResult, 3)
	ch <- codex.ExecResult{Line: `{"type":"turn.started"}`}
	ch <- codex.ExecResult{Line: `{"type":"turn.completed","usage":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0}}`}
	close(ch)
	return ch
}

type mockGoalSetExec struct {
	MockExec
	params []types.ThreadGoalSetParams
}

func (m *mockGoalSetExec) RunGoalSet(_ codex.CodexExecArgs, params types.ThreadGoalSetParams) <-chan codex.ExecResult {
	m.params = append(m.params, params)
	ch := make(chan codex.ExecResult, 5)
	ch <- codex.ExecResult{Line: `{"type":"thread.goal.updated","threadId":"thread-goal-1","goal":{"threadId":"thread-goal-1","objective":"Ship goal support","status":"active","createdAt":1,"updatedAt":2}}`}
	ch <- codex.ExecResult{Line: `{"type":"turn.started","threadId":"thread-goal-1","turnId":"turn-goal-1"}`}
	ch <- codex.ExecResult{Line: `{"type":"item.completed","item":{"id":"msg-goal-1","type":"agentMessage","text":"working"},"threadId":"thread-goal-1","turnId":"turn-goal-1"}`}
	ch <- codex.ExecResult{Line: `{"type":"turn.completed","threadId":"thread-goal-1","turnId":"turn-goal-1","usage":{"inputTokens":0,"cachedInputTokens":0,"outputTokens":0}}`}
	close(ch)
	return ch
}

type closeTestExec struct {
	closed bool
}

func (e *closeTestExec) Run(_ codex.CodexExecArgs) <-chan codex.ExecResult {
	ch := make(chan codex.ExecResult)
	close(ch)
	return ch
}

func (e *closeTestExec) Close() error {
	e.closed = true
	return nil
}

func TestInputNormalization(t *testing.T) {
	// Create a thread to test normalization
	client := codex.NewCodex(types.CodexOptions{})
	_ = client.StartThread(types.ThreadOptions{})

	// We can't directly test private normalizeInput, so we test through Run
	// For this unit test, we'll just verify the types compile

	// Test string input
	input1 := "Hello, world!"
	if input1 == "" {
		t.Error("String input should not be empty")
	}

	// Test UserInput slice
	input2 := []types.UserInput{
		types.NewTextInput("First part"),
		types.NewTextInput("Second part"),
		types.NewImageInput("/path/to/image.png"),
	}

	if len(input2) != 3 {
		t.Errorf("Expected 3 inputs, got %d", len(input2))
	}

	if input2[0].Type != "text" || input2[0].Text != "First part" {
		t.Error("First input should be text type")
	}

	if input2[2].Type != "local_image" || input2[2].Path != "/path/to/image.png" {
		t.Error("Third input should be image type")
	}
}

func TestConstants(t *testing.T) {
	// Test that all constants are defined correctly
	if codex.ApprovalModeNever != "never" {
		t.Error("ApprovalModeNever mismatch")
	}
	if codex.ApprovalModeOnRequest != "on-request" {
		t.Error("ApprovalModeOnRequest mismatch")
	}
	if codex.SandboxModeReadOnly != "read-only" {
		t.Error("SandboxModeReadOnly mismatch")
	}
	if codex.ModelReasoningEffortHigh != "high" {
		t.Error("ModelReasoningEffortHigh mismatch")
	}
	if codex.ModelReasoningEffortMax != "max" {
		t.Error("ModelReasoningEffortMax mismatch")
	}
	if codex.ModelReasoningEffortUltra != "ultra" {
		t.Error("ModelReasoningEffortUltra mismatch")
	}
	if codex.ReasoningEffortMax != "max" {
		t.Error("ReasoningEffortMax mismatch")
	}
	if codex.ReasoningEffortUltra != "ultra" {
		t.Error("ReasoningEffortUltra mismatch")
	}
	if codex.WebSearchModeLive != "live" {
		t.Error("WebSearchModeLive mismatch")
	}
}

func TestForkThreadTruncatesWithRollback(t *testing.T) {
	exec := &mockRPCExec{}
	client := codex.NewCodexWithExec(exec, types.CodexOptions{Transport: types.TransportAppServer})
	ordinal := 3
	thread, err := client.ForkThread(context.Background(), "thread-source-1", types.ThreadForkOptions{
		ThreadOptions: types.ThreadOptions{
			Model:                "gpt-5.3-codex",
			WorkingDirectory:     "/tmp/project",
			FastService:          "on",
			SandboxMode:          types.SandboxModeFullAccess,
			ApprovalPolicy:       types.ApprovalModeNever,
			ModelReasoningEffort: types.ModelReasoningEffortHigh,
		},
		TruncateBeforeNthUserMessage: &ordinal,
	})
	if err != nil {
		t.Fatalf("ForkThread returned error: %v", err)
	}
	if thread == nil || thread.ID() == nil || *thread.ID() != "thread-fork-1" {
		t.Fatalf("forked thread id = %#v", thread)
	}
	if len(exec.calls) != 3 {
		t.Fatalf("expected 3 RPC calls, got %d", len(exec.calls))
	}
	call := exec.calls[1]
	if call.method != "thread/fork" {
		t.Fatalf("method = %q, want thread/fork", call.method)
	}
	params, ok := call.params.(map[string]interface{})
	if !ok {
		t.Fatalf("params type = %T", call.params)
	}
	if got := params["threadId"]; got != "thread-source-1" {
		t.Fatalf("threadId = %#v", got)
	}
	if _, ok := params["truncateBeforeNthUserMessage"]; ok {
		t.Fatalf("thread/fork should not receive unsupported truncateBeforeNthUserMessage param")
	}
	if got := params["serviceTier"]; got != "fast" {
		t.Fatalf("serviceTier = %#v", got)
	}
	config, ok := params["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("config type = %T", params["config"])
	}
	if got := config["model_reasoning_effort"]; got != "high" {
		t.Fatalf("model_reasoning_effort = %#v", got)
	}
	rollbackCall := exec.calls[2]
	if rollbackCall.method != "thread/rollback" {
		t.Fatalf("method = %q, want thread/rollback", rollbackCall.method)
	}
	rollbackParams, ok := rollbackCall.params.(map[string]interface{})
	if !ok {
		t.Fatalf("rollback params type = %T", rollbackCall.params)
	}
	if got := rollbackParams["threadId"]; got != "thread-fork-1" {
		t.Fatalf("rollback threadId = %#v", got)
	}
	if got := rollbackParams["numTurns"]; got != 2 {
		t.Fatalf("rollback numTurns = %#v", got)
	}
}

func TestTypeAliases(t *testing.T) {
	// Test that type aliases work
	var event types.ThreadEvent = &types.ThreadStartedEvent{
		Type:     "thread.started",
		ThreadId: "test-123",
	}

	if event.GetType() != "thread.started" {
		t.Error("GetType should return thread.started")
	}

	// Test item types
	var item types.ThreadItem = &types.AgentMessageItem{
		ID:   "msg-1",
		Type: "agentMessage",
		Text: "Hello!",
	}

	if item.GetType() != "agentMessage" {
		t.Error("Item GetType should return agentMessage")
	}

	// Test CommandExecutionItem
	exitCode := 0
	aggregatedOutput := "test\n"
	cmdItem := &types.CommandExecutionItem{
		ID:               "cmd-1",
		Type:             "commandExecution",
		Command:          "echo test",
		AggregatedOutput: &aggregatedOutput,
		ExitCode:         &exitCode,
		Status:           types.CommandExecutionStatusCompleted,
	}

	if cmdItem.Status != types.CommandExecutionStatusCompleted {
		t.Error("Command should be completed")
	}
}

func TestTurnCreation(t *testing.T) {
	// Test Turn structure
	usage := &types.Usage{
		InputTokens:       100,
		CachedInputTokens: 20,
		OutputTokens:      50,
	}

	turn := &types.Turn{
		Items: []types.ThreadItem{
			&types.AgentMessageItem{
				ID:   "msg-1",
				Type: "agentMessage",
				Text: "Test response",
			},
		},
		FinalResponse: "Test response",
		Usage:         usage,
	}

	if turn.FinalResponse != "Test response" {
		t.Error("Final response mismatch")
	}

	if turn.Usage.InputTokens != 100 {
		t.Error("Input tokens mismatch")
	}
}

func TestCodexClientCreation(t *testing.T) {
	// Test creating Codex client with options
	client := codex.NewCodex(types.CodexOptions{
		ApiKey:  "test-key",
		BaseUrl: "https://api.example.com",
	})

	if client == nil {
		t.Error("Client should not be nil")
	}

	// Test creating thread
	thread := client.StartThread(types.ThreadOptions{
		Model:       "claude-3-opus",
		SandboxMode: codex.SandboxModeReadOnly,
	})

	if thread == nil {
		t.Error("Thread should not be nil")
	}

	// Thread ID should be nil initially
	if thread.ID() != nil {
		t.Error("Thread ID should be nil before first turn")
	}
}

func TestCodexClose(t *testing.T) {
	exec := &closeTestExec{}
	client := codex.NewCodexWithExec(exec, types.CodexOptions{})

	if err := client.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if !exec.closed {
		t.Fatal("expected Close() to be forwarded to the underlying exec")
	}
}

func TestThreadResumption(t *testing.T) {
	client := codex.NewCodex(types.CodexOptions{})

	// Create thread with ID
	threadID := "existing-thread-123"
	thread := client.ResumeThread(threadID, types.ThreadOptions{})

	if thread == nil {
		t.Error("Resumed thread should not be nil")
	}

	id := thread.ID()
	if id == nil || *id != threadID {
		t.Error("Thread ID should match resumed ID")
	}
}

func TestOutputSchemaFile(t *testing.T) {
	// Test creating output schema file
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"answer": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"answer"},
	}

	schemaFile, err := codex.CreateOutputSchemaFile(schema)
	if err != nil {
		t.Fatalf("Failed to create schema file: %v", err)
	}

	if schemaFile.SchemaPath == "" {
		t.Error("Schema path should not be empty")
	}

	// Cleanup should work
	err = schemaFile.Cleanup()
	if err != nil {
		t.Errorf("Cleanup failed: %v", err)
	}

	// Test nil schema (should return no-op cleanup)
	schemaFile2, err := codex.CreateOutputSchemaFile(nil)
	if err != nil {
		t.Fatalf("Failed to create nil schema file: %v", err)
	}

	if schemaFile2.SchemaPath != "" {
		t.Error("Nil schema should have empty path")
	}

	err = schemaFile2.Cleanup()
	if err != nil {
		t.Errorf("Nil schema cleanup failed: %v", err)
	}
}

func TestEventTypes(t *testing.T) {
	// Test all event types can be created
	events := []types.ThreadEvent{
		&types.ThreadStartedEvent{
			Type:     "thread.started",
			ThreadId: "thread-123",
		},
		&types.TurnStartedEvent{
			Type: "turn.started",
		},
		&types.TurnCompletedEvent{
			Type: "turn.completed",
			Usage: types.Usage{
				InputTokens:       42,
				CachedInputTokens: 12,
				OutputTokens:      5,
			},
		},
		&types.ItemCompletedEvent{
			Type: "item.completed",
			Item: &types.AgentMessageItem{
				ID:   "msg-1",
				Type: "agentMessage",
				Text: "Test",
			},
		},
		&types.ThreadErrorEvent{
			Type:    "error",
			Message: "Test error",
		},
	}

	for _, event := range events {
		if event.GetType() == "" {
			t.Error("Event type should not be empty")
		}
	}
}

func TestItemTypes(t *testing.T) {
	// Test all item types
	items := []types.ThreadItem{
		&types.CommandExecutionItem{
			ID:      "cmd-1",
			Type:    "commandExecution",
			Command: "ls -la",
			Status:  types.CommandExecutionStatusInProgress,
		},
		&types.FileChangeItem{
			ID:   "file-1",
			Type: "fileChange",
			Changes: []types.FileUpdateChange{
				{Path: "test.go", Kind: types.PatchChangeKind{Type: types.PatchChangeKindUpdate}},
			},
			Status: types.PatchApplyStatusCompleted,
		},
		&types.McpToolCallItem{
			ID:        "mcp-1",
			Type:      "mcpToolCall",
			Server:    "test-server",
			Tool:      "test-tool",
			Arguments: map[string]interface{}{"arg": "value"},
			Status:    types.McpToolCallStatusCompleted,
		},
		&types.WebSearchItem{
			ID:    "search-1",
			Type:  "webSearch",
			Query: "test query",
		},
		&types.TodoListItem{
			ID:   "todo-1",
			Type: "todoList",
			Items: []types.TodoItem{
				{Text: "Task 1", Completed: false},
			},
		},
		&types.ErrorItem{
			ID:      "error-1",
			Type:    "error",
			Message: "Test error",
		},
	}

	for _, item := range items {
		if item.GetType() == "" {
			t.Error("Item type should not be empty")
		}
	}
}

func TestItemStartedEvent_FileChangeKindObject(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "fileChange",
			"id": "file-1",
			"changes": [
				{"path": "test.go", "kind": {"type": "update", "move_path": "new_test.go"}, "diff": "@@ -1 +1 @@\n-old\n+new"}
			],
			"status": "completed"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	if event.GetType() != "item.started" {
		t.Fatalf("expected event type item.started, got %q", event.GetType())
	}

	item, ok := event.Item.(*types.FileChangeItem)
	if !ok {
		t.Fatalf("expected FileChangeItem, got %T", event.Item)
	}
	if len(item.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(item.Changes))
	}
	if item.Changes[0].Kind.Type != types.PatchChangeKindUpdate {
		t.Fatalf("expected kind %q, got %q", types.PatchChangeKindUpdate, item.Changes[0].Kind.Type)
	}
	if item.Changes[0].Kind.MovePath == nil || *item.Changes[0].Kind.MovePath != "new_test.go" {
		t.Fatalf("expected move_path %q, got %v", "new_test.go", item.Changes[0].Kind.MovePath)
	}
	if item.Changes[0].Diff != "@@ -1 +1 @@\n-old\n+new" {
		t.Fatalf("expected diff to round-trip, got %q", item.Changes[0].Diff)
	}
}

func TestParseThreadEvent_TurnDiffUpdated(t *testing.T) {
	mockExec := NewMockExec()
	mockExec.SetEvents([]string{
		`{"type":"turn.diff.updated","threadId":"thread-1","turnId":"turn-1","diff":"@@ -1 +1 @@\n-old\n+new"}`,
	})
	thread := NewTestThread(mockExec)
	streamed, err := thread.RunStreamed("test", types.TurnOptions{})
	if err != nil {
		t.Fatalf("run streamed failed: %v", err)
	}

	var event types.ThreadEvent
	for next := range streamed.Events {
		event = next
	}
	diffEvent, ok := event.(*types.TurnDiffUpdatedEvent)
	if !ok {
		t.Fatalf("expected TurnDiffUpdatedEvent, got %T", event)
	}
	if diffEvent.ThreadId != "thread-1" {
		t.Fatalf("expected thread id %q, got %q", "thread-1", diffEvent.ThreadId)
	}
	if diffEvent.TurnId != "turn-1" {
		t.Fatalf("expected turn id %q, got %q", "turn-1", diffEvent.TurnId)
	}
	if diffEvent.Diff != "@@ -1 +1 @@\n-old\n+new" {
		t.Fatalf("expected diff body, got %q", diffEvent.Diff)
	}
}

func TestSupportedSlashCommandsIncludesGoal(t *testing.T) {
	commands := codex.SupportedSlashCommands()
	for _, command := range commands {
		if command.Name == "goal" {
			if command.ArgumentHint == "" {
				t.Fatal("expected goal command to expose an argument hint")
			}
			return
		}
	}
	t.Fatal("expected /goal to be listed as a supported slash command")
}

func TestGoalSlashCommandGetsCurrentGoal(t *testing.T) {
	exec := &mockRPCExec{}
	client := codex.NewCodexWithExec(exec, types.CodexOptions{})
	thread := client.StartThread(types.ThreadOptions{})

	turn, err := thread.Run("/goal", types.TurnOptions{})
	if err != nil {
		t.Fatalf("run /goal failed: %v", err)
	}
	if turn.FinalResponse == "" {
		t.Fatal("expected a synthetic goal summary")
	}
	if len(exec.calls) != 2 {
		t.Fatalf("expected thread/start and thread/goal/get calls, got %d", len(exec.calls))
	}
	if exec.calls[0].method != "thread/start" || exec.calls[1].method != "thread/goal/get" {
		t.Fatalf("unexpected calls: %#v", exec.calls)
	}
	params, ok := exec.calls[1].params.(types.ThreadGoalGetParams)
	if !ok {
		t.Fatalf("expected ThreadGoalGetParams, got %T", exec.calls[1].params)
	}
	if params.ThreadID != "thread-goal-1" {
		t.Fatalf("expected thread id %q, got %q", "thread-goal-1", params.ThreadID)
	}
}

func TestGoalSlashCommandSetsObjective(t *testing.T) {
	exec := &mockRPCExec{}
	client := codex.NewCodexWithExec(exec, types.CodexOptions{})
	thread := client.ResumeThread("thread-goal-1", types.ThreadOptions{})

	if _, err := thread.Run("/goal Ship goal support", types.TurnOptions{}); err != nil {
		t.Fatalf("run /goal objective failed: %v", err)
	}
	if len(exec.calls) != 1 {
		t.Fatalf("expected one goal set call, got %d", len(exec.calls))
	}
	if exec.calls[0].method != "thread/goal/set" {
		t.Fatalf("expected thread/goal/set, got %q", exec.calls[0].method)
	}
	params, ok := exec.calls[0].params.(types.ThreadGoalSetParams)
	if !ok {
		t.Fatalf("expected ThreadGoalSetParams, got %T", exec.calls[0].params)
	}
	if params.Objective == nil || *params.Objective != "Ship goal support" {
		t.Fatalf("unexpected objective: %#v", params.Objective)
	}
	if params.Status == nil || *params.Status != types.ThreadGoalStatusActive {
		t.Fatalf("unexpected status: %#v", params.Status)
	}
}

func TestGoalSlashCommandStreamsContinuationWithGoalRunner(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantObjective *string
		wantStatus    types.ThreadGoalStatus
	}{
		{
			name:          "objective",
			input:         "/goal Ship goal support",
			wantObjective: ptrString("Ship goal support"),
			wantStatus:    types.ThreadGoalStatusActive,
		},
		{
			name:       "resume",
			input:      "/goal resume",
			wantStatus: types.ThreadGoalStatusActive,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &mockGoalSetExec{}
			client := codex.NewCodexWithExec(exec, types.CodexOptions{})
			thread := client.ResumeThread("thread-goal-1", types.ThreadOptions{})

			turn, err := thread.Run(tc.input, types.TurnOptions{})
			if err != nil {
				t.Fatalf("run %s failed: %v", tc.input, err)
			}
			if len(exec.params) != 1 {
				t.Fatalf("expected one goal set call, got %d", len(exec.params))
			}
			params := exec.params[0]
			if params.ThreadID != "" {
				t.Fatalf("expected runner to fill thread id, got %q", params.ThreadID)
			}
			if tc.wantObjective == nil {
				if params.Objective != nil {
					t.Fatalf("unexpected objective: %#v", params.Objective)
				}
			} else if params.Objective == nil || *params.Objective != *tc.wantObjective {
				t.Fatalf("unexpected objective: %#v", params.Objective)
			}
			if params.Status == nil || *params.Status != tc.wantStatus {
				t.Fatalf("unexpected status: %#v", params.Status)
			}
			if turn.FinalResponse != "working" {
				t.Fatalf("expected streamed continuation response, got %q", turn.FinalResponse)
			}
		})
	}
}

func TestGoalSlashCommandControlCommands(t *testing.T) {
	cases := []struct {
		input      string
		wantMethod string
		wantStatus *types.ThreadGoalStatus
	}{
		{input: "/goal pause", wantMethod: "thread/goal/set", wantStatus: ptrGoalStatus(types.ThreadGoalStatusPaused)},
		{input: "/goal resume", wantMethod: "thread/goal/set", wantStatus: ptrGoalStatus(types.ThreadGoalStatusActive)},
		{input: "/goal clear", wantMethod: "thread/goal/clear"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			exec := &mockRPCExec{}
			client := codex.NewCodexWithExec(exec, types.CodexOptions{})
			thread := client.ResumeThread("thread-goal-1", types.ThreadOptions{})
			if _, err := thread.Run(tc.input, types.TurnOptions{}); err != nil {
				t.Fatalf("run %s failed: %v", tc.input, err)
			}
			if len(exec.calls) != 1 {
				t.Fatalf("expected one call, got %d", len(exec.calls))
			}
			if exec.calls[0].method != tc.wantMethod {
				t.Fatalf("expected %q, got %q", tc.wantMethod, exec.calls[0].method)
			}
			if tc.wantStatus != nil {
				params, ok := exec.calls[0].params.(types.ThreadGoalSetParams)
				if !ok {
					t.Fatalf("expected ThreadGoalSetParams, got %T", exec.calls[0].params)
				}
				if params.Status == nil || *params.Status != *tc.wantStatus {
					t.Fatalf("unexpected status: %#v", params.Status)
				}
			}
		})
	}
}

func ptrGoalStatus(status types.ThreadGoalStatus) *types.ThreadGoalStatus {
	return &status
}

func ptrString(value string) *string {
	return &value
}

func TestSupportedSlashCommandsIncludesShellOnly(t *testing.T) {
	commands := codex.SupportedSlashCommands()
	found := map[string]bool{}
	for _, command := range commands {
		found[command.Name] = true
	}
	if !found["shell"] {
		t.Fatal("expected /shell to be listed as a supported slash command")
	}
	if found["exec"] {
		t.Fatal("expected /exec to be removed from supported slash commands")
	}
}

func TestShellSlashCommandUsesShellRunner(t *testing.T) {
	exec := &mockShellExec{}
	client := codex.NewCodexWithExec(exec, types.CodexOptions{})
	thread := client.StartThread(types.ThreadOptions{})

	if _, err := thread.Run("/shell echo ok", types.TurnOptions{}); err != nil {
		t.Fatalf("run /shell failed: %v", err)
	}
	if len(exec.commands) != 1 || exec.commands[0] != "echo ok" {
		t.Fatalf("unexpected shell commands: %#v", exec.commands)
	}
}

func TestItemStartedEvent_CommandExecutionSchema(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "commandExecution",
			"id": "cmd-1",
			"command": "ls",
			"commandActions": [
				{"type":"listFiles","command":"ls","path":"src"}
			],
			"aggregatedOutput": "ok",
			"source": "userShell",
			"status": "inProgress"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	item, ok := event.Item.(*types.CommandExecutionItem)
	if !ok {
		t.Fatalf("expected CommandExecutionItem, got %T", event.Item)
	}
	if item.AggregatedOutput == nil || *item.AggregatedOutput != "ok" {
		t.Fatalf("expected aggregated output %q, got %v", "ok", item.AggregatedOutput)
	}
	if string(item.Status) != "inProgress" {
		t.Fatalf("expected status %q, got %q", "inProgress", item.Status)
	}
	if item.Source != "userShell" {
		t.Fatalf("expected source %q, got %q", "userShell", item.Source)
	}
	if len(item.CommandActions) != 1 || item.CommandActions[0].Type != types.CommandActionTypeListFiles {
		t.Fatalf("expected listFiles command action, got %#v", item.CommandActions)
	}
	if item.CommandActions[0].Path == nil || *item.CommandActions[0].Path != "src" {
		t.Fatalf("expected command action path %q, got %#v", "src", item.CommandActions[0].Path)
	}
}

func TestCommandExecutionItemSupportsSnakeCaseCommandActions(t *testing.T) {
	payload := []byte(`{
		"type":"command_execution",
		"id":"cmd-2",
		"command":"rg TODO server",
		"command_actions":[{"type":"search","command":"rg TODO server","query":"TODO","path":"server"}],
		"status":"completed"
	}`)
	var item types.CommandExecutionItem
	if err := json.Unmarshal(payload, &item); err != nil {
		t.Fatalf("unmarshal command execution: %v", err)
	}
	if len(item.CommandActions) != 1 || item.CommandActions[0].Type != types.CommandActionTypeSearch {
		t.Fatalf("expected search command action, got %#v", item.CommandActions)
	}
	if item.CommandActions[0].Query == nil || *item.CommandActions[0].Query != "TODO" {
		t.Fatalf("expected query %q, got %#v", "TODO", item.CommandActions[0].Query)
	}
}

func TestItemCompletedEvent_McpToolCallSchema(t *testing.T) {
	payload := []byte(`{
		"type": "item.completed",
		"item": {
			"type": "mcpToolCall",
			"id": "mcp-1",
			"server": "test-server",
			"tool": "test-tool",
			"arguments": {"arg": "value"},
			"result": {
				"content": "ok",
				"structuredContent": {"a": 1}
			},
			"status": "completed"
		}
	}`)

	var event types.ItemCompletedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.completed failed: %v", err)
	}
	item, ok := event.Item.(*types.McpToolCallItem)
	if !ok {
		t.Fatalf("expected McpToolCallItem, got %T", event.Item)
	}
	if item.Result == nil {
		t.Fatalf("expected result, got nil")
	}
	if item.Result.Content != "ok" {
		t.Fatalf("expected content %q, got %v", "ok", item.Result.Content)
	}
	if item.Result.StructuredContent == nil {
		t.Fatalf("expected structured content, got nil")
	}
}

func TestItemCompletedEvent_FileChangeDeclined(t *testing.T) {
	payload := []byte(`{
		"type": "item.completed",
		"item": {
			"type": "fileChange",
			"id": "file-1",
			"changes": [
				{"path": "test.go", "kind": {"type": "update", "move_path": null}}
			],
			"status": "declined"
		}
	}`)

	var event types.ItemCompletedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.completed failed: %v", err)
	}
	item, ok := event.Item.(*types.FileChangeItem)
	if !ok {
		t.Fatalf("expected FileChangeItem, got %T", event.Item)
	}
	if string(item.Status) != "declined" {
		t.Fatalf("expected status %q, got %q", "declined", item.Status)
	}
	if item.Changes[0].Kind.Type != types.PatchChangeKindUpdate {
		t.Fatalf("expected kind %q, got %q", types.PatchChangeKindUpdate, item.Changes[0].Kind.Type)
	}
	if item.Changes[0].Kind.MovePath != nil {
		t.Fatalf("expected move_path nil, got %v", item.Changes[0].Kind.MovePath)
	}
}

func TestItemCompletedEvent_FileChangeKindAsString(t *testing.T) {
	payload := []byte(`{
		"type": "item.completed",
		"item": {
			"type": "fileChange",
			"id": "file-2",
			"changes": [
				{"path": "test.go", "kind": "update"}
			],
			"status": "completed"
		}
	}`)

	var event types.ItemCompletedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.completed failed: %v", err)
	}
	item, ok := event.Item.(*types.FileChangeItem)
	if !ok {
		t.Fatalf("expected FileChangeItem, got %T", event.Item)
	}
	if item.Changes[0].Kind.Type != types.PatchChangeKindUpdate {
		t.Fatalf("expected kind %q, got %q", types.PatchChangeKindUpdate, item.Changes[0].Kind.Type)
	}
	if item.Changes[0].Kind.MovePath != nil {
		t.Fatalf("expected move_path nil, got %v", item.Changes[0].Kind.MovePath)
	}
}

func TestItemCompletedEvent_CommandExecutionDeclined(t *testing.T) {
	payload := []byte(`{
		"type": "item.completed",
		"item": {
			"type": "commandExecution",
			"id": "cmd-2",
			"command": "rm -rf /tmp/nope",
			"aggregatedOutput": "",
			"status": "declined"
		}
	}`)

	var event types.ItemCompletedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.completed failed: %v", err)
	}
	item, ok := event.Item.(*types.CommandExecutionItem)
	if !ok {
		t.Fatalf("expected CommandExecutionItem, got %T", event.Item)
	}
	if item.Status != types.CommandExecutionStatusDeclined {
		t.Fatalf("expected status %q, got %q", types.CommandExecutionStatusDeclined, item.Status)
	}
}

func TestThreadStartedEvent_CamelCaseThreadId(t *testing.T) {
	payload := []byte(`{
		"type": "thread.started",
		"threadId": "thread-123"
	}`)

	var event types.ThreadStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal thread.started failed: %v", err)
	}
	if event.ThreadId != "thread-123" {
		t.Fatalf("expected thread id %q, got %q", "thread-123", event.ThreadId)
	}
}

func TestTurnCompletedEvent_UsageCamelCase(t *testing.T) {
	payload := []byte(`{
		"type": "turn.completed",
		"usage": {
			"inputTokens": 10,
			"cachedInputTokens": 2,
			"outputTokens": 5
		}
	}`)

	var event types.TurnCompletedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal turn.completed failed: %v", err)
	}
	if event.Usage.InputTokens != 10 {
		t.Fatalf("expected input tokens %d, got %d", 10, event.Usage.InputTokens)
	}
	if event.Usage.CachedInputTokens != 2 {
		t.Fatalf("expected cached input tokens %d, got %d", 2, event.Usage.CachedInputTokens)
	}
	if event.Usage.OutputTokens != 5 {
		t.Fatalf("expected output tokens %d, got %d", 5, event.Usage.OutputTokens)
	}
}

func TestItemUpdatedEvent_ReasoningSummaryText(t *testing.T) {
	payload := []byte(`{
		"type": "item.updated",
		"item": {
			"type": "reasoning",
			"id": "reason-1",
			"summary": ["Short summary"]
		}
	}`)

	var event types.ItemUpdatedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.updated failed: %v", err)
	}
	item, ok := event.Item.(*types.ReasoningItem)
	if !ok {
		t.Fatalf("expected ReasoningItem, got %T", event.Item)
	}
	if len(item.Summary) != 1 || item.Summary[0] != "Short summary" {
		t.Fatalf("expected summary %q, got %v", "Short summary", item.Summary)
	}
}

func TestItemStartedEvent_TodoList(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "todoList",
			"id": "todo-1",
			"items": [
				{"text": "Task 1", "completed": false},
				{"text": "Task 2", "completed": true}
			]
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	item, ok := event.Item.(*types.TodoListItem)
	if !ok {
		t.Fatalf("expected TodoListItem, got %T", event.Item)
	}
	if len(item.Items) != 2 {
		t.Fatalf("expected 2 todo items, got %d", len(item.Items))
	}
	if item.Items[1].Completed != true {
		t.Fatalf("expected second todo completed true, got %v", item.Items[1].Completed)
	}
}

func TestItemStartedEvent_WebSearch(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "webSearch",
			"id": "search-1",
			"query": "hello"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	item, ok := event.Item.(*types.WebSearchItem)
	if !ok {
		t.Fatalf("expected WebSearchItem, got %T", event.Item)
	}
	if item.Query != "hello" {
		t.Fatalf("expected query %q, got %q", "hello", item.Query)
	}
}

func TestItemStartedEvent_ErrorItem(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "error",
			"id": "error-1",
			"message": "oops"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	item, ok := event.Item.(*types.ErrorItem)
	if !ok {
		t.Fatalf("expected ErrorItem, got %T", event.Item)
	}
	if item.Message != "oops" {
		t.Fatalf("expected message %q, got %q", "oops", item.Message)
	}
}

func TestThreadErrorEvent_AppServerShape(t *testing.T) {
	payload := []byte(`{
		"type": "error",
		"willRetry": false,
		"error": {
			"message": "stream failed"
		}
	}`)

	var event types.ThreadErrorEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal ThreadErrorEvent failed: %v", err)
	}
	if event.Message != "stream failed" {
		t.Fatalf("expected flattened message %q, got %q", "stream failed", event.Message)
	}
	if event.Error == nil || event.Error.Message != "stream failed" {
		t.Fatalf("expected structured error to be preserved, got %#v", event.Error)
	}
	if event.WillRetry {
		t.Fatal("expected willRetry=false")
	}
}

func TestItemStartedEvent_UserMessage(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "userMessage",
			"id": "user-1",
			"text": "hi"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	item, ok := event.Item.(*types.UserMessageItem)
	if !ok {
		t.Fatalf("expected UserMessageItem, got %T", event.Item)
	}
	if item.Text != "hi" {
		t.Fatalf("expected text %q, got %q", "hi", item.Text)
	}
}

func TestItemStartedEvent_ImageView(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "imageView",
			"id": "img-1",
			"url": "https://example.com/image.png"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal item.started failed: %v", err)
	}
	item, ok := event.Item.(*types.ImageViewItem)
	if !ok {
		t.Fatalf("expected ImageViewItem, got %T", event.Item)
	}
	if item.URL != "https://example.com/image.png" {
		t.Fatalf("expected url %q, got %q", "https://example.com/image.png", item.URL)
	}
}

func TestItemStartedEvent_ReviewModeItems(t *testing.T) {
	enterPayload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "enteredReviewMode",
			"id": "enter-1"
		}
	}`)

	var enterEvent types.ItemStartedEvent
	if err := json.Unmarshal(enterPayload, &enterEvent); err != nil {
		t.Fatalf("unmarshal enteredReviewMode failed: %v", err)
	}
	if _, ok := enterEvent.Item.(*types.EnteredReviewModeItem); !ok {
		t.Fatalf("expected EnteredReviewModeItem, got %T", enterEvent.Item)
	}

	exitPayload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "exitedReviewMode",
			"id": "exit-1"
		}
	}`)

	var exitEvent types.ItemStartedEvent
	if err := json.Unmarshal(exitPayload, &exitEvent); err != nil {
		t.Fatalf("unmarshal exitedReviewMode failed: %v", err)
	}
	if _, ok := exitEvent.Item.(*types.ExitedReviewModeItem); !ok {
		t.Fatalf("expected ExitedReviewModeItem, got %T", exitEvent.Item)
	}
}

func TestItemStartedEvent_CompactedItem(t *testing.T) {
	payload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "compacted",
			"id": "compact-1",
			"summary": "summary"
		}
	}`)

	var event types.ItemStartedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("unmarshal compacted failed: %v", err)
	}
	item, ok := event.Item.(*types.CompactedItem)
	if !ok {
		t.Fatalf("expected CompactedItem, got %T", event.Item)
	}
	if item.Summary != "summary" {
		t.Fatalf("expected summary %q, got %q", "summary", item.Summary)
	}
}

func TestContextCompactionItemStatus(t *testing.T) {
	startedPayload := []byte(`{
		"type": "item.started",
		"item": {
			"type": "contextCompaction",
			"id": "compact-1",
			"status": "running"
		}
	}`)

	var started types.ItemStartedEvent
	if err := json.Unmarshal(startedPayload, &started); err != nil {
		t.Fatalf("unmarshal context compaction started failed: %v", err)
	}
	startedItem, ok := started.Item.(*types.CompactedItem)
	if !ok {
		t.Fatalf("expected CompactedItem, got %T", started.Item)
	}
	if startedItem.Status != "running" {
		t.Fatalf("expected started status %q, got %q", "running", startedItem.Status)
	}

	completedPayload := []byte(`{
		"type": "item.completed",
		"item": {
			"type": "contextCompaction",
			"id": "compact-1",
			"status": "complete"
		}
	}`)

	var completed types.ItemCompletedEvent
	if err := json.Unmarshal(completedPayload, &completed); err != nil {
		t.Fatalf("unmarshal context compaction completed failed: %v", err)
	}
	completedItem, ok := completed.Item.(*types.CompactedItem)
	if !ok {
		t.Fatalf("expected CompactedItem, got %T", completed.Item)
	}
	if completedItem.Status != "complete" {
		t.Fatalf("expected completed status %q, got %q", "complete", completedItem.Status)
	}
}

func TestItemStartedEvent_CollabToolCall(t *testing.T) {
	for _, itemType := range []string{"collabToolCall", "collabAgentToolCall"} {
		t.Run(itemType, func(t *testing.T) {
			payload := []byte(`{
			"type": "item.started",
			"item": {
				"type": "` + itemType + `",
				"id": "collab-1",
				"tool": "tool",
				"arguments": {"a": 1},
				"status": "inProgress",
				"senderThreadId": "sender-thread",
				"receiverThreadIds": ["receiver-thread"],
				"prompt": "inspect this",
				"model": "gpt-test",
				"reasoningEffort": "high",
				"agentsStates": {
					"receiver-thread": {
						"status": "running",
						"message": "working"
					}
				}
			}
		}`)

			var event types.ItemStartedEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("unmarshal collab tool call failed: %v", err)
			}
			item, ok := event.Item.(*types.CollabToolCallItem)
			if !ok {
				t.Fatalf("expected CollabToolCallItem, got %T", event.Item)
			}
			if item.Tool != "tool" {
				t.Fatalf("expected tool %q, got %q", "tool", item.Tool)
			}
			if item.Status != "inProgress" {
				t.Fatalf("expected status %q, got %q", "inProgress", item.Status)
			}
			if item.SenderThreadID != "sender-thread" {
				t.Fatalf("expected sender thread id, got %q", item.SenderThreadID)
			}
			if len(item.ReceiverThreadIDs) != 1 || item.ReceiverThreadIDs[0] != "receiver-thread" {
				t.Fatalf("expected receiver thread ids, got %#v", item.ReceiverThreadIDs)
			}
			if item.Prompt == nil || *item.Prompt != "inspect this" {
				t.Fatalf("expected prompt, got %#v", item.Prompt)
			}
			if item.Model == nil || *item.Model != "gpt-test" {
				t.Fatalf("expected model, got %#v", item.Model)
			}
			if item.ReasoningEffort == nil || *item.ReasoningEffort != "high" {
				t.Fatalf("expected reasoning effort, got %#v", item.ReasoningEffort)
			}
			state, ok := item.AgentsStates["receiver-thread"]
			if !ok || state.Status != "running" || state.Message == nil || *state.Message != "working" {
				t.Fatalf("expected agent state, got %#v", item.AgentsStates)
			}
		})
	}
}
