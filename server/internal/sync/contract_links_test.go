package sync_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/cli"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
)

type documentLinkConnector struct {
	harness.Connector
	connectionID int64
}

func (c documentLinkConnector) Summary(context.Context) harness.Summary {
	return harness.Summary{ConnectionID: c.connectionID, Configured: true, Connected: true}
}

func TestContractTaskLinksAndAcceptedHandoff(t *testing.T) {
	forPlacements(t, func(t *testing.T, p placement) {
		store, task := contractTask(t, p)
		fake := &documentHarness{}
		adapters := map[int64]execution.Adapter{task.ConnectionID: fake}
		var svc *coordinator.Service
		if p.remote {
			rig := remotetest.NewWithConnectors(t, remoteMachine, adapters, map[string]harness.Connector{
				task.Harness: documentLinkConnector{connectionID: task.ConnectionID},
			})
			svc = coordinator.NewForMachines(store, rig.Set(), nil)
		} else {
			svc = coordinator.NewWithAdapters(store, adapters, nil)
		}
		svc.Harnesses = documentHarnesses
		ctx := t.Context()
		srv := httptest.NewServer(api.NewRouter(api.Deps{Store: store, Sync: svc, Harnesses: documentHarnesses}))
		t.Cleanup(srv.Close)
		c := client.New(srv.URL)
		home := t.TempDir()
		checkLinks := func(want execution.Links, open string) client.TaskDetail {
			t.Helper()
			detail, err := c.Task(ctx, task.ID)
			if err != nil || detail.Links != want {
				t.Fatalf("task links = %+v, want %+v, error %v", detail.Links, want, err)
			}
			var out, stderr bytes.Buffer
			env := cli.Env{Stdout: &out, Stderr: &stderr, Home: home, Getenv: func(key string) string {
				if key == cli.URLEnv {
					return srv.URL
				}
				return ""
			}}
			if code := cli.Run(ctx, []string{"task", "show", strconv.FormatInt(task.ID, 10)}, env); code != 0 {
				t.Fatalf("task show exited %d: %s", code, &stderr)
			}
			wantOpen := "  open     " + open + "\n"
			if open == "" && strings.Contains(out.String(), "  open     ") || open != "" && !strings.Contains(out.String(), wantOpen) {
				t.Fatalf("task show open link = %q, output %q", open, &out)
			}
			return detail
		}

		checkLinks(execution.Links{}, "")
		launchedLinks := execution.Links{Web: "https://documents.invalid/live", Native: "documents://session/live", Command: "documents resume live"}
		fake.links = &launchedLinks
		launched, err := svc.StartTask(ctx, task.ID)
		if err != nil || launched.Session == nil {
			t.Fatalf("launch %+v: %v", launched, err)
		}
		checkLinks(launchedLinks, launchedLinks.Web)

		publishedLinks := execution.Links{Native: "documents://session/published"}
		fake.links = &publishedLinks
		fake.phase = "published"
		if err := svc.PollTask(ctx, launched); err != nil {
			t.Fatal(err)
		}
		checkLinks(publishedLinks, publishedLinks.Native)
		preview, err := store.CompletionPreview(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ChangeAcceptance(ctx, task.ID, db.AcceptanceRequest{
			RequestID: "accept-links", Revision: preview.Revision, EvidenceVersion: preview.EvidenceVersion,
		}, false); err != nil {
			t.Fatal(err)
		}

		// The latest observation can explicitly remove every link.
		o, err := store.Observation(ctx, *launched.Session)
		if err != nil {
			t.Fatal(err)
		}
		o.Links = execution.Links{}
		o.UpdatedAt = o.UpdatedAt.Add(time.Second)
		if err := store.PutObservation(ctx, o); err != nil {
			t.Fatal(err)
		}
		detail := checkLinks(execution.Links{}, "")
		if len(detail.Acceptances) != 1 || detail.Acceptances[0].Evidence.SessionLink == nil || *detail.Acceptances[0].Evidence.SessionLink != publishedLinks.Native {
			t.Fatalf("accepted links %+v", detail.Acceptances)
		}

		next, err := store.CreateTask(ctx, db.NewTask{
			ProjectID: task.ProjectID, ConnectionID: task.ConnectionID, Title: "Follow-up", Prompt: "Build on the result",
			ExecutionConfig: task.ExecutionConfig, Needs: []int64{task.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StartTask(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fake.input.Prompt, publishedLinks.Native) || strings.Contains(fake.input.Prompt, launchedLinks.Web) {
			t.Fatalf("prompt lost the accepted handoff link: %q", fake.input.Prompt)
		}
	})
}
