package types

import "io"

// ApprovalMode represents the approval mode for actions.
type ApprovalMode string

const (
	ApprovalModeNever     ApprovalMode = "never"
	ApprovalModeOnRequest ApprovalMode = "on-request"
	ApprovalModeOnFailure ApprovalMode = "on-failure"
	ApprovalModeUntrusted ApprovalMode = "untrusted"
)

// SandboxMode represents the sandbox access mode.
type SandboxMode string

const (
	SandboxModeReadOnly       SandboxMode = "read-only"
	SandboxModeWorkspaceWrite SandboxMode = "workspace-write"
	SandboxModeFullAccess     SandboxMode = "danger-full-access"
)

// ModelReasoningEffort represents the reasoning effort level for the model.
type ModelReasoningEffort string

const (
	ModelReasoningEffortMinimal ModelReasoningEffort = "minimal"
	ModelReasoningEffortLow     ModelReasoningEffort = "low"
	ModelReasoningEffortMedium  ModelReasoningEffort = "medium"
	ModelReasoningEffortHigh    ModelReasoningEffort = "high"
	ModelReasoningEffortXHigh   ModelReasoningEffort = "xhigh"
	ModelReasoningEffortMax     ModelReasoningEffort = "max"
	ModelReasoningEffortUltra   ModelReasoningEffort = "ultra"
)

// WebSearchMode represents the web search mode.
type WebSearchMode string

const (
	WebSearchModeDisabled WebSearchMode = "disabled"
	WebSearchModeCached   WebSearchMode = "cached"
	WebSearchModeLive     WebSearchMode = "live"
)

// CollaborationModeKind selects the Codex collaboration mode for a turn.
type CollaborationModeKind string

const (
	CollaborationModeDefault CollaborationModeKind = "default"
	CollaborationModePlan    CollaborationModeKind = "plan"
)

// CollaborationModeSettings configures the model behavior for a collaboration mode.
//
// App-server requires settings.model. SDK callers normally use
// NewCollaborationMode and let the app-server transport fill model/effort from
// the active thread or turn options before sending the request.
type CollaborationModeSettings struct {
	Model                 string                `json:"model"`
	ReasoningEffort       *ModelReasoningEffort `json:"reasoning_effort,omitempty"`
	DeveloperInstructions *string               `json:"developer_instructions,omitempty"`
}

// CollaborationMode configures Codex collaboration mode for turn/start.
//
// A nil DeveloperInstructions value lets app-server use built-in instructions for the mode.
type CollaborationMode struct {
	Mode     CollaborationModeKind     `json:"mode"`
	Settings CollaborationModeSettings `json:"settings"`
}

// NewCollaborationMode creates a collaboration mode value.
//
// The app-server transport fills required settings from the active thread or
// turn options before sending the request.
func NewCollaborationMode(mode CollaborationModeKind) *CollaborationMode {
	return &CollaborationMode{
		Mode: mode,
	}
}

// ApprovalDecision represents the decision for an approval request.
type ApprovalDecision string

const (
	// ApprovalDecisionApproved means the action is approved.
	ApprovalDecisionApproved ApprovalDecision = "approved"
	// ApprovalDecisionRejected means the action is rejected.
	ApprovalDecisionRejected ApprovalDecision = "rejected"
)

// ApprovalRequest represents an approval request from the app server.
type ApprovalRequest struct {
	ItemID   string
	ItemType string
}

// ApprovalHandler decides how to respond to an approval request.
type ApprovalHandler func(request ApprovalRequest) (ApprovalDecision, error)

// AskUserQuestionOption is one selectable option for a request_user_input question.
type AskUserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AskUserQuestion describes one question from the request_user_input tool.
type AskUserQuestion struct {
	ID       string                  `json:"id"`
	Header   string                  `json:"header"`
	Question string                  `json:"question"`
	IsOther  bool                    `json:"isOther,omitempty"`
	IsSecret bool                    `json:"isSecret,omitempty"`
	Options  []AskUserQuestionOption `json:"options,omitempty"`
}

// AskUserRequest carries a pending request_user_input app-server request.
type AskUserRequest struct {
	ThreadID  string            `json:"threadId"`
	TurnID    string            `json:"turnId"`
	ItemID    string            `json:"itemId"`
	Questions []AskUserQuestion `json:"questions"`
}

// AskUserAnswer is the answer for one request_user_input question.
type AskUserAnswer struct {
	Answers []string `json:"answers"`
}

// AskUserResponse is sent back to app-server to resolve request_user_input.
type AskUserResponse struct {
	Answers map[string]AskUserAnswer `json:"answers"`
}

// AskUserHandler decides how to answer a request_user_input request.
type AskUserHandler func(request AskUserRequest) (AskUserResponse, error)

// TransportMode represents the backend transport used by the SDK.
type TransportMode string

const (
	// TransportAppServer uses the Codex app server protocol.
	TransportAppServer TransportMode = "app-server"
	// TransportCLI uses the Codex CLI JSONL protocol.
	TransportCLI TransportMode = "cli"
)

// ClientInfo identifies the SDK client to the app server.
type ClientInfo struct {
	Name    string
	Version string
}

// CodexOptions represents options for the Codex client.
type CodexOptions struct {
	// Transport selects the backend transport. Defaults to app-server.
	Transport TransportMode
	// AppServerPathOverride is an optional path to the app server executable.
	// When unset, the SDK will try to locate the codex binary and run it with "app-server".
	AppServerPathOverride string
	// AppServerArgs are optional arguments passed after the "app-server" subcommand.
	AppServerArgs []string
	// ClientInfo identifies the SDK client to the app server (initialize).
	ClientInfo ClientInfo
	// CodexPathOverride is an optional path to the codex binary
	CodexPathOverride string
	// BaseUrl is the base URL for the API
	BaseUrl string
	// ApiKey is the API key for authentication
	ApiKey string
	// Env is environment variables passed to the Codex CLI process.
	// When provided, the SDK will not inherit variables from the environment.
	Env map[string]string
	// Verbose enables debug logging for Codex CLI execution.
	Verbose bool
	// VerboseWriter is the output for debug logs when Verbose is enabled.
	VerboseWriter io.Writer
}

// ThreadOptions represents options for a thread.
type ThreadOptions struct {
	// Model is the model to use
	Model string
	// ModelProvider is the model provider to use.
	// When resuming with the app-server transport and this is empty, the SDK
	// uses the current effective config's model_provider instead of the
	// provider persisted on the old thread.
	ModelProvider string
	// SandboxMode is the sandbox access mode
	SandboxMode SandboxMode
	// WorkingDirectory is the working directory for the thread
	WorkingDirectory string
	// DeveloperInstructions overrides the effective developer instructions for the thread.
	// This is app-server-only. CLI transport ignores it.
	DeveloperInstructions string
	// SkipGitRepoCheck skips the git repository check
	SkipGitRepoCheck bool
	// DisableSkills disables the Codex CLI skills feature.
	DisableSkills bool
	// FastService controls Codex fast service behavior.
	// Supported values:
	//   ""    - leave service_tier unspecified
	//   "on"  - force service_tier="fast"
	//   "off" - explicitly clear service_tier
	FastService string
	// ModelReasoningEffort is the reasoning effort for the model
	ModelReasoningEffort ModelReasoningEffort
	// NetworkAccessEnabled enables network access
	NetworkAccessEnabled bool
	// WebSearchMode is the web search mode
	WebSearchMode WebSearchMode
	// WebSearchEnabled is a legacy flag for web search
	WebSearchEnabled *bool
	// ApprovalPolicy is the approval mode
	ApprovalPolicy ApprovalMode
	// ApprovalHandler handles approval requests when the app server asks for permission.
	ApprovalHandler ApprovalHandler
	// AskUserHandler handles request_user_input requests when app-server asks the user.
	AskUserHandler AskUserHandler
	// AdditionalDirectories are additional directories to include
	AdditionalDirectories []string
	// CollaborationMode selects the Codex collaboration mode for app-server turn/start.
	// This is app-server-only. CLI transport ignores it.
	CollaborationMode *CollaborationMode
}

// ThreadForkOptions represents options for forking an existing thread.
type ThreadForkOptions struct {
	ThreadOptions
	// LastTurnID forks through this completed turn, inclusive. It cannot be
	// combined with TruncateBeforeNthUserMessage.
	LastTurnID string
	// TruncateBeforeNthUserMessage cuts the forked history strictly before the
	// nth user message. The index is zero-based. Positive ordinals are resolved
	// to LastTurnID before forking; zero retains the legacy rollback behavior.
	TruncateBeforeNthUserMessage *int
}

// TurnOptions represents options for a turn.
type TurnOptions struct {
	// OutputSchema is a JSON schema describing the expected agent output
	OutputSchema interface{}
	// Context is a context.Context for cancellation (replaces AbortSignal from TypeScript)
	Context interface{}
	// CollaborationMode overrides the Codex collaboration mode for this and subsequent turns.
	// This is app-server-only. CLI transport ignores it.
	CollaborationMode *CollaborationMode
}
