package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Transcript is what Stage reads from a session's transcript
// (<projectsDirectory>/<encoded cwd>/<sessionId>.jsonl, ADR 0005 E4). Every
// field is best effort.
type Transcript struct {
	SessionID string
	// Cwd, Entrypoint and FirstPrompt come from the first typed user
	// prompt (entrypoint t3-code: a session T3 started).
	Cwd, Entrypoint, FirstPrompt string
	FirstAt, LastAt              time.Time
	// Title: the user's title (custom-title), else Claude's (ai-title).
	Title, Branch, Model string
	// LastPromptID is the uuid of the last typed user prompt: one per turn.
	LastPromptID string
	// Reply is the text of the last assistant message of the current turn
	// (after LastPromptID), "" when it has none yet.
	Reply, ReplyID string
	ReplyAt        time.Time
	// TurnOver: a turn_duration record follows the last prompt or reply.
	TurnOver bool
	// Background: the last typed prompt ran in a background session
	// (sessionKind "bg"), not a terminal or SDK one.
	Background bool
	// Turns: typed user prompts on the main chain (the current one
	// included); a background subagent's notification or a peer session's
	// message starts model work but is not a turn.
	Turns int
	// WorkingMs: the main chain's turn_duration records, summed (finished
	// turns only).
	WorkingMs int64
	// Tokens: every assistant message of this file, sidechains included.
	Tokens Tokens
}

// Tokens is the token usage of assistant messages, each message counted
// once.
type Tokens struct {
	Input, CacheRead, CacheWrite, Output int64
}

func (a Tokens) add(b Tokens) Tokens {
	return Tokens{Input: a.Input + b.Input, CacheRead: a.CacheRead + b.CacheRead,
		CacheWrite: a.CacheWrite + b.CacheWrite, Output: a.Output + b.Output}
}

// max is the larger value of each channel: a message's records repeat its
// usage, and a streamed one can grow from its first record to its last.
func (a Tokens) max(b Tokens) Tokens {
	return Tokens{Input: max(a.Input, b.Input), CacheRead: max(a.CacheRead, b.CacheRead),
		CacheWrite: max(a.CacheWrite, b.CacheWrite), Output: max(a.Output, b.Output)}
}

// originHuman is a typed user prompt's origin kind.
const originHuman = "human"

// syntheticModel marks a local placeholder message that never reached the
// API.
const syntheticModel = "<synthetic>"

// record is the subset of a transcript line Stage reads. Unknown types are
// skipped.
type record struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Entrypoint  string `json:"entrypoint"`
	SessionKind string `json:"sessionKind"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	SessionID   string `json:"sessionId"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	CustomTitle string `json:"customTitle"`
	AITitle     string `json:"aiTitle"`
	RequestID   string `json:"requestId"`
	DurationMs  int64  `json:"durationMs"`
	// Origin is who sent a user record: "human" for a typed prompt,
	// "task-notification" (a background subagent finished) or "peer"
	// (another session) otherwise; absent in -p, SDK and older sessions.
	Origin *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message *struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *usage          `json:"usage"`
	} `json:"message"`
}

// usage is an assistant message's Messages API usage.
type usage struct {
	InputTokens         int64 `json:"input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

// tokens is rec's usage and the key that dedupes its message's records
// ("" when it has neither a message nor a request id); ok false for a
// record without usage or a synthetic one.
func (rec record) tokens() (key string, t Tokens, ok bool) {
	m := rec.Message
	if rec.Type != "assistant" || m == nil || m.Usage == nil || m.Model == syntheticModel {
		return "", Tokens{}, false
	}
	if m.ID != "" || rec.RequestID != "" {
		key = m.ID + ":" + rec.RequestID
	}
	u := m.Usage
	return key, Tokens{Input: u.InputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheCreationTokens, Output: u.OutputTokens}, true
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// promptText is a user message's typed text: a string, or its text blocks;
// "" for a tool result or other non-prompt content.
func promptText(content json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s, true
	}
	var bs []block
	if json.Unmarshal(content, &bs) != nil {
		return "", false
	}
	var parts []string
	for _, b := range bs {
		if b.Type == "tool_result" {
			return "", false
		}
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// assistantText is the text blocks of one assistant record, joined.
func assistantText(content json.RawMessage) string {
	var bs []block
	if json.Unmarshal(content, &bs) != nil {
		return ""
	}
	var parts []string
	for _, b := range bs {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// maxLine bounds one transcript line Stage parses; longer lines (request
// blobs with the system prompt) are skipped unread.
const maxLine = 4 << 20

// eachRecord calls fn for each parsable line of r until fn returns false.
func eachRecord(r io.Reader, fn func(record) bool) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			var rec record
			if json.Unmarshal(line, &rec) == nil && !fn(rec) {
				return nil
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLine reads one line, returning nil for one longer than maxLine.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	skip := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !skip {
			buf = append(buf, chunk...)
			if len(buf) > maxLine {
				buf, skip = nil, true
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// ReadTranscript parses the transcript at path.
func ReadTranscript(path string) (Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()
	return parseTranscript(f)
}

func parseTranscript(r io.Reader) (Transcript, error) {
	var t Transcript
	var customTitle, aiTitle string
	messages := map[string]Tokens{}
	err := eachRecord(r, func(rec record) bool {
		if rec.SessionID != "" && t.SessionID == "" {
			t.SessionID = rec.SessionID
		}
		if key, tok, ok := rec.tokens(); ok {
			if key == "" {
				t.Tokens = t.Tokens.add(tok)
			} else {
				messages[key] = messages[key].max(tok)
			}
		}
		switch rec.Type {
		case "custom-title":
			if rec.CustomTitle != "" {
				customTitle = rec.CustomTitle
			}
			return true
		case "ai-title":
			if rec.AITitle != "" {
				aiTitle = rec.AITitle
			}
			return true
		}
		if rec.IsSidechain || rec.Timestamp == "" {
			return true
		}
		at := parseTime(rec.Timestamp)
		if !at.IsZero() {
			if t.FirstAt.IsZero() {
				t.FirstAt = at
			}
			t.LastAt = at
		}
		if rec.GitBranch != "" {
			t.Branch = rec.GitBranch
		}
		switch rec.Type {
		case "user":
			if rec.IsMeta || rec.Message == nil {
				return true
			}
			text, ok := promptText(rec.Message.Content)
			if !ok {
				return true
			}
			if t.FirstPrompt == "" && t.Entrypoint == "" {
				t.FirstPrompt, t.Entrypoint, t.Cwd = text, rec.Entrypoint, rec.Cwd
			}
			t.LastPromptID, t.Reply, t.ReplyID, t.ReplyAt, t.TurnOver = rec.UUID, "", "", time.Time{}, false
			if rec.Origin == nil || rec.Origin.Kind == originHuman {
				t.Turns++
			}
			t.Background = rec.SessionKind == sessionKindBackground
		case "assistant":
			if rec.Message == nil {
				return true
			}
			t.TurnOver = false
			if rec.Message.Model != "" && !strings.HasPrefix(rec.Message.Model, "<") {
				t.Model = rec.Message.Model
			}
			text := assistantText(rec.Message.Content)
			if text == "" {
				return true
			}
			// One message can span several records with the same id.
			if rec.Message.ID != "" && rec.Message.ID == t.ReplyID {
				t.Reply += "\n" + text
			} else {
				t.Reply, t.ReplyID = text, rec.Message.ID
			}
			t.ReplyAt = at
		case "system":
			if rec.Subtype == "turn_duration" {
				t.TurnOver = true
				t.WorkingMs += max(rec.DurationMs, 0)
			}
		}
		return true
	})
	for _, tok := range messages {
		t.Tokens = t.Tokens.add(tok)
	}
	t.Title = customTitle
	if t.Title == "" {
		t.Title = aiTitle
	}
	return t, err
}

// ReadHead reads a transcript only up to its first typed user prompt: the
// session's cwd, entrypoint and first prompt (marker search, adoption
// checks).
func ReadHead(path string) (Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()
	var t Transcript
	err = eachRecord(f, func(rec record) bool {
		if rec.Type != "user" || rec.IsSidechain || rec.IsMeta || rec.Message == nil {
			return true
		}
		text, ok := promptText(rec.Message.Content)
		if !ok {
			return true
		}
		t.SessionID, t.Cwd, t.Entrypoint, t.FirstPrompt = rec.SessionID, rec.Cwd, rec.Entrypoint, text
		t.FirstAt = parseTime(rec.Timestamp)
		return false
	})
	return t, err
}

// EncodeProject is the transcript folder name for cwd: '/' and '.' become
// '-' (ADR 0005 E4; other characters were not probed and are kept).
func EncodeProject(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// transcriptPath finds a session's transcript under root: in cwd's folder
// when known, else any project folder (a session with no process may report
// its launch directory as cwd).
func transcriptPath(root, cwd, id string) (string, bool) {
	if root == "" || !validUUID(id) {
		return "", false
	}
	if cwd != "" {
		p := filepath.Join(root, EncodeProject(cwd), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	ms, _ := filepath.Glob(filepath.Join(root, "*", id+".jsonl"))
	if len(ms) == 0 {
		return "", false
	}
	return ms[0], true
}

// Origin reports the entrypoint of a T3-started session.
const entrypointT3 = "t3-code"

// sessionKindBackground is a transcript record's sessionKind in a `--bg`
// session.
const sessionKindBackground = "bg"

// refusedT3 is the refusal for a session T3 started: T3 tracks it.
var refusedT3 = "This Claude session was started by T3. Track it through T3."
