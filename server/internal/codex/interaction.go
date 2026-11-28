package codex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.Interactive = (*Adapter)(nil)

// threadSession is a connection subscribed to one thread.
type threadSession struct {
	c      *conn
	thread thread
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
	return &threadSession{c: c, thread: res.Thread}, nil
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

// waiting reports a thread waiting on an approval or user input.
func waiting(t thread) bool {
	return t.Status.Type == statusActive &&
		(slices.Contains(t.Status.ActiveFlags, "waitingOnApproval") || slices.Contains(t.Status.ActiveFlags, "waitingOnUserInput"))
}

// Busy reasons for a send.
const (
	busyWorking = "Codex is working. Send once it finishes."
	busyWaiting = "Codex is waiting on an approval or question. Answer that first."
)

func busyFor(t thread) error {
	if t.Status.Type != statusActive {
		return nil
	}
	if waiting(t) {
		return &execution.Busy{Reason: busyWaiting}
	}
	return &execution.Busy{Reason: busyWorking}
}

// Send starts a turn with the message on an idle thread: thread/resume,
// turn/start with the message's request id as its client message id, then
// unsubscribe. A thread with a turn in progress is busy (Stage never
// steers a running turn). A message whose request id the newest turn
// already carries was delivered: nothing is sent again. An archived thread
// is *execution.Refused.
func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	if err := m.Delivery.RequireSend(); err != nil {
		return err
	}
	if !a.valid(ref) {
		return execution.ErrNotFound
	}
	t, err := a.read(ctx, ref.ExternalSessionID)
	if err != nil {
		return err
	}
	if err := busyFor(t); err != nil {
		return err
	}
	s, err := a.resume(ctx, t.ID)
	var re *RPCError
	if errors.As(err, &re) && strings.Contains(re.Message, "is archived") {
		// Codex takes no message until the user unarchives it there.
		return &execution.Refused{Reason: "Codex: " + re.Message}
	}
	if err != nil {
		return err
	}
	defer s.close(ctx)
	if err := busyFor(s.thread); err != nil {
		return err
	}
	clientID := m.RequestID
	if clientID == "" {
		clientID = randomID()
	} else if delivered, err := s.delivered(ctx, clientID); err != nil {
		return err
	} else if delivered {
		return nil
	}
	input := []map[string]any{{"type": "text", "text": m.Text, "text_elements": []any{}}}
	err = s.c.call(ctx, "turn/start", map[string]any{"threadId": t.ID, "input": input, "clientUserMessageId": clientID}, nil)
	if errors.As(err, &re) && re.Code == codeInvalidRequest {
		// Refused (a turn started meanwhile, or the thread can't run one):
		// nothing was sent.
		return &execution.Busy{Reason: "Codex: " + re.Message}
	}
	return sessionErr(err)
}

// delivered reports whether the newest turn's user message carries
// clientID.
func (s *threadSession) delivered(ctx context.Context, clientID string) (bool, error) {
	var page turnsPage
	if err := s.c.call(ctx, "thread/turns/list", map[string]any{"threadId": s.thread.ID, "limit": 1, "sortDirection": "desc", "itemsView": "full"}, &page); err != nil {
		return false, sessionErr(err)
	}
	for _, tu := range page.Data {
		for _, it := range tu.Items {
			if it.Type == itemUserMessage && it.ClientID != nil && *it.ClientID == clientID {
				return true, nil
			}
		}
	}
	return false, nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "stage-" + hex.EncodeToString(b)
}
