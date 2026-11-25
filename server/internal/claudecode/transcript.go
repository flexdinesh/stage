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
}

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
	Message     *struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
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
	err := eachRecord(r, func(rec record) bool {
		if rec.SessionID != "" && t.SessionID == "" {
			t.SessionID = rec.SessionID
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
			}
		}
		return true
	})
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
