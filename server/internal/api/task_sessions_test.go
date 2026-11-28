package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestTaskSessionHistoryAfterTwoRestarts(t *testing.T) {
	e := newEnv(t)
	project := e.project("/w/session-history")
	task := e.task(project.ID, "Run", nil)
	path := fmt.Sprintf("/api/tasks/%d", task.ID)
	var unlaunched client.TaskDetail
	e.must("GET", path, nil, 200, &unlaunched)
	if unlaunched.Sessions == nil || len(unlaunched.Sessions) != 0 {
		t.Fatalf("unlaunched sessions %+v", unlaunched.Sessions)
	}

	var refs []db.SessionRef
	for attempt := 1; attempt <= 3; attempt++ {
		var started client.Task
		e.must("POST", path+"/start", nil, 200, &started)
		if started.Session == nil || started.LaunchAttempt != attempt {
			t.Fatalf("attempt %d: %+v", attempt, started)
		}
		ref := *started.Session
		refs = append(refs, ref)
		if attempt < 3 {
			if err := e.fake.SetStatus(ref.ExternalSessionID, t3.StatusFailed); err != nil {
				t.Fatal(err)
			}
			e.poll()
			if failed := e.getTask(task.ID); failed.State != "failed" {
				t.Fatalf("attempt %d should be restartable: %+v", attempt, failed)
			}
		}
	}

	ctx := t.Context()
	past, err := e.store.Observation(ctx, refs[0])
	if err != nil {
		t.Fatal(err)
	}
	captured := time.Date(2026, 11, 29, 1, 0, 0, 0, time.UTC)
	past.StateReason, past.Branch, past.Directory = "reason for attempt 1", "past-branch", "/past-work"
	past.Reply = &execution.Reply{ItemID: "past-reply", Text: "First attempt: " + strings.Repeat("界", 4000)}
	past.Usage = &execution.Usage{Turns: 2}
	past.Facts = []execution.Fact{
		{ID: "valid", Label: "Work", Value: "Past", Link: "https://example.test/past"},
		{ID: "unsafe", Label: "Link", Value: "Past", Link: "javascript:unsafe"},
	}
	past.Links = execution.Links{Web: "https://example.test/first"}
	if err := e.store.PutObservationAt(ctx, past, captured); err != nil {
		t.Fatal(err)
	}
	e.fake.RemoveThread(refs[0].ExternalSessionID) // Cached reads survive the harness losing the session.

	var detail client.TaskDetail
	e.must("GET", path, nil, 200, &detail)
	if detail.Session == nil || *detail.Session != refs[2] || len(detail.Sessions) != 3 {
		t.Fatalf("task history %+v", detail.Sessions)
	}
	for i, session := range detail.Sessions {
		attempt := 3 - i
		if session.Attempt != attempt || session.Current != (i == 0) || session.Session != refs[attempt-1] || session.RecordedAt.IsZero() {
			t.Fatalf("history entry %d: %+v", i, session)
		}
	}
	old := detail.Sessions[2]
	if old.State != "failed" || old.StateReason != past.StateReason || old.ObservedAt == nil || !old.ObservedAt.Equal(captured) || old.Links == nil || *old.Links != past.Links {
		t.Fatalf("past summary %+v", old)
	}
	var snapshot struct {
		client.TaskSession
		Reply    *execution.Reply `json:"reply"`
		Usage    *execution.Usage `json:"usage"`
		Facts    []execution.Fact `json:"facts"`
		Reported struct {
			Branch, Directory string
		} `json:"reported"`
	}
	e.must("GET", path+"/sessions/1", nil, 200, &snapshot)
	if snapshot.Current || snapshot.Attempt != 1 || snapshot.Session != refs[0] || snapshot.State != "failed" ||
		snapshot.Reply == nil || snapshot.Reply.ItemID != "past-reply" || !snapshot.Reply.Truncated ||
		!strings.HasPrefix(snapshot.Reply.Text, "First attempt: ") || utf8.RuneCountInString(snapshot.Reply.Text) != 4000 ||
		snapshot.Usage == nil || snapshot.Usage.Turns != 2 || snapshot.Reported.Branch != past.Branch || snapshot.Reported.Directory != past.Directory ||
		len(snapshot.Facts) != 2 || snapshot.Facts[0].Link != "https://example.test/past" || snapshot.Facts[1].Link != "" {
		t.Fatalf("past snapshot %+v", snapshot)
	}

	for _, subpath := range []string{"/sessions/0", "/sessions/4", "/activity?attempt=4"} {
		e.must("GET", path+subpath, nil, 404, nil)
	}
	for _, subpath := range []string{"/sessions/-1", "/activity?attempt="} {
		e.must("GET", path+subpath, nil, 400, nil)
	}
	ref := fmt.Sprintf("/connections/%d/sessions/%s", refs[0].ConnectionID, url.PathEscape(refs[0].ExternalSessionID))
	var resolved client.Ref
	e.must("GET", "/api/refs/resolve?cached=true&ref="+url.QueryEscape(ref), nil, 200, &resolved)
	if resolved.TaskID == nil || *resolved.TaskID != task.ID || resolved.Attempt == nil || *resolved.Attempt != 1 ||
		resolved.Session == nil || *resolved.Session != refs[0] || resolved.Links != past.Links {
		t.Fatalf("historical ref %+v", resolved)
	}
}
