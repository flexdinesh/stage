package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.Interactive = (*Adapter)(nil)

// threadSession is a connection subscribed to one thread.
type threadSession struct {
	c        *conn
	thread   thread
	settings threadSettings
}

// sessionErr marks a dropped session connection as unavailable.
func sessionErr(err error) error {
	if errors.Is(err, errClosed) {
		return fmt.Errorf("%w: %w", execution.ErrUnavailable, err)
	}
	return err
}

// resume opens a connection and resumes the thread on it. ErrNotFound:
// Codex does not know the thread.
func (a *Adapter) resume(ctx context.Context, id string) (*threadSession, error) {
	c, err := a.client.session(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	var res threadResponse
	if err := resumeThread(ctx, c, id, &res); err != nil {
		c.close()
		if notLoaded(err) {
			return nil, execution.ErrNotFound
		}
		return nil, sessionErr(err)
	}
	if res.Thread.ID != id {
		c.close()
		return nil, errors.New("codex resumed a different thread")
	}
	return &threadSession{c: c, thread: res.Thread, settings: res.settings()}, nil
}

// resumeThread attaches to a thread's stream without turn history or config
// overrides. The daemon replays that thread's pending server requests.
func resumeThread(ctx context.Context, c *conn, id string, out *threadResponse) error {
	return c.call(ctx, "thread/resume", map[string]any{"threadId": id, "excludeTurns": true}, out)
}

// close unsubscribes (the turn keeps running) and disconnects.
func (s *threadSession) close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.c.call(ctx, "thread/unsubscribe", map[string]any{"threadId": s.thread.ID}, nil)
	s.c.close()
}

// Busy reasons for a send.
const (
	busyWorking      = "Codex is working. Steer instead."
	busyEnded        = "The turn ended; send instead."
	busyNonSteerable = "Codex can't steer a review or compaction turn. Wait for it to finish."
	queueUnavailable = "Codex queues messages only in its own client."
)

func busyFor(t thread) error {
	switch t.Status.Type {
	case statusActive:
		return &execution.Busy{Reason: busyWorking}
	case statusIdle, statusNotLoaded:
		return nil
	default:
		return &execution.Busy{Reason: "Check the thread in Codex before sending again."}
	}
}

// Send delivers to the selected native Codex turn operation.
func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	op, err := m.Delivery.Operation()
	if err != nil {
		return err
	}
	if op == execution.OpQueue {
		return &execution.Invalid{Reason: queueUnavailable}
	}
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	id := ref.ExternalSessionID
	if op == execution.OpSteer {
		return a.steer(ctx, id, m)
	}
	// The native start can also steer an active turn. Serialize Stage sends
	// to this thread and recheck its state at the last possible moment.
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	mu := &a.sends[h.Sum32()%uint32(len(a.sends))]
	mu.Lock()
	defer mu.Unlock()
	return a.sendIdle(ctx, id, m)
}

func (a *Adapter) sendIdle(ctx context.Context, id string, m execution.Message) error {
	t, err := a.read(ctx, id)
	if err != nil {
		return err
	}
	if err := busyFor(t); err != nil {
		return err
	}
	c, _, held := a.heldThread(id)
	var re *RPCError
	if !held || t.Status.Type == statusNotLoaded {
		s, err := a.resume(ctx, id)
		if errors.As(err, &re) && strings.Contains(re.Message, "is archived") {
			return &execution.Refused{Reason: "Codex: " + re.Message}
		}
		if err != nil {
			return err
		}
		defer s.close(ctx)
		if err := busyFor(s.thread); err != nil {
			return err
		}
		c = s.c
	}
	view := "notLoaded"
	if m.RequestID != "" {
		view = "full"
	}
	last, err := a.lastTurn(ctx, id, view)
	if err != nil {
		return err
	}
	if last == nil {
		return &execution.Busy{Reason: "Codex has no finished turn yet."}
	}
	if last.Status == turnInProgress || (last.Status == turnInterrupted && last.CompletedAt == nil) {
		return &execution.Busy{Reason: busyWorking}
	}
	if last.Status != turnCompleted && last.Status != turnInterrupted && last.Status != turnFailed {
		return &execution.Busy{Reason: "Check the thread in Codex before sending again."}
	}
	if last.hasClientMessage(m.RequestID) {
		return nil
	}
	t, err = a.read(ctx, id)
	if err != nil {
		return err
	}
	if err := busyFor(t); err != nil {
		return err
	}
	err = c.call(ctx, "turn/start", messageParams(id, m), nil)
	if errors.As(err, &re) && re.Code == codeInvalidRequest && !unsupported(err) {
		return &execution.Busy{Reason: "Codex: " + re.Message}
	}
	return a.client.deliveryError(err)
}

func (a *Adapter) steer(ctx context.Context, id string, m execution.Message) error {
	t, err := a.read(ctx, id)
	if err != nil {
		return err
	}
	if t.Status.Type != statusActive {
		return &execution.Busy{Reason: busyEnded}
	}
	// Read only the newest turn's ID. thread/read with includeTurns would
	// load the full history, which Codex deprecates for paginated threads.
	last, err := a.lastTurn(ctx, id, "notLoaded")
	if err != nil && !notYet(err) {
		return err
	}
	if last == nil || last.Status != turnInProgress {
		return &execution.Busy{Reason: busyEnded}
	}
	params := messageParams(id, m)
	params["expectedTurnId"] = last.ID
	err = a.client.call(ctx, "turn/steer", params, nil)
	var re *RPCError
	if errors.As(err, &re) && re.Code == codeInvalidRequest {
		var detail struct {
			Info struct {
				NonSteerable *struct {
					TurnKind string `json:"turnKind"`
				} `json:"activeTurnNotSteerable"`
			} `json:"codexErrorInfo"`
		}
		_ = json.Unmarshal(re.Data, &detail)
		switch {
		case notLoaded(err), re.Message == "no active turn to steer", strings.HasPrefix(re.Message, "expected active turn id "):
			return &execution.Busy{Reason: busyEnded}
		case detail.Info.NonSteerable != nil, re.Message == "cannot steer a review turn", re.Message == "cannot steer a compact turn":
			return &execution.Busy{Reason: busyNonSteerable}
		}
	}
	return a.client.deliveryError(err)
}

func (t turn) hasClientMessage(id string) bool {
	return id != "" && slices.ContainsFunc(t.Items, func(it item) bool {
		return it.Type == itemUserMessage && it.ClientID != nil && *it.ClientID == id
	})
}

func messageParams(id string, m execution.Message) map[string]any {
	clientID := m.RequestID
	if clientID == "" {
		clientID = randomID()
	}
	input := []map[string]any{{"type": "text", "text": m.Text, "text_elements": []any{}}}
	return map[string]any{"threadId": id, "input": input, "clientUserMessageId": clientID}
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "stage-" + hex.EncodeToString(b)
}
