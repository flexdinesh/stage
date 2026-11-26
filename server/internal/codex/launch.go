package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Launch phases (checkpoint version 1).
const (
	// phaseThread: thread/start may have run; no id recorded. A thread
	// created then has no turn and no rollout, so replay starts another.
	phaseThread = "thread"
	// phaseTurn: the thread id is recorded; turn/start may have run.
	// Replay finds the turn by its client message id before starting one.
	phaseTurn = "turn"
)

type launchRecord struct {
	Intent string `json:"intent"`
	Phase  string `json:"phase"`
	Thread string `json:"thread,omitempty"`
}

func (r *launchRecord) save(ctx context.Context, save execution.SaveCheckpoint) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return save(ctx, execution.Checkpoint{Version: 1, Data: b})
}

// digest pins the intent a checkpoint belongs to (images by name and type:
// their bytes are not part of a replayed intent's identity).
func digest(in execution.Intent) (string, error) {
	type img struct{ Name, MimeType string }
	imgs := make([]img, len(in.Images))
	for i, m := range in.Images {
		imgs[i] = img{m.Name, m.MimeType}
	}
	b, err := json.Marshal(struct {
		RequestID, Directory, Title, Prompt string
		Config                              execution.Config
		Images                              []img
	}{in.RequestID, in.Directory, in.Title, in.Prompt, in.Config, imgs})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// selection is the validated launch configuration.
type selection struct {
	model      string
	effort     string
	permission permission
}

// validate checks the intent before any effect; a problem certifies that
// nothing started (execution.Rejected).
func (a *Adapter) validate(ctx context.Context, in execution.Intent) (selection, error) {
	var sel selection
	if strings.TrimSpace(in.Directory) == "" {
		return sel, &execution.Rejected{Reason: "Choose a bound Codex project directory."}
	}
	for _, img := range in.Images {
		if !strings.HasPrefix(img.MimeType, "image/") || len(img.Data) == 0 {
			return sel, &execution.Rejected{Reason: fmt.Sprintf("Attachment %q is not an image Codex can read.", img.Name)}
		}
	}
	caps, err := a.Features(ctx)
	if err != nil {
		return sel, err
	}
	if err := caps.ValidateConfig(in.Config); err != nil {
		return sel, &execution.Rejected{Reason: err.Error()}
	}
	p, ok := permissionFor(in.Config.Permission)
	if !ok {
		return sel, &execution.Rejected{Reason: "Unknown Codex permission " + in.Config.Permission + "."}
	}
	sel.model, sel.permission = in.Config.Model, p
	for _, o := range in.Config.Options {
		if o.ID == effortOption {
			sel.effort = o.Value.Str
		}
	}
	return sel, nil
}

// Launch starts a thread in the bound directory and its first turn. The
// thread id is checkpointed before the turn starts, and the turn carries
// the request id as its client message id, so a replay reconciles the
// same thread and never starts a second turn.
func (a *Adapter) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	var zero execution.Observation
	if in.RequestID == "" || in.ConnectionID != a.connection {
		return zero, errors.New("invalid Codex launch identity")
	}
	sum, err := digest(in)
	if err != nil {
		return zero, err
	}
	var r launchRecord
	fresh := cp.Version == 0 && (len(cp.Data) == 0 || string(cp.Data) == "null")
	if !fresh {
		if cp.Version != 1 || json.Unmarshal(cp.Data, &r) != nil || r.Intent != sum || (r.Phase != phaseThread && r.Phase != phaseTurn) || (r.Phase == phaseTurn && !threadID.MatchString(r.Thread)) {
			return zero, errors.New("Codex launch checkpoint cannot be verified. Review the thread in Codex, then link it.")
		}
	}
	sel, err := a.validate(ctx, in)
	if err != nil {
		var rej *execution.Rejected
		if !fresh && errors.As(err, &rej) {
			// A replay may already have started work: never certify.
			return zero, errors.New(rej.Reason)
		}
		return zero, err
	}
	if r.Phase == phaseTurn {
		t, err := a.read(ctx, r.Thread)
		switch {
		case errors.Is(err, execution.ErrNotFound):
			// Never persisted (no first turn) and gone with a daemon
			// restart: no turn can exist. Start again.
			r = launchRecord{}
		case err != nil:
			return zero, err
		default:
			admitted, err := a.admitted(ctx, t.ID, in)
			if err != nil {
				return zero, err
			}
			if admitted {
				return a.launched(ctx, t.ID)
			}
		}
	}
	r.Intent = sum
	if r.Thread == "" {
		r.Phase = phaseThread
		if err := r.save(ctx, save); err != nil {
			return zero, err
		}
		params := map[string]any{"cwd": in.Directory, "approvalPolicy": sel.permission.Approval, "sandbox": sel.permission.Sandbox}
		if sel.model != "" {
			params["model"] = sel.model
		}
		var res threadResponse
		if err := a.client.call(ctx, "thread/start", params, &res); err != nil {
			return zero, err
		}
		if !threadID.MatchString(res.Thread.ID) {
			return zero, errors.New("codex thread/start returned no thread id")
		}
		r.Thread, r.Phase = res.Thread.ID, phaseTurn
		if err := r.save(ctx, save); err != nil {
			return zero, err
		}
		if title := strings.TrimSpace(in.Title); title != "" {
			// The name is cosmetic: a failure leaves Codex's preview.
			_ = a.client.call(ctx, "thread/name/set", map[string]any{"threadId": r.Thread, "name": title}, nil)
		}
	}
	input := []map[string]any{{"type": "text", "text": in.Prompt, "text_elements": []any{}}}
	for _, img := range in.Images {
		input = append(input, map[string]any{"type": "image", "url": "data:" + img.MimeType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)})
	}
	params := map[string]any{"threadId": r.Thread, "input": input, "clientUserMessageId": in.RequestID}
	if sel.effort != "" {
		params["effort"] = sel.effort
	}
	var res turnResponse
	if err := a.client.call(ctx, "turn/start", params, &res); err != nil {
		return zero, err
	}
	return a.launched(ctx, r.Thread)
}

// admitted reports whether the thread already holds the launch's turn:
// its first user message carries the request id (or, when Codex keeps no
// client id, the prompt). Another turn means the thread is not this
// launch's alone: the user decides.
func (a *Adapter) admitted(ctx context.Context, id string, in execution.Intent) (bool, error) {
	var page turnsPage
	if err := a.client.call(ctx, "thread/turns/list", map[string]any{"threadId": id, "limit": 1, "sortDirection": "asc", "itemsView": "full"}, &page); err != nil {
		if notLoaded(err) {
			return false, nil
		}
		return false, err
	}
	if len(page.Data) == 0 {
		return false, nil
	}
	for _, it := range page.Data[0].Items {
		if it.Type != itemUserMessage {
			continue
		}
		if (it.ClientID != nil && *it.ClientID == in.RequestID) || (it.ClientID == nil && userText(it) == in.Prompt) {
			return true, nil
		}
	}
	return false, errors.New("the Codex thread has a turn Stage did not start. Review it in Codex, then link it.")
}

// launched leaves the thread's event stream (the daemon keeps running the
// turn; status changes still reach Stage) and observes it.
func (a *Adapter) launched(ctx context.Context, id string) (execution.Observation, error) {
	_ = a.client.call(ctx, "thread/unsubscribe", map[string]any{"threadId": id}, nil)
	return a.Observe(ctx, a.ref(id))
}

// userText joins a user message's text inputs.
func userText(it item) string {
	var inputs []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(it.Content, &inputs) != nil {
		return ""
	}
	var parts []string
	for _, in := range inputs {
		if in.Type == "text" {
			parts = append(parts, in.Text)
		}
	}
	return strings.Join(parts, "\n")
}
