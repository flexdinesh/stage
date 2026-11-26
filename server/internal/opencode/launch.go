package opencode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flexdinesh/stage/server/internal/execution"
)

type launchRecord struct {
	Intent, Endpoint, Marker, Session, Message, Directory, Phase string
	Process                                                      serverInfo
	PromptAt                                                     int64
}

func (r *launchRecord) advance(ctx context.Context, save execution.SaveCheckpoint, phase string) error {
	r.Phase = phase
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return save(ctx, execution.Checkpoint{Version: 1, Data: b})
}
func (a *Adapter) matched(s session, r launchRecord, in execution.Intent) bool {
	return s.ID == r.Session && s.Metadata["stageAttempt"] == r.Marker && s.ProjectID == in.Project && s.Location.Directory == r.Directory && s.ParentID == "" && s.Time.Archived == 0
}

type admitted struct {
	ID, Type, SessionID string
	Time                wireTime
	Payload             struct {
		Text     string
		Metadata map[string]interface{}
	}
}

func (r launchRecord) matchesPrompt(v admitted, text string) bool {
	return v.Type == "user" && v.ID == r.Message && v.SessionID == r.Session && v.Payload.Text == text && v.Payload.Metadata["stageAttempt"] == r.Marker
}

func (a *Adapter) verifyPrompt(ctx context.Context, r *launchRecord, text string) error {
	prefix := "/api/session/" + r.Session
	var inbox envelope[[]admitted]
	if e := a.get(ctx, prefix+"/inbox", &inbox); e != nil {
		return e
	}
	for _, v := range inbox.Data {
		if r.matchesPrompt(v, text) {
			r.PromptAt = v.Time.Created
			return nil
		}
	}
	var m envelope[message]
	if e := a.get(ctx, prefix+"/message/"+r.Message, &m); e != nil {
		if missing(e) {
			return errors.New("Initial prompt is unconfirmed. Check the session in OpenCode before linking it.")
		}
		return e
	}
	if m.Data.ID != r.Message || m.Data.Type != "user" || m.Data.Metadata["stageAttempt"] != r.Marker || m.Data.Text != text {
		return errors.New("OpenCode prompt identity or content differs from the saved launch")
	}
	r.PromptAt = m.Data.Time.Created
	return nil
}

func (a *Adapter) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	var zero execution.Observation
	if in.RequestID == "" || in.ConnectionID != a.connection {
		return zero, errors.New("invalid OpenCode launch identity")
	}
	info, err := a.check(ctx)
	if err != nil {
		return zero, err
	}
	encoded, e := json.Marshal(in)
	if e != nil {
		return zero, e
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded))
	var r launchRecord
	fresh := cp.Version == 0 && (len(cp.Data) == 0 || string(cp.Data) == "null")
	if fresh {
		b := make([]byte, 16)
		if _, e = rand.Read(b); e != nil {
			return zero, e
		}
		key := hex.EncodeToString(b)
		r = launchRecord{Intent: digest, Endpoint: a.endpoint, Process: info, Marker: key, Session: "ses_" + key, Message: "msg_" + key, Directory: in.Directory, Phase: "ready"}
	} else {
		if cp.Version != 1 || json.Unmarshal(cp.Data, &r) != nil || r.Phase == "ready" || r.Intent != digest || r.Endpoint != a.endpoint || r.Marker == "" || r.Session != "ses_"+r.Marker || r.Message != "msg_"+r.Marker || r.Directory == "" {
			return zero, errors.New("OpenCode launch checkpoint cannot be verified. Review the existing session.")
		}
	}
	if r.Phase == "worktree-requested" {
		return zero, errors.New("OpenCode worktree creation is unconfirmed. Verify setup in OpenCode and link an existing session.")
	}
	ref := execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: r.Session}
	if fresh && in.Config.Bool(optionWorktree) {
		c, e := a.FeaturesForWorkspace(ctx, in.Project, in.Directory)
		if e != nil {
			return zero, e
		}
		if _, e = selection(c, in); e != nil {
			return zero, e
		}
		if e = r.advance(ctx, save, "worktree-requested"); e != nil {
			return zero, e
		}
		var tree location
		if e = a.request(ctx, "POST", "/api/worktree", map[string]string{"projectID": in.Project, "from": in.Directory, "name": "stage-" + r.Marker}, &tree); e != nil {
			return zero, e
		}
		if tree.Directory == "" || tree.Directory == in.Directory {
			return zero, errors.New("OpenCode returned an invalid new worktree directory")
		}
		r.Directory = tree.Directory
		if e = r.advance(ctx, save, "workspace-created"); e != nil {
			return zero, e
		}
	}
	if r.Phase == "ready" || r.Phase == "workspace-created" {
		if r.Phase == "workspace-created" {
			if !fresh && (info != r.Process || info.Hostname == "" || info.PID <= 0) {
				return zero, errors.New("OpenCode restarted before session creation. Verify the worktree and link the existing session.")
			}
			if e = a.verifyWorktree(ctx, in.Project, r.Directory); e != nil {
				return zero, e
			}
		}
		c, e := a.FeaturesForWorkspace(ctx, in.Project, r.Directory)
		if e != nil {
			return zero, e
		}
		model, e := selection(c, in)
		if e != nil {
			if r.Phase == "workspace-created" {
				return zero, fmt.Errorf("Worktree was created but launch configuration is unavailable: %s", e)
			}
			return zero, e
		}
		create := map[string]interface{}{"id": r.Session, "title": in.Title, "location": location{Directory: r.Directory}, "metadata": map[string]string{"stageAttempt": r.Marker}}
		if in.Config.Mode != "" {
			create["agent"] = in.Config.Mode
		}
		if model != nil {
			create["model"] = model
		}
		if e = r.advance(ctx, save, "session-requested"); e != nil {
			return zero, e
		}
		var result envelope[session]
		if e = a.request(ctx, "POST", "/api/session", create, &result); e != nil {
			return zero, e
		}
		if !a.matched(result.Data, r, in) {
			return zero, errors.New("OpenCode returned a different session. Verify the existing session before linking.")
		}
	}
	if r.Phase != "session-requested" && r.Phase != "prompt-requested" && r.Phase != "admitted" {
		return zero, errors.New("unknown OpenCode launch checkpoint phase")
	}
	s, e := a.readSession(ctx, ref)
	if errors.Is(e, execution.ErrNotFound) {
		return zero, errors.New("Reserved OpenCode session is unconfirmed. Restore the original service or link a verified session.")
	}
	if e != nil {
		return zero, e
	}
	if !a.matched(s, r, in) {
		return zero, errors.New("OpenCode session does not match the reserved launch destination")
	}
	switch r.Phase {
	case "session-requested":
		if e = r.advance(ctx, save, "prompt-requested"); e != nil {
			return zero, e
		}
		var result envelope[admitted]
		if e = a.request(ctx, "POST", "/api/session/"+r.Session+"/prompt", map[string]interface{}{"id": r.Message, "text": in.Prompt, "metadata": map[string]string{"stageAttempt": r.Marker}}, &result); e != nil {
			return zero, e
		}
		if !r.matchesPrompt(result.Data, in.Prompt) {
			return zero, errors.New("OpenCode returned a mismatched prompt admission")
		}
		r.PromptAt = result.Data.Time.Created
	case "prompt-requested":
		if e = a.verifyPrompt(ctx, &r, in.Prompt); e != nil {
			return zero, e
		}
	}
	if r.PromptAt <= 0 {
		return zero, errors.New("OpenCode prompt has no admission timestamp")
	}
	if r.Phase != "admitted" {
		if e = r.advance(ctx, save, "admitted"); e != nil {
			return zero, e
		}
	}
	o, e := a.Observe(ctx, ref)
	if e != nil {
		return zero, e
	}
	if o.State == "idle" || o.EndedAt != nil && o.EndedAt.UnixMilli() < r.PromptAt {
		return zero, errors.New("Prompt admitted. Waiting for OpenCode to report execution.")
	}
	return o, nil
}
