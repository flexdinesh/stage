package codex

import "encoding/json"

// Wire shapes of the app-server protocol (codex-cli 0.171.0, `codex
// app-server generate-ts`). Only the fields Stage reads.

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type initializeParams struct {
	ClientInfo   clientInfo     `json:"clientInfo"`
	Capabilities map[string]any `json:"capabilities"`
}

type initializeResponse struct {
	UserAgent string `json:"userAgent"`
	CodexHome string `json:"codexHome"`
}

// threadStatus: notLoaded, idle, systemError or active (with flags
// waitingOnApproval / waitingOnUserInput).
type threadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

const (
	statusNotLoaded   = "notLoaded"
	statusIdle        = "idle"
	statusSystemError = "systemError"
	statusActive      = "active"
)

type thread struct {
	ID             string       `json:"id"`
	ParentThreadID *string      `json:"parentThreadId"`
	Preview        string       `json:"preview"`
	Ephemeral      bool         `json:"ephemeral"`
	Model          *string      `json:"model"`
	CreatedAt      int64        `json:"createdAt"`
	UpdatedAt      int64        `json:"updatedAt"`
	Status         threadStatus `json:"status"`
	Cwd            string       `json:"cwd"`
	Originator     string       `json:"originator"`
	Name           *string      `json:"name"`
	GitInfo        *struct {
		Branch *string `json:"branch"`
	} `json:"gitInfo"`
	Turns []turn `json:"turns"`
}

// Turn statuses.
const (
	turnCompleted   = "completed"
	turnInterrupted = "interrupted"
	turnFailed      = "failed"
	turnInProgress  = "inProgress"
)

type turn struct {
	ID     string `json:"id"`
	Items  []item `json:"items"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	StartedAt   *int64 `json:"startedAt"`
	CompletedAt *int64 `json:"completedAt"`
}

// item is a thread item; Stage reads user and agent messages.
type item struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Text     string          `json:"text"`
	Phase    *string         `json:"phase"`
	ClientID *string         `json:"clientId"`
	Content  json.RawMessage `json:"content"`
}

const (
	itemUserMessage  = "userMessage"
	itemAgentMessage = "agentMessage"
	phaseFinalAnswer = "final_answer"
)

type threadResponse struct {
	Thread thread `json:"thread"`
}

type turnsPage struct {
	Data       []turn  `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

type threadsPage struct {
	Data       []thread `json:"data"`
	NextCursor *string  `json:"nextCursor"`
}

type idsPage struct {
	Data       []string `json:"data"`
	NextCursor *string  `json:"nextCursor"`
}

type turnResponse struct {
	Turn turn `json:"turn"`
}

type reasoningEffort struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

type model struct {
	ID                        string            `json:"id"`
	Model                     string            `json:"model"`
	DisplayName               string            `json:"displayName"`
	Hidden                    bool              `json:"hidden"`
	SupportedReasoningEfforts []reasoningEffort `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort    string            `json:"defaultReasoningEffort"`
	InputModalities           []string          `json:"inputModalities"`
	IsDefault                 bool              `json:"isDefault"`
}

type modelsPage struct {
	Data       []model `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

// account: apiKey, chatgpt (email, planType), amazonBedrock or gcp.
type account struct {
	Type     string  `json:"type"`
	Email    *string `json:"email"`
	PlanType *string `json:"planType"`
}

type accountResponse struct {
	Account            *account `json:"account"`
	RequiresOpenaiAuth bool     `json:"requiresOpenaiAuth"`
}

type configResponse struct {
	Config map[string]json.RawMessage `json:"config"`
	// Layers: only with includeLayers.
	Layers []configLayer `json:"layers"`
}

// configLayer is one config layer; Version is what a write's
// expectedVersion compares against.
type configLayer struct {
	Name struct {
		Type    string  `json:"type"`
		Profile *string `json:"profile"`
	} `json:"name"`
	Version string `json:"version"`
}

// configWriteResponse answers config/batchWrite. Status: ok or
// okOverridden (a higher layer still decides the value).
type configWriteResponse struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Override *struct {
		Message string `json:"message"`
	} `json:"overriddenMetadata"`
}

// fsMetadata answers fs/getMetadata.
type fsMetadata struct {
	IsDirectory bool `json:"isDirectory"`
}
