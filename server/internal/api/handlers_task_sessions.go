package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type taskSessionStateJSON struct {
	Attempt     int              `json:"attempt"`
	Session     db.SessionRef    `json:"session"`
	Current     bool             `json:"current"`
	State       string           `json:"state"`
	StateReason string           `json:"stateReason"`
	ObservedAt  *time.Time       `json:"observedAt"`
	Links       *execution.Links `json:"links,omitempty"`
}

type taskSessionJSON struct {
	taskSessionStateJSON
	RecordedAt time.Time `json:"recordedAt"`
}

type taskSessionSnapshotJSON struct {
	taskSessionStateJSON
	Reply    *execution.Reply `json:"reply"`
	Usage    *execution.Usage `json:"usage"`
	Facts    []execution.Fact `json:"facts"`
	Reported *reportedJSON    `json:"reported,omitempty"`
}

func taskSessionState(session db.TaskSession) taskSessionStateJSON {
	o := session.Observation
	out := taskSessionStateJSON{
		Attempt: session.Attempt, Session: session.Session, Current: session.Current,
		State: o.State, StateReason: o.StateReason,
	}
	if !o.CapturedAt.IsZero() {
		out.ObservedAt = &o.CapturedAt
	}
	if o.Links != (execution.Links{}) {
		out.Links = &o.Links
	}
	return out
}

func parseTaskAttempt(raw string) (int, error) {
	attempt, err := strconv.Atoi(raw)
	if err != nil || attempt < 0 {
		return 0, db.Invalid("attempt must be a nonnegative integer")
	}
	return attempt, nil
}

func (s *server) getTaskSession(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	attempt, err := parseTaskAttempt(r.PathValue("attempt"))
	if err != nil {
		return err
	}
	session, err := s.Store.TaskSession(r.Context(), id, attempt)
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("attempt %d has no recorded session on task %d", attempt, id))
	}
	o := session.Observation
	d, _ := s.registry().Lookup(session.Harness)
	out := taskSessionSnapshotJSON{
		taskSessionStateJSON: taskSessionState(session),
		Reply: boundedTaskReply(o.Reply), Usage: o.Usage, Facts: d.DisplayFacts(o.Facts),
	}
	if out.Facts == nil {
		out.Facts = []execution.Fact{}
	}
	if o.Branch != "" || o.Directory != "" {
		out.Reported = &reportedJSON{Branch: o.Branch, Directory: o.Directory}
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// Replies are plain markdown in JSON. The UI sanitizes the rendered HTML.
func boundedTaskReply(reply *execution.Reply) *execution.Reply {
	const maxChars = 4000
	if reply == nil || utf8.RuneCountInString(reply.Text) <= maxChars {
		return reply
	}
	bounded := *reply
	bounded.Text = string([]rune(reply.Text)[:maxChars])
	bounded.Truncated = true
	return &bounded
}
