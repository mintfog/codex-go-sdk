package codex

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fanwenlin/codex-go-sdk/types"
)

type rpcError struct {
	Code    int             `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcEnvelope struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type appEvent struct {
	ID     *int64
	Method string
	Params json.RawMessage
}

// AppServerExec handles execution of the Codex app server.
type AppServerExec struct {
	executablePath string
	args           []string
	envOverride    map[string]string
	clientInfo     types.ClientInfo
	baseURL        string
	apiKey         string

	verbose       bool
	verboseWriter io.Writer

	startOnce sync.Once
	startErr  error

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmdMu  sync.RWMutex

	writeMu sync.Mutex

	nextID    int64
	pending   map[int64]chan rpcEnvelope
	pendingMu sync.Mutex

	subsMu sync.RWMutex
	subs   map[chan appEvent]struct{}

	knownThreadsMu sync.Mutex
	knownThreads   map[string]struct{}

	closeOnce sync.Once
	closed    atomic.Bool
}

// NewAppServerExec creates a new AppServerExec instance.
func NewAppServerExec(
	executablePath string,
	args []string,
	envOverride map[string]string,
	clientInfo types.ClientInfo,
	baseURL string,
	apiKey string,
) *AppServerExec {
	if executablePath == "" {
		executablePath = findCodexPath()
	}
	args = append([]string{"app-server"}, args...)
	if clientInfo.Name == "" {
		clientInfo.Name = "codex-go-sdk"
		clientInfo.Version = codexSDKVersion
	}

	return &AppServerExec{
		executablePath: executablePath,
		args:           args,
		envOverride:    envOverride,
		clientInfo:     clientInfo,
		baseURL:        baseURL,
		apiKey:         apiKey,
		pending:        make(map[int64]chan rpcEnvelope),
		subs:           make(map[chan appEvent]struct{}),
		knownThreads:   make(map[string]struct{}),
	}
}

// EnableVerbose enables debug logging for the app server exec.
func (a *AppServerExec) EnableVerbose(writer io.Writer) {
	a.verbose = true
	if writer != nil {
		a.verboseWriter = writer
	} else {
		a.verboseWriter = os.Stderr
	}
}

func (a *AppServerExec) logf(format string, args ...interface{}) {
	if !a.verbose {
		return
	}
	if a.verboseWriter == nil {
		a.verboseWriter = os.Stderr
	}
	fmt.Fprintf(a.verboseWriter, format+"\n", args...)
}

func (a *AppServerExec) ensureStarted() error {
	if a.closed.Load() {
		return errors.New("app server exec is closed")
	}
	a.startOnce.Do(func() {
		a.startErr = a.start()
	})
	return a.startErr
}

func (a *AppServerExec) start() error {
	// #nosec G204 -- Executable path and args are user-provided by design in SDK integrations.
	cmd := exec.CommandContext(context.Background(), a.executablePath, a.args...)
	configurePlatformCommand(cmd)

	// Set up environment
	env := os.Environ()
	if a.envOverride != nil {
		env = []string{}
		for k, v := range a.envOverride {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	foundOriginator := false
	for _, e := range env {
		if strings.HasPrefix(e, envInternalOriginatorOverrideKey+"=") {
			foundOriginator = true
			break
		}
	}
	if !foundOriginator {
		env = append(env, envInternalOriginatorOverrideKey+"="+defaultOriginator)
	}
	if a.baseURL != "" {
		env = append(env, fmt.Sprintf("%s=%s", envBaseURLKey, a.baseURL))
	}
	if a.apiKey != "" {
		env = append(env, fmt.Sprintf("%s=%s", envCodexAPIEnvVar, a.apiKey))
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create app server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create app server stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create app server stderr: %w", err)
	}

	startErr := cmd.Start()
	if startErr != nil {
		return fmt.Errorf("failed to start app server: %w", startErr)
	}

	a.cmdMu.Lock()
	a.cmd = cmd
	a.cmdMu.Unlock()
	a.stdin = stdin
	a.stdout = stdout

	go a.readStdout()
	go a.readStderr(stderr)

	// Initialize protocol.
	ctx, cancel := context.WithTimeout(context.Background(), defaultInitTimeout)
	defer cancel()
	initErr := a.initialize(ctx)
	if initErr != nil {
		return initErr
	}
	return nil
}

// ProcessID returns the app-server PID, or 0 before the process starts.
func (a *AppServerExec) ProcessID() int {
	if a == nil {
		return 0
	}
	a.cmdMu.RLock()
	defer a.cmdMu.RUnlock()
	if a.cmd == nil || a.cmd.Process == nil {
		return 0
	}
	return a.cmd.Process.Pid
}

func (a *AppServerExec) readStdout() {
	reader := bufio.NewReader(a.stdout)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if line != "" {
				a.handleLine(line)
			}
		}
		if err != nil {
			if err != io.EOF && !isExpectedReadCloseError(err) {
				a.logf("app server stdout error: %v", err)
			}
			return
		}
	}
}

func isExpectedReadCloseError(err error) bool {
	return err == nil || errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}

func (a *AppServerExec) readStderr(stderr io.Reader) {
	reader := bufio.NewReader(stderr)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			a.logf("app server stderr: %s", strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			if err != io.EOF {
				a.logf("app server stderr error: %v", err)
			}
			return
		}
	}
}

func (a *AppServerExec) handleLine(line string) {
	var envelope rpcEnvelope
	unmarshalErr := json.Unmarshal([]byte(line), &envelope)
	if unmarshalErr != nil {
		a.logf("app server: failed to parse line: %v", unmarshalErr)
		return
	}
	if envelope.Method != "" {
		if envelope.Method == "thread/closed" {
			a.forgetKnownThread(threadIDFromRawParams(envelope.Params))
		}
		a.dispatchEvent(appEvent{ID: envelope.ID, Method: envelope.Method, Params: envelope.Params})
		return
	}
	if envelope.ID != nil {
		a.pendingMu.Lock()
		ch := a.pending[*envelope.ID]
		a.pendingMu.Unlock()
		if ch != nil {
			ch <- envelope
		}
		return
	}
}

func threadIDFromRawParams(params json.RawMessage) string {
	var value struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value.ThreadID)
}

func (a *AppServerExec) forgetKnownThread(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	a.knownThreadsMu.Lock()
	delete(a.knownThreads, threadID)
	a.knownThreadsMu.Unlock()
}

func (a *AppServerExec) dispatchEvent(event appEvent) {
	a.subsMu.RLock()
	for ch := range a.subs {
		select {
		case ch <- event:
		default:
			// Drop if the subscriber is too slow.
		}
	}
	a.subsMu.RUnlock()
}

func (a *AppServerExec) subscribe() chan appEvent {
	ch := make(chan appEvent, appServerSubscriberBuffer)
	a.subsMu.Lock()
	a.subs[ch] = struct{}{}
	a.subsMu.Unlock()
	return ch
}

func (a *AppServerExec) unsubscribe(ch chan appEvent) {
	a.subsMu.Lock()
	if _, ok := a.subs[ch]; ok {
		delete(a.subs, ch)
		close(ch)
	}
	a.subsMu.Unlock()
}

func (a *AppServerExec) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := atomic.AddInt64(&a.nextID, 1)
	respCh := make(chan rpcEnvelope, 1)

	a.pendingMu.Lock()
	a.pending[id] = respCh
	a.pendingMu.Unlock()

	sendErr := a.sendRequest(id, method, params)
	if sendErr != nil {
		a.pendingMu.Lock()
		delete(a.pending, id)
		a.pendingMu.Unlock()
		return nil, sendErr
	}

	select {
	case resp := <-respCh:
		a.pendingMu.Lock()
		delete(a.pending, id)
		a.pendingMu.Unlock()
		if resp.Error != nil {
			return nil, fmt.Errorf("app server error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		a.pendingMu.Lock()
		delete(a.pending, id)
		a.pendingMu.Unlock()
		return nil, ctx.Err()
	}
}

// RPCCall executes a raw app-server RPC request and returns the result payload.
func (a *AppServerExec) RPCCall(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if err := a.ensureStarted(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return a.call(ctx, method, params)
}

// UnsubscribeThread removes this connection's subscription and evicts the
// local loaded-thread cache so a later turn performs thread/resume.
func (a *AppServerExec) UnsubscribeThread(ctx context.Context, threadID string) (*types.ThreadUnsubscribeResponse, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return &types.ThreadUnsubscribeResponse{Status: types.ThreadUnsubscribeStatusNotLoaded}, nil
	}
	if err := a.ensureStarted(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := a.call(ctx, "thread/unsubscribe", map[string]interface{}{"threadId": threadID})
	if err != nil {
		return nil, err
	}
	return a.applyThreadUnsubscribeResult(threadID, result)
}

func (a *AppServerExec) applyThreadUnsubscribeResult(threadID string, result json.RawMessage) (*types.ThreadUnsubscribeResponse, error) {
	var response types.ThreadUnsubscribeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	a.forgetKnownThread(threadID)
	return &response, nil
}

// LoginChatGPTDeviceCode starts account/login/start with type chatgptDeviceCode and streams login progress.
func (a *AppServerExec) LoginChatGPTDeviceCode(ctx context.Context) <-chan types.LoginEvent {
	output := make(chan types.LoginEvent)
	go func() {
		defer close(output)
		if err := a.loginChatGPTDeviceCode(ctx, output); err != nil {
			output <- types.LoginEvent{Status: "error", Error: err.Error()}
		}
	}()
	return output
}

func (a *AppServerExec) notify(method string, params interface{}) error {
	return a.sendRequest(0, method, params)
}

func (a *AppServerExec) sendRequest(id int64, method string, params interface{}) error {
	if a.closed.Load() {
		return errors.New("app server exec is closed")
	}
	payload := map[string]interface{}{
		"method": method,
	}
	if id != 0 {
		payload["id"] = id
	}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_, writeErr := a.stdin.Write(append(data, '\n'))
	if writeErr != nil {
		return writeErr
	}
	return nil
}

func (a *AppServerExec) sendResponse(id int64, result interface{}) error {
	if a.closed.Load() {
		return errors.New("app server exec is closed")
	}
	payload := map[string]interface{}{
		"id":     id,
		"result": result,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_, writeErr := a.stdin.Write(append(data, '\n'))
	return writeErr
}

// Close terminates the long-lived app-server process and releases resources.
func (a *AppServerExec) Close() error {
	var closeErr error

	a.closeOnce.Do(func() {
		a.closed.Store(true)
		a.failPendingCalls()
		a.closeSubscribers()

		if a.stdin != nil {
			if err := a.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) && closeErr == nil {
				closeErr = err
			}
		}
		if a.stdout != nil {
			if err := a.stdout.Close(); err != nil && !errors.Is(err, os.ErrClosed) && closeErr == nil {
				closeErr = err
			}
		}
		a.cmdMu.RLock()
		cmd := a.cmd
		a.cmdMu.RUnlock()
		if cmd != nil {
			if cmd.Process != nil {
				if err := killPlatformCommand(cmd); err != nil && !errors.Is(err, os.ErrProcessDone) && closeErr == nil {
					closeErr = err
				}
			}
			if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) && closeErr == nil {
					closeErr = err
				}
			}
		}
	})

	return closeErr
}

func (a *AppServerExec) failPendingCalls() {
	a.pendingMu.Lock()
	pending := a.pending
	a.pending = make(map[int64]chan rpcEnvelope)
	a.pendingMu.Unlock()

	envelope := rpcEnvelope{
		Error: &rpcError{
			Message: "app server exec is closed",
		},
	}
	for _, ch := range pending {
		select {
		case ch <- envelope:
		default:
		}
		close(ch)
	}
}

func (a *AppServerExec) closeSubscribers() {
	a.subsMu.Lock()
	subs := a.subs
	a.subs = make(map[chan appEvent]struct{})
	a.subsMu.Unlock()

	for ch := range subs {
		close(ch)
	}
}

func (a *AppServerExec) initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"clientInfo": map[string]string{
			"name":    a.clientInfo.Name,
			"version": a.clientInfo.Version,
		},
		"capabilities": map[string]interface{}{
			"experimentalApi": true,
		},
	}
	_, initErr := a.call(ctx, "initialize", params)
	if initErr != nil {
		return initErr
	}
	return a.notify("initialized", nil)
}

// ListModels fetches the app server model catalog.
func (a *AppServerExec) ListModels(ctx context.Context, params types.ModelListParams) (*types.ModelListResponse, error) {
	if err := a.ensureStarted(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := a.call(ctx, "model/list", params)
	if err != nil {
		return nil, err
	}
	var resp types.ModelListResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Run executes the app server turn and returns a channel of JSONL event lines.
func (a *AppServerExec) Run(args CodexExecArgs) <-chan ExecResult {
	output := make(chan ExecResult)

	go func() {
		defer close(output)
		turnErr := a.runTurn(args, output)
		if turnErr != nil {
			output <- ExecResult{Error: turnErr}
		}
	}()

	return output
}

// RunReview executes review/start and streams the resulting turn events.
func (a *AppServerExec) RunReview(
	args CodexExecArgs,
	params types.ReviewStartParams,
) <-chan ExecResult {
	output := make(chan ExecResult)

	go func() {
		defer close(output)
		if err := a.runReview(args, params, output); err != nil {
			output <- ExecResult{Error: err}
		}
	}()

	return output
}

// RunShellCommand executes thread/shellCommand and streams the resulting thread events.
func (a *AppServerExec) RunShellCommand(args CodexExecArgs, command string) <-chan ExecResult {
	output := make(chan ExecResult)

	go func() {
		defer close(output)
		if err := a.runShellCommand(args, command, output); err != nil {
			output <- ExecResult{Error: err}
		}
	}()

	return output
}

// RunCompact starts manual context compaction and streams progress until it completes.
func (a *AppServerExec) RunCompact(args CodexExecArgs) <-chan ExecResult {
	output := make(chan ExecResult)

	go func() {
		defer close(output)
		if err := a.runCompact(args, output); err != nil {
			output <- ExecResult{Error: err}
		}
	}()

	return output
}

// RunGoalSet updates the thread goal and streams any goal continuation turn events.
func (a *AppServerExec) RunGoalSet(
	args CodexExecArgs,
	params types.ThreadGoalSetParams,
) <-chan ExecResult {
	output := make(chan ExecResult)

	go func() {
		defer close(output)
		if err := a.runGoalSet(args, params, output); err != nil {
			output <- ExecResult{Error: err}
		}
	}()

	return output
}

func (a *AppServerExec) SubscribeThreadEvents(args CodexExecArgs) <-chan ExecResult {
	output := make(chan ExecResult)

	go func() {
		defer close(output)
		if err := a.subscribeThreadEvents(args, output); err != nil {
			output <- ExecResult{Error: err}
		}
	}()

	return output
}

func (a *AppServerExec) runTurn(args CodexExecArgs, output chan ExecResult) error {
	startErr := a.ensureStarted()
	if startErr != nil {
		return startErr
	}

	ctx := args.Context
	if ctx == nil {
		ctx = context.Background()
	}

	threadID, isNewThread, err := a.ensureThread(ctx, args)
	if err != nil {
		return err
	}

	if isNewThread {
		threadStarted := map[string]interface{}{
			"type":     "thread.started",
			"threadId": threadID,
		}
		line, marshalErr := json.Marshal(threadStarted)
		if marshalErr == nil {
			output <- ExecResult{Line: string(line)}
		}
	}

	turnID, err := a.startTurn(ctx, threadID, args)
	if err != nil {
		return err
	}

	if turnID == "" {
		// If the server did not return a turn id, rely on events to detect completion.
		a.logf("app server: missing turn id in response")
	}

	return a.streamTurn(ctx, threadID, turnID, args, output)
}

func (a *AppServerExec) subscribeThreadEvents(args CodexExecArgs, output chan ExecResult) error {
	startErr := a.ensureStarted()
	if startErr != nil {
		return startErr
	}

	ctx := args.Context
	if ctx == nil {
		ctx = context.Background()
	}
	threadID, _, err := a.ensureThread(ctx, args)
	if err != nil {
		return err
	}

	sub := a.subscribe()
	defer a.unsubscribe(sub)

	state := &turnState{
		items: make(map[string]map[string]interface{}),
	}
	interruptCh := ctx.Done()
	cancelRequested := false
	interruptSent := false
	turnID := ""
	var interruptTimer *time.Timer
	var interruptDeadline <-chan time.Time
	defer func() {
		if interruptTimer != nil {
			stopTimer(interruptTimer)
		}
	}()
	interrupt := func() error {
		if interruptSent {
			return nil
		}
		if turnID == "" {
			return ctx.Err()
		}
		if interruptTimer != nil {
			stopTimer(interruptTimer)
			interruptTimer = nil
			interruptDeadline = nil
		}
		interruptCtx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
		err := a.interruptTurn(interruptCtx, threadID, turnID)
		cancel()
		if err != nil {
			return err
		}
		interruptSent = true
		return nil
	}
	for {
		select {
		case <-interruptCh:
			interruptCh = nil
			cancelRequested = true
			if turnID != "" {
				if err := interrupt(); err != nil {
					return err
				}
				continue
			}
			interruptTimer = time.NewTimer(defaultInterruptTimeout)
			interruptDeadline = interruptTimer.C
		case <-interruptDeadline:
			return ctx.Err()
		case event, ok := <-sub:
			if !ok {
				return finishStreamTurn(interruptSent, ctx.Err())
			}
			if !eventMatchesTurn(event, threadID, "") {
				continue
			}
			if turnID == "" {
				turnID = eventTurnID(event)
			}
			if cancelRequested && !interruptSent && turnID != "" {
				if err := interrupt(); err != nil {
					return err
				}
			}
			if args.ApprovalHandler != nil && isApprovalRequestedEvent(event.Method) {
				a.submitApproval(ctx, event, args.ApprovalHandler)
			}
			if args.AskUserHandler != nil && isRequestUserInputEvent(event.Method) {
				a.submitAskUserResponse(event, args.AskUserHandler)
			}
			line, done, err := appEventToLegacyLine(event, state)
			if err != nil {
				return err
			}
			if line != "" {
				output <- ExecResult{Line: line}
			}
			if done {
				return finishStreamTurn(interruptSent, ctx.Err())
			}
		}
	}
}

func (a *AppServerExec) runCompact(args CodexExecArgs, output chan ExecResult) error {
	startErr := a.ensureStarted()
	if startErr != nil {
		return startErr
	}

	ctx := args.Context
	if ctx == nil {
		ctx = context.Background()
	}

	threadID, isNewThread, err := a.ensureThread(ctx, args)
	if err != nil {
		return err
	}

	if isNewThread {
		threadStarted := map[string]interface{}{
			"type":     "thread.started",
			"threadId": threadID,
		}
		line, marshalErr := json.Marshal(threadStarted)
		if marshalErr == nil {
			output <- ExecResult{Line: string(line)}
		}
	}

	sub := a.subscribe()
	defer a.unsubscribe(sub)

	_, err = a.call(ctx, "thread/compact/start", map[string]interface{}{
		"threadId": threadID,
	})
	if err != nil {
		return err
	}

	state := &turnState{
		items: make(map[string]map[string]interface{}),
	}
	compactItemID := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-sub:
			if !ok {
				return nil
			}
			if !eventMatchesTurn(event, threadID, "") {
				continue
			}
			line, done, err := appEventToLegacyLine(event, state)
			if err != nil {
				return err
			}
			if line != "" {
				output <- ExecResult{Line: line}
			}
			if itemID, ok := contextCompactionItemID(event); ok {
				if compactItemID == "" {
					compactItemID = itemID
				}
				if event.Method == "item/completed" && (compactItemID == "" || itemID == compactItemID) {
					return nil
				}
			}
			if done && compactItemID != "" {
				return nil
			}
		}
	}
}

func (a *AppServerExec) loginChatGPTDeviceCode(ctx context.Context, output chan types.LoginEvent) error {
	if err := a.ensureStarted(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	sub := a.subscribe()
	defer a.unsubscribe(sub)

	var response types.LoginAccountDeviceCodeResponse
	result, err := a.call(ctx, "account/login/start", map[string]interface{}{
		"type": "chatgptDeviceCode",
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return err
	}
	if strings.TrimSpace(response.LoginID) == "" {
		return errors.New("app server did not return login id")
	}
	output <- types.LoginEvent{
		Status:          "pending",
		LoginID:         response.LoginID,
		VerificationURL: response.VerificationURL,
		UserCode:        response.UserCode,
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-sub:
			if !ok {
				return nil
			}
			switch event.Method {
			case "account/updated":
				updated, ok, err := parseAccountUpdatedEvent(event)
				if err != nil {
					return err
				}
				if ok {
					output <- types.LoginEvent{
						Status:         "account_updated",
						LoginID:        response.LoginID,
						AccountUpdated: &updated,
					}
				}
			case "account/login/completed":
				completed, ok, err := parseAccountLoginCompletedEvent(event)
				if err != nil {
					return err
				}
				if !ok || completed.LoginID == nil || strings.TrimSpace(*completed.LoginID) != response.LoginID {
					continue
				}
				if completed.Success {
					output <- types.LoginEvent{Status: "success", LoginID: response.LoginID}
				} else {
					msg := "login failed"
					if completed.Error != nil && strings.TrimSpace(*completed.Error) != "" {
						msg = strings.TrimSpace(*completed.Error)
					}
					output <- types.LoginEvent{Status: "error", LoginID: response.LoginID, Error: msg}
				}
				return nil
			}
		}
	}
}

func (a *AppServerExec) runShellCommand(
	args CodexExecArgs,
	command string,
	output chan ExecResult,
) error {
	startErr := a.ensureStarted()
	if startErr != nil {
		return startErr
	}

	ctx := args.Context
	if ctx == nil {
		ctx = context.Background()
	}

	threadID, isNewThread, err := a.ensureThread(ctx, args)
	if err != nil {
		return err
	}

	if isNewThread {
		threadStarted := map[string]interface{}{
			"type":     "thread.started",
			"threadId": threadID,
		}
		line, marshalErr := json.Marshal(threadStarted)
		if marshalErr == nil {
			output <- ExecResult{Line: string(line)}
		}
	}

	sub := a.subscribe()
	defer a.unsubscribe(sub)

	_, err = a.call(ctx, "thread/shellCommand", types.ThreadShellCommandParams{
		ThreadID: threadID,
		Command:  command,
	})
	if err != nil {
		return err
	}

	state := &turnState{
		items: make(map[string]map[string]interface{}),
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-sub:
			if !ok {
				return nil
			}
			done, err := a.handleTurnEvent(ctx, event, threadID, "", args, state, output)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

func (a *AppServerExec) runGoalSet(
	args CodexExecArgs,
	params types.ThreadGoalSetParams,
	output chan ExecResult,
) error {
	startErr := a.ensureStarted()
	if startErr != nil {
		return startErr
	}

	ctx := args.Context
	if ctx == nil {
		ctx = context.Background()
	}

	threadID, isNewThread, err := a.ensureThread(ctx, args)
	if err != nil {
		return err
	}
	params.ThreadID = threadID

	if isNewThread {
		threadStarted := map[string]interface{}{
			"type":     "thread.started",
			"threadId": threadID,
		}
		line, marshalErr := json.Marshal(threadStarted)
		if marshalErr == nil {
			output <- ExecResult{Line: string(line)}
		}
	}

	sub := a.subscribe()
	defer a.unsubscribe(sub)

	result, err := a.call(ctx, "thread/goal/set", params)
	if err != nil {
		return err
	}

	var response types.ThreadGoalSetResponse
	var goal *types.ThreadGoal
	if len(result) > 0 {
		if unmarshalErr := json.Unmarshal(result, &response); unmarshalErr != nil {
			return unmarshalErr
		}
		goal = &response.Goal
	}

	return a.streamGoalContinuation(ctx, threadID, args, goal, sub, output)
}

func threadSettingsCollaborationModeLineMatches(
	raw json.RawMessage,
	threadID string,
	mode types.CollaborationModeKind,
) bool {
	var payload struct {
		ThreadID       string `json:"threadId"`
		ThreadSettings struct {
			CollaborationMode *types.CollaborationMode `json:"collaborationMode"`
		} `json:"threadSettings"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if threadID != "" && payload.ThreadID != "" && payload.ThreadID != threadID {
		return false
	}
	return payload.ThreadSettings.CollaborationMode != nil &&
		payload.ThreadSettings.CollaborationMode.Mode == mode
}

func (a *AppServerExec) streamGoalContinuation(
	ctx context.Context,
	threadID string,
	args CodexExecArgs,
	goal *types.ThreadGoal,
	sub chan appEvent,
	output chan ExecResult,
) error {
	state := &turnState{
		items: make(map[string]map[string]interface{}),
	}
	startTimer := time.NewTimer(defaultGoalContinuationStartTimeout)
	defer startTimer.Stop()
	startDeadline := startTimer.C

	sawTurn := false
	interruptCh := ctx.Done()
	interruptPending := false
	turnID := ""
	goalStatus := goalStatusFromGoal(goal)
	goalCleared := false
	activeGoalTurn := false

	for {
		select {
		case <-startDeadline:
			if !sawTurn {
				if goalStatus == types.ThreadGoalStatusActive {
					startDeadline = nil
					continue
				}
				return emitSyntheticGoalMessage(output, goal)
			}
		case <-interruptCh:
			interruptCh = nil
			a.pauseGoalBestEffort(threadID)
			if err := a.interruptGoalTurnBestEffort(threadID, turnID); err != nil {
				return err
			}
			interruptPending = true
			return finishStreamTurn(interruptPending, ctx.Err())
		case event, ok := <-sub:
			if !ok {
				return finishStreamTurn(interruptPending, ctx.Err())
			}
			if isTurnStartedEvent(event.Method) && eventMatchesTurn(event, threadID, "") {
				if id := eventTurnID(event); id != "" {
					turnID = id
				}
				activeGoalTurn = true
				sawTurn = true
				stopTimer(startTimer)
				startDeadline = nil
			}
			if status, ok := goalStatusFromEvent(event, threadID); ok {
				goalStatus = status
				goalCleared = false
			}
			if goalClearedFromEvent(event, threadID) {
				goalCleared = true
			}
			if isTurnDoneEvent(event.Method) &&
				eventMatchesTurn(event, threadID, turnID) &&
				goalStatus == types.ThreadGoalStatusActive &&
				!goalCleared {
				activeGoalTurn = false
				turnID = ""
				continue
			}
			done, err := a.handleTurnEvent(ctx, event, threadID, turnID, args, state, output)
			if err != nil {
				return err
			}
			if done {
				activeGoalTurn = false
				if goalStatus == types.ThreadGoalStatusActive && !goalCleared {
					turnID = ""
					continue
				}
				return finishStreamTurn(interruptPending, ctx.Err())
			}
			if !activeGoalTurn && (goalCleared || terminalGoalStatus(goalStatus)) {
				return finishStreamTurn(interruptPending, ctx.Err())
			}
		}
	}
}

func goalStatusFromGoal(goal *types.ThreadGoal) types.ThreadGoalStatus {
	if goal == nil {
		return ""
	}
	return goal.Status
}

func goalStatusFromEvent(event appEvent, threadID string) (types.ThreadGoalStatus, bool) {
	if event.Method != "thread/goal/updated" {
		return "", false
	}
	var payload struct {
		ThreadID string `json:"threadId"`
		Goal     struct {
			Status types.ThreadGoalStatus `json:"status"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return "", false
	}
	if threadID != "" && payload.ThreadID != "" && payload.ThreadID != threadID {
		return "", false
	}
	if payload.Goal.Status == "" {
		return "", false
	}
	return payload.Goal.Status, true
}

func goalClearedFromEvent(event appEvent, threadID string) bool {
	if event.Method != "thread/goal/cleared" {
		return false
	}
	var payload struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return false
	}
	return threadID == "" || payload.ThreadID == "" || payload.ThreadID == threadID
}

func terminalGoalStatus(status types.ThreadGoalStatus) bool {
	switch status {
	case types.ThreadGoalStatusPaused,
		types.ThreadGoalStatusBlocked,
		types.ThreadGoalStatusUsageLimited,
		types.ThreadGoalStatusBudgetLimited,
		types.ThreadGoalStatusComplete:
		return true
	default:
		return false
	}
}

func (a *AppServerExec) pauseGoalBestEffort(threadID string) {
	if strings.TrimSpace(threadID) == "" {
		return
	}
	status := types.ThreadGoalStatusPaused
	ctx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
	defer cancel()
	_, _ = a.call(ctx, "thread/goal/set", types.ThreadGoalSetParams{
		ThreadID: threadID,
		Status:   &status,
	})
}

func (a *AppServerExec) interruptGoalTurnBestEffort(threadID string, turnID string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
	err := a.interruptTurn(ctx, threadID, turnID)
	cancel()
	if err == nil || isNoActiveTurnToInterruptError(err) {
		return nil
	}
	nextTurnID := activeTurnIDFromMismatchError(err)
	if nextTurnID == "" || nextTurnID == turnID {
		return nil
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultInterruptTimeout)
	err = a.interruptTurn(ctx, threadID, nextTurnID)
	cancel()
	if err == nil || isNoActiveTurnToInterruptError(err) || activeTurnIDFromMismatchError(err) != "" {
		return nil
	}
	return nil
}

func isNoActiveTurnToInterruptError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no active turn to interrupt")
}

var activeTurnMismatchPattern = regexp.MustCompile("expected active turn id `?([^`\\s]+)`? but found `?([^`\\s]+)`?")

func activeTurnIDFromMismatchError(err error) string {
	if err == nil {
		return ""
	}
	matches := activeTurnMismatchPattern.FindStringSubmatch(err.Error())
	if len(matches) < 3 {
		return ""
	}
	return strings.TrimSpace(matches[2])
}

func emitSyntheticGoalMessage(output chan ExecResult, goal *types.ThreadGoal) error {
	itemID := fmt.Sprintf("msg-synth-%d", atomic.AddUint64(&syntheticTurnCounter, 1))
	message := formatGoalUpdatedMessage(goal)
	lines := []map[string]interface{}{
		{"type": "turn.started"},
		{"type": "item.started", "item": map[string]interface{}{"id": itemID, "type": "agentMessage", "text": ""}},
		{"type": "item.updated", "item": map[string]interface{}{"id": itemID, "type": "agentMessage", "text": message}},
		{"type": "item.completed", "item": map[string]interface{}{"id": itemID, "type": "agentMessage", "text": message}},
		{"type": "turn.completed", "usage": map[string]interface{}{"inputTokens": 0, "cachedInputTokens": 0, "outputTokens": 0}},
	}
	for _, payload := range lines {
		line, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		output <- ExecResult{Line: string(line)}
	}
	return nil
}

func isTurnStartedEvent(method string) bool {
	return method == "turn/started"
}

func isTurnDoneEvent(method string) bool {
	return method == "turn/completed" || method == "turn/failed"
}

func eventTurnID(event appEvent) string {
	var meta struct {
		TurnID string `json:"turnId"`
		Turn   *struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(event.Params, &meta); err != nil {
		return ""
	}
	if meta.TurnID != "" {
		return meta.TurnID
	}
	if meta.Turn != nil {
		return meta.Turn.ID
	}
	return ""
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (a *AppServerExec) runReview(
	args CodexExecArgs,
	params types.ReviewStartParams,
	output chan ExecResult,
) error {
	startErr := a.ensureStarted()
	if startErr != nil {
		return startErr
	}

	ctx := args.Context
	if ctx == nil {
		ctx = context.Background()
	}

	threadID, isNewThread, err := a.ensureThread(ctx, args)
	if err != nil {
		return err
	}
	params.ThreadID = threadID

	if isNewThread {
		threadStarted := map[string]interface{}{
			"type":     "thread.started",
			"threadId": threadID,
		}
		line, marshalErr := json.Marshal(threadStarted)
		if marshalErr == nil {
			output <- ExecResult{Line: string(line)}
		}
	}

	result, err := a.call(ctx, "review/start", params)
	if err != nil {
		return err
	}
	turnID := extractTurnID(result)
	if turnID == "" {
		a.logf("app server: missing review turn id in response")
	}

	return a.streamTurn(ctx, threadID, turnID, args, output)
}

func (a *AppServerExec) startTurn(ctx context.Context, threadID string, args CodexExecArgs) (string, error) {
	turnParams, err := a.buildTurnParams(threadID, args)
	if err != nil {
		return "", err
	}

	result, err := a.call(ctx, "turn/start", turnParams)
	if err != nil {
		return "", err
	}

	return extractTurnID(result), nil
}

func (a *AppServerExec) buildTurnParams(threadID string, args CodexExecArgs) (map[string]interface{}, error) {
	inputItems := buildInputItems(args)

	turnParams := map[string]interface{}{
		"threadId": threadID,
		"input":    inputItems,
		"stream":   true,
	}
	if args.Model != "" {
		turnParams["model"] = args.Model
	}
	switch normalizeFastService(args.FastService) {
	case "on":
		turnParams["serviceTier"] = "fast"
	case "off":
		turnParams["serviceTier"] = nil
	}
	if effort := normalizeReasoningEffortForModel(args.Model, args.ModelReasoningEffort); effort != "" {
		turnParams["effort"] = effort
	}
	if args.WorkingDirectory != "" {
		turnParams["cwd"] = args.WorkingDirectory
	}
	if sandbox := buildSandboxPolicy(args); sandbox != nil {
		turnParams["sandboxPolicy"] = sandbox
	}
	if approval := mapApprovalPolicy(args.ApprovalPolicy); approval != "" {
		turnParams["approvalPolicy"] = approval
	}
	if args.CollaborationMode != nil {
		turnParams["collaborationMode"] = buildCollaborationMode(
			args.CollaborationMode,
			args.Model,
			args.ModelReasoningEffort,
		)
	}

	schema, hasSchema, schemaErr := loadOutputSchema(args.OutputSchemaFile)
	if schemaErr != nil {
		return nil, schemaErr
	}
	if hasSchema {
		turnParams["outputSchema"] = schema
	}

	return turnParams, nil
}

func buildCollaborationMode(
	mode *types.CollaborationMode,
	model string,
	effort string,
) *types.CollaborationMode {
	if mode == nil {
		return nil
	}
	next := *mode
	if strings.TrimSpace(next.Settings.Model) == "" {
		next.Settings.Model = strings.TrimSpace(model)
	}
	requestedEffort := effort
	if next.Settings.ReasoningEffort != nil {
		requestedEffort = string(*next.Settings.ReasoningEffort)
	} else if strings.TrimSpace(requestedEffort) == "" {
		return &next
	}
	value := types.ModelReasoningEffort(normalizeReasoningEffortForModel(next.Settings.Model, requestedEffort))
	next.Settings.ReasoningEffort = &value
	return &next
}

func (a *AppServerExec) streamTurn(
	ctx context.Context,
	threadID string,
	turnID string,
	args CodexExecArgs,
	output chan ExecResult,
) error {
	sub := a.subscribe()
	defer a.unsubscribe(sub)

	state := &turnState{
		items: make(map[string]map[string]interface{}),
	}
	interruptCh := ctx.Done()
	interruptPending := false

	for {
		select {
		case <-interruptCh:
			interruptCh = nil
			if turnID == "" {
				return ctx.Err()
			}
			interruptCtx, cancel := context.WithTimeout(context.Background(), defaultInterruptTimeout)
			err := a.interruptTurn(interruptCtx, threadID, turnID)
			cancel()
			if err != nil {
				return err
			}
			interruptPending = true
		case event, ok := <-sub:
			if !ok {
				return finishStreamTurn(interruptPending, ctx.Err())
			}
			done, err := a.handleTurnEvent(ctx, event, threadID, turnID, args, state, output)
			if err != nil {
				return err
			}
			if done {
				return finishStreamTurn(interruptPending, ctx.Err())
			}
		}
	}
}

func finishStreamTurn(interruptPending bool, cancelErr error) error {
	if interruptPending {
		return cancelErr
	}
	return nil
}

func (a *AppServerExec) interruptTurn(ctx context.Context, threadID string, turnID string) error {
	a.logf("app server: interrupting turn %s in thread %s", turnID, threadID)
	_, err := a.call(ctx, "turn/interrupt", map[string]interface{}{
		"threadId": threadID,
		"turnId":   turnID,
	})
	return err
}

func isApprovalRequestedEvent(method string) bool {
	return method == "item/commandExecution/requestApproval" ||
		method == "item/fileChange/requestApproval" ||
		method == "item/commandExecution/approvalRequested" ||
		method == "item/fileChange/approvalRequested"
}

func isRequestUserInputEvent(method string) bool {
	return method == "item/tool/requestUserInput"
}

//go:embed current_version
var codexSDKVersion string

const (
	appServerSubscriberBuffer           = 256
	defaultInitTimeout                  = 10 * time.Second
	defaultInterruptTimeout             = 5 * time.Second
	defaultGoalContinuationStartTimeout = 2 * time.Second
)

func (a *AppServerExec) ensureThread(ctx context.Context, args CodexExecArgs) (string, bool, error) {
	requested := args.ThreadId
	if requested == nil || *requested == "" {
		params := buildThreadStartParams(args)
		result, err := a.call(ctx, "thread/start", params)
		if err != nil {
			return "", false, err
		}
		threadID := extractThreadID(result)
		if threadID == "" {
			return "", false, errors.New("app server did not return thread id")
		}
		a.knownThreadsMu.Lock()
		a.knownThreads[threadID] = struct{}{}
		a.knownThreadsMu.Unlock()
		return threadID, true, nil
	}

	threadID := *requested
	a.knownThreadsMu.Lock()
	_, known := a.knownThreads[threadID]
	a.knownThreadsMu.Unlock()
	if !known {
		params, paramsErr := a.buildThreadResumeParams(ctx, threadID, args)
		if paramsErr != nil {
			return "", false, paramsErr
		}
		_, err := a.call(ctx, "thread/resume", params)
		if err != nil {
			return "", false, err
		}
		a.knownThreadsMu.Lock()
		a.knownThreads[threadID] = struct{}{}
		a.knownThreadsMu.Unlock()
	}
	return threadID, false, nil
}

func buildThreadStartParams(args CodexExecArgs) map[string]interface{} {
	params := map[string]interface{}{}
	appendThreadContextParams(params, args, args.ModelProvider)
	return params
}

func (a *AppServerExec) buildThreadResumeParams(ctx context.Context, threadID string, args CodexExecArgs) (map[string]interface{}, error) {
	modelProvider := strings.TrimSpace(args.ModelProvider)
	if modelProvider == "" {
		var err error
		modelProvider, err = a.currentModelProvider(ctx, args.WorkingDirectory)
		if err != nil {
			return nil, err
		}
	}
	return buildThreadResumeParams(threadID, args, modelProvider), nil
}

func buildThreadResumeParams(threadID string, args CodexExecArgs, modelProvider string) map[string]interface{} {
	params := map[string]interface{}{
		"threadId": threadID,
	}
	appendThreadContextParams(params, args, modelProvider)
	return params
}

func appendThreadContextParams(params map[string]interface{}, args CodexExecArgs, modelProvider string) {
	if args.Model != "" {
		params["model"] = args.Model
	}
	if modelProvider != "" {
		params["modelProvider"] = modelProvider
	}
	switch normalizeFastService(args.FastService) {
	case "on":
		params["serviceTier"] = "fast"
	case "off":
		params["serviceTier"] = nil
	}
	if args.WorkingDirectory != "" {
		params["cwd"] = args.WorkingDirectory
	}
	if args.DeveloperInstructions != "" {
		params["developerInstructions"] = args.DeveloperInstructions
	}
	if args.ApprovalPolicy != "" {
		params["approvalPolicy"] = args.ApprovalPolicy
	}
	if args.SandboxMode != "" {
		params["sandbox"] = args.SandboxMode
	}
	if args.CollaborationMode != nil {
		params["collaborationMode"] = buildCollaborationMode(
			args.CollaborationMode,
			args.Model,
			args.ModelReasoningEffort,
		)
	}
}

func (a *AppServerExec) currentModelProvider(ctx context.Context, workingDirectory string) (string, error) {
	params := map[string]interface{}{
		"includeLayers": false,
	}
	if workingDirectory != "" {
		params["cwd"] = workingDirectory
	}
	result, err := a.call(ctx, "config/read", params)
	if err != nil {
		return "", fmt.Errorf("failed to read current Codex config for thread resume: %w", err)
	}
	var response struct {
		Config struct {
			ModelProvider string `json:"model_provider"`
		} `json:"config"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return "", fmt.Errorf("failed to parse current Codex config for thread resume: %w", err)
	}
	currentProvider := strings.TrimSpace(response.Config.ModelProvider)
	if currentProvider == "" {
		currentProvider = "openai"
	}
	return currentProvider, nil
}

type turnState struct {
	items      map[string]map[string]interface{}
	usage      map[string]interface{}
	totalUsage map[string]interface{}
}

func (a *AppServerExec) handleTurnEvent(
	ctx context.Context,
	event appEvent,
	threadID string,
	turnID string,
	args CodexExecArgs,
	state *turnState,
	output chan ExecResult,
) (bool, error) {
	if !eventMatchesTurn(event, threadID, turnID) {
		return false, nil
	}
	if args.ApprovalHandler != nil && isApprovalRequestedEvent(event.Method) {
		a.submitApproval(ctx, event, args.ApprovalHandler)
	}
	if args.AskUserHandler != nil && isRequestUserInputEvent(event.Method) {
		a.submitAskUserResponse(event, args.AskUserHandler)
	}
	line, done, err := appEventToLegacyLine(event, state)
	if err != nil {
		return false, err
	}
	if line != "" {
		output <- ExecResult{Line: line}
	}
	return done, nil
}

func appEventToLegacyLine(event appEvent, state *turnState) (string, bool, error) {
	method := event.Method
	switch method {
	case "item/agentMessage/delta":
		return applyTextDelta(event, state, "agentMessage", "text")
	case "item/reasoning/summaryTextDelta":
		return applyTextDelta(event, state, "reasoning", "text")
	case "item/commandExecution/outputDelta":
		return applyTextDelta(event, state, "commandExecution", "aggregatedOutput")
	case "item/fileChange/outputDelta":
		return applyTextDelta(event, state, "fileChange", "output")
	}

	payload := map[string]interface{}{}
	if len(event.Params) > 0 {
		unmarshalErr := json.Unmarshal(event.Params, &payload)
		if unmarshalErr != nil {
			return "", false, unmarshalErr
		}
	}
	if method == "error" {
		if willRetry, _ := payload["willRetry"].(bool); willRetry {
			return "", false, nil
		}
	}
	payload["type"] = strings.ReplaceAll(method, "/", ".")
	if method == "turn/started" {
		state.usage = nil
		state.totalUsage = nil
	}
	if method == "thread/tokenUsage/updated" {
		if tokenUsage, ok := payload["tokenUsage"].(map[string]interface{}); ok {
			if last, ok := tokenUsage["last"].(map[string]interface{}); ok {
				total, hasTotal := tokenUsage["total"].(map[string]interface{})
				if !hasTotal || !sameTokenUsage(state.totalUsage, total) {
					state.usage = accumulateTurnUsage(state.usage, last)
					state.totalUsage = total
				}
			}
		}
	}
	if method == "turn/completed" {
		if _, ok := payload["usage"]; !ok && state.usage != nil {
			payload["usage"] = state.usage
		}
	}

	if itemPayload, ok := payload["item"].(map[string]interface{}); ok {
		if itemPayload["type"] == "contextCompaction" {
			switch method {
			case "item/started":
				itemPayload["status"] = "running"
			case "item/completed":
				itemPayload["status"] = "complete"
			}
		}
		if id, okID := itemPayload["id"].(string); okID {
			state.items[id] = itemPayload
		}
	}

	line, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	done := method == "turn/completed" || method == "turn/failed"
	return string(line), done, nil
}

func accumulateTurnUsage(current, last map[string]interface{}) map[string]interface{} {
	if current == nil {
		current = make(map[string]interface{}, 3)
	}
	for _, key := range []string{"inputTokens", "cachedInputTokens", "outputTokens"} {
		value, ok := last[key].(float64)
		if !ok {
			continue
		}
		previous, _ := current[key].(float64)
		current[key] = previous + value
	}
	return current
}

func sameTokenUsage(left, right map[string]interface{}) bool {
	if left == nil || right == nil {
		return false
	}
	for _, key := range []string{"inputTokens", "cachedInputTokens", "outputTokens", "totalTokens"} {
		if left[key] != right[key] {
			return false
		}
	}
	return true
}

func applyTextDelta(event appEvent, state *turnState, itemType string, field string) (string, bool, error) {
	var params struct {
		ItemID string `json:"itemId"`
		Delta  string `json:"delta"`
	}
	unmarshalErr := json.Unmarshal(event.Params, &params)
	if unmarshalErr != nil {
		return "", false, unmarshalErr
	}
	if params.ItemID == "" {
		return "", false, nil
	}
	item, ok := state.items[params.ItemID]
	if !ok {
		item = map[string]interface{}{
			"id":   params.ItemID,
			"type": itemType,
		}
		state.items[params.ItemID] = item
	}
	if params.Delta != "" {
		if existing, okExisting := item[field].(string); okExisting {
			item[field] = existing + params.Delta
		} else {
			item[field] = params.Delta
		}
	}
	payload := map[string]interface{}{
		"type": "item.updated",
		"item": item,
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	return string(line), false, nil
}

func eventMatchesTurn(event appEvent, threadID string, turnID string) bool {
	if threadID == "" && turnID == "" {
		return true
	}
	var meta struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     *struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	unmarshalErr := json.Unmarshal(event.Params, &meta)
	if unmarshalErr != nil {
		return false
	}
	if meta.TurnID == "" && meta.Turn != nil {
		meta.TurnID = meta.Turn.ID
	}
	if threadID != "" && meta.ThreadID != "" && meta.ThreadID != threadID {
		return false
	}
	if turnID != "" {
		return meta.TurnID == turnID
	}
	if threadID != "" && meta.ThreadID != "" {
		return meta.ThreadID == threadID
	}
	return false
}

func contextCompactionItemID(event appEvent) (string, bool) {
	if event.Method != "item/started" && event.Method != "item/completed" {
		return "", false
	}
	var payload struct {
		Item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"item"`
	}
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return "", false
	}
	return payload.Item.ID, payload.Item.Type == "contextCompaction"
}

func parseAccountLoginCompletedEvent(event appEvent) (types.AccountLoginCompletedNotification, bool, error) {
	if event.Method != "account/login/completed" {
		return types.AccountLoginCompletedNotification{}, false, nil
	}
	var payload types.AccountLoginCompletedNotification
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return types.AccountLoginCompletedNotification{}, false, err
	}
	return payload, true, nil
}

func parseAccountUpdatedEvent(event appEvent) (types.AccountUpdatedNotification, bool, error) {
	if event.Method != "account/updated" {
		return types.AccountUpdatedNotification{}, false, nil
	}
	var payload types.AccountUpdatedNotification
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return types.AccountUpdatedNotification{}, false, err
	}
	return payload, true, nil
}

func (a *AppServerExec) submitApproval(ctx context.Context, event appEvent, handler types.ApprovalHandler) {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Item     *struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"item"`
	}
	unmarshalErr := json.Unmarshal(event.Params, &params)
	if unmarshalErr != nil {
		a.logf("app server: failed to parse approval request: %v", unmarshalErr)
		return
	}
	itemID := params.ItemID
	itemType := approvalItemType(event.Method)
	if params.Item != nil {
		if itemID == "" {
			itemID = params.Item.ID
		}
		if params.Item.Type != "" {
			itemType = params.Item.Type
		}
	}
	if itemID == "" {
		return
	}
	decision, err := handler(types.ApprovalRequest{
		ItemID:   itemID,
		ItemType: itemType,
	})
	if err != nil {
		a.logf("app server: approval handler error: %v", err)
		return
	}
	if decision == "" {
		return
	}
	if event.ID != nil {
		result := approvalResponseResult(event.Method, decision)
		if result == nil {
			return
		}
		if submitErr := a.sendResponse(*event.ID, result); submitErr != nil {
			a.logf("app server: approval response error: %v", submitErr)
		}
		return
	}
	payload := map[string]interface{}{
		"itemId":   itemID,
		"decision": string(decision),
	}
	if params.ThreadID != "" {
		payload["threadId"] = params.ThreadID
	}
	_, submitErr := a.call(ctx, "approval/submit", payload)
	if submitErr != nil {
		a.logf("app server: approval submit error: %v", submitErr)
	}
}

func (a *AppServerExec) submitAskUserResponse(event appEvent, handler types.AskUserHandler) {
	if event.ID == nil {
		return
	}
	var request types.AskUserRequest
	if err := json.Unmarshal(event.Params, &request); err != nil {
		a.logf("app server: failed to parse request_user_input request: %v", err)
		return
	}
	response, err := handler(request)
	if err != nil {
		a.logf("app server: request_user_input handler error: %v", err)
		return
	}
	if len(response.Answers) == 0 {
		return
	}
	if submitErr := a.sendResponse(*event.ID, response); submitErr != nil {
		a.logf("app server: request_user_input response error: %v", submitErr)
	}
}

func approvalItemType(method string) string {
	switch method {
	case "item/commandExecution/requestApproval", "item/commandExecution/approvalRequested":
		return "commandExecution"
	case "item/fileChange/requestApproval", "item/fileChange/approvalRequested":
		return "fileChange"
	default:
		return ""
	}
}

func approvalResponseResult(method string, decision types.ApprovalDecision) map[string]interface{} {
	mapped := mapApprovalDecision(decision)
	if mapped == "" {
		return nil
	}
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return map[string]interface{}{"decision": mapped}
	default:
		return nil
	}
}

func mapApprovalDecision(decision types.ApprovalDecision) string {
	switch decision {
	case types.ApprovalDecisionApproved:
		return "accept"
	case types.ApprovalDecisionRejected:
		return "decline"
	default:
		return string(decision)
	}
}

func buildInputItems(args CodexExecArgs) []map[string]interface{} {
	inputItems := args.InputItems
	if len(inputItems) == 0 && args.Input != "" {
		inputItems = []types.UserInput{types.NewTextInput(args.Input)}
	}

	items := make([]map[string]interface{}, 0, len(inputItems)+len(args.Images))
	for _, item := range inputItems {
		appendInputItem(&items, item)
	}
	for _, image := range args.Images {
		appendLocalImage(&items, image)
	}
	return items
}

func appendInputItem(items *[]map[string]interface{}, item types.UserInput) {
	switch item.Type {
	case "text":
		if item.Text == "" {
			return
		}
		*items = append(*items, map[string]interface{}{
			"type": "text",
			"text": item.Text,
		})
	case "local_image", "localImage":
		appendLocalImage(items, item.Path)
	case "image":
		url := item.URL
		if url == "" {
			url = item.Path
		}
		if url == "" {
			return
		}
		*items = append(*items, map[string]interface{}{
			"type": "image",
			"url":  url,
		})
	case "skill":
		name := item.Name
		if name == "" {
			name = item.Text
		}
		if name == "" {
			return
		}
		*items = append(*items, map[string]interface{}{
			"type": "skill",
			"name": name,
		})
	case "mention":
		text := item.Text
		if text == "" {
			text = item.Name
		}
		if text == "" {
			return
		}
		*items = append(*items, map[string]interface{}{
			"type": "mention",
			"text": text,
		})
	}
}

func appendLocalImage(items *[]map[string]interface{}, path string) {
	if path == "" {
		return
	}
	*items = append(*items, map[string]interface{}{
		"type": "localImage",
		"path": path,
	})
}

func buildSandboxPolicy(args CodexExecArgs) map[string]interface{} {
	policy := map[string]interface{}{}
	switch args.SandboxMode {
	case "read-only":
		policy["type"] = "readOnly"
	case "workspace-write":
		policy["type"] = "workspaceWrite"
	case "danger-full-access":
		policy["type"] = "dangerFullAccess"
	}
	if len(policy) == 0 {
		return nil
	}
	networkAccess := "disabled"
	if args.NetworkAccessEnabled {
		networkAccess = "enabled"
	}
	policy["networkAccess"] = networkAccess
	if args.WorkingDirectory != "" || len(args.AdditionalDirectories) > 0 {
		roots := []string{}
		if args.WorkingDirectory != "" {
			roots = append(roots, args.WorkingDirectory)
		}
		for _, dir := range args.AdditionalDirectories {
			if dir == "" {
				continue
			}
			roots = append(roots, dir)
		}
		policy["writableRoots"] = roots
	}
	return policy
}

func mapApprovalPolicy(policy string) string {
	switch policy {
	case "on-request":
		return "onRequest"
	case "on-failure":
		return "onFailure"
	case "untrusted":
		return "unlessTrusted"
	default:
		return policy
	}
}

func loadOutputSchema(path string) (interface{}, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var schema interface{}
	unmarshalErr := json.Unmarshal(data, &schema)
	if unmarshalErr != nil {
		return nil, false, unmarshalErr
	}
	return schema, true, nil
}

func extractThreadID(result json.RawMessage) string {
	var payload struct {
		Thread *struct {
			ID string `json:"id"`
		} `json:"thread"`
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return ""
	}
	if payload.Thread != nil && payload.Thread.ID != "" {
		return payload.Thread.ID
	}
	return payload.ThreadID
}

func extractTurnID(result json.RawMessage) string {
	var payload struct {
		Turn *struct {
			ID string `json:"id"`
		} `json:"turn"`
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return ""
	}
	if payload.Turn != nil && payload.Turn.ID != "" {
		return payload.Turn.ID
	}
	return payload.TurnID
}
