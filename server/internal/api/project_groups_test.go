package api_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/principal"
)

func inviteGroupMember(t *testing.T, accounts *auth.Service, name string) auth.SignInResult {
	t.Helper()
	invite, err := accounts.Invite(t.Context(), db.DefaultUser, name+"@example.test")
	invite = mustValue(t, invite, err)
	member, err := accounts.RedeemInvite(t.Context(), notificationToken(t, invite.Link.URL()), name, memberPassphrase)
	return mustValue(t, member, err)
}

func TestMembersShareGroupMembershipAndPreserveWork(t *testing.T) {
	ctx := t.Context()
	e, _, accounts, _, _ := sharedNotificationEnv(t)
	a := inviteGroupMember(t, accounts, "Ada")
	b := inviteGroupMember(t, accounts, "Bea")
	former := inviteGroupMember(t, accounts, "Former")

	pair, err := e.store.CreatePairing(ctx, "worker")
	pair = mustValue(t, pair, err)
	worker, err := e.store.RedeemPairing(ctx, pair.Code, "worker", "")
	worker = mustValue(t, worker, err)
	author := db.WithMachine(db.WithUser(principal.With(ctx, principal.Actor{
		Kind: principal.Agent, AccountID: former.Account.ID, MachineID: worker.MachineID,
	}), former.Account.ID), worker.MachineID)

	const connection = dbtest.T3Connection
	first := execution.WorkspaceInfo{ID: "native-first", Title: "Build", WorkspaceRoot: "/private/alpha"}
	second := execution.WorkspaceInfo{ID: "native-second", Title: "Build", WorkspaceRoot: "/private/beta"}
	selector := db.WorkspaceID(connection, second.ID, second.WorkspaceRoot)
	refresh := func(rows ...execution.WorkspaceInfo) {
		t.Helper()
		// A user-triggered refresh carries an inherited human principal.
		// Discovery records its own internal actor.
		actor := db.WithUser(principal.With(ctx, principal.Actor{Kind: principal.Human, AccountID: a.Account.ID}), a.Account.ID)
		if _, err := e.store.RefreshWorkspaces(actor, connection, rows); err != nil {
			t.Fatal(err)
		}
	}
	bound := func(row execution.WorkspaceInfo) db.Project {
		t.Helper()
		p, err := e.store.InsertProject(author, db.Project{Title: row.Title, Bindings: []db.ProjectBinding{{
			ConnectionID: connection, ExternalProjectID: row.ID, Workspace: row.WorkspaceRoot,
		}}})
		return mustValue(t, p, err)
	}
	projectA, projectB := bound(first), bound(second)
	alias, err := e.store.InsertProject(author, db.Project{Title: "Older Build"})
	alias = mustValue(t, alias, err)
	_, err = e.store.MergeProjects(author, projectB.ID, alias.ID, true)
	mustValue(t, struct{}{}, err)
	bindingID := projectB.Bindings[0].ID
	refresh(first, second)

	work, err := e.store.CreateTask(author, db.NewTask{
		ProjectID: projectB.ID, ConnectionID: connection, BindingID: &bindingID,
		Title: "Original work", Prompt: "Preserve this prompt",
	})
	work = mustValue(t, work, err)
	wantPin := db.WorkspacePin{ExternalID: second.ID, Root: second.WorkspaceRoot}
	if work.WorkspacePin == nil || *work.WorkspacePin != wantPin {
		t.Fatalf("legacy selection did not record the exact task pin: %+v", work.WorkspacePin)
	}
	for _, step := range []db.Transition{
		{TaskID: work.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true,
			LaunchCheck: &db.LaunchCheck{ConnectionID: connection, WorkspacePin: wantPin, Config: work.ExecutionConfig}},
		{TaskID: work.ID, From: []db.TaskState{db.StateLaunching}, To: db.StateDone},
	} {
		result, err := e.store.TransitionTask(author, step)
		if !mustValue(t, result, err).Changed {
			t.Fatalf("could not record prior attempt: %+v", step)
		}
	}
	frozen, err := e.store.LaunchDestination(ctx, work.ID, 1)
	if mustValue(t, frozen, err).Workspace != second.WorkspaceRoot {
		t.Fatalf("wrong original destination: %+v", frozen)
	}
	_, err = e.store.AppendNote(author, db.JournalOwner{TaskID: &work.ID}, db.AppendNoteInput{
		Text: "Keep this journal evidence ✓", RequestID: "journal-by-former",
	})
	mustValue(t, struct{}{}, err)
	completion, err := e.store.CompletionPreview(ctx, work.ID)
	completion = mustValue(t, completion, err)
	_, err = e.store.ChangeAcceptance(author, work.ID, db.AcceptanceRequest{
		RequestID: "accept-by-former", EvidenceVersion: completion.EvidenceVersion,
		Revision: completion.Revision, Note: "Keep this result",
	}, false)
	mustValue(t, struct{}{}, err)
	schedule, err := e.store.CreateSchedule(author, db.NewSchedule{
		ProjectID: projectB.ID, ConnectionID: connection, WorkspaceID: selector,
		Title: "Original schedule", Prompt: "Run here", Cron: "*/5 * * * *", Tz: "UTC",
	})
	schedule = mustValue(t, schedule, err)
	if schedule.WorkspacePin == nil || *schedule.WorkspacePin != wantPin {
		t.Fatalf("exact selection did not record the schedule pin: %+v", schedule.WorkspacePin)
	}
	_, err = e.store.CreateSnippet(author, db.SnippetPatch{
		Name:      db.Optional[string]{Set: true, V: "build"},
		Body:      db.Optional[string]{Set: true, V: "Original snippet"},
		ProjectID: db.Optional[int64]{Set: true, V: projectB.ID},
	})
	mustValue(t, struct{}{}, err)
	if err := accounts.Disable(ctx, db.DefaultUser, former.Account.ID); err != nil {
		t.Fatal(err)
	}

	workSnapshot := func() []string {
		t.Helper()
		// The JSON arrays put raw evidence and attribution text into strings,
		// preserving bytes including whitespace and distinguishing NULL.
		rows, err := testDB.Pool.QueryContext(ctx, `
SELECT 'project' kind,id,json_array(id,title,created_by_user_id,source_machine_id) data FROM projects WHERE id IN ($1,$2)
UNION ALL SELECT 'binding',id,json_array(id,project_id,connection_id,external_project_id,workspace,revision,is_primary,active) FROM project_bindings WHERE project_id IN ($1,$2)
UNION ALL SELECT 'task',id,json_array(id,project_id,connection_id,binding_id,workspace_pin,created_by_user_id,source_machine_id,state,acceptance_revision) FROM tasks WHERE id=$3
UNION ALL SELECT 'destination',attempt,json_array(task_id,attempt,connection_id,external_project_id,workspace,binding_id,binding_revision) FROM task_launch_destinations WHERE task_id=$3
UNION ALL SELECT 'attempt',attempt,json_array(task_id,attempt,intent,attribution) FROM execution_attempts WHERE task_id=$3
UNION ALL SELECT 'schedule',id,json_array(id,project_id,connection_id,binding_id,workspace_pin,created_by_user_id,source_machine_id) FROM schedules WHERE project_id=$2
UNION ALL SELECT 'snippet',id,json_array(id,project_id,name,body) FROM snippets WHERE project_id=$2
UNION ALL SELECT 'event',id,json_array(id,task_id,from_state,to_state,detail,attribution) FROM task_events WHERE task_id=$3
UNION ALL SELECT 'journal',id,json_array(id,task_id,text,created_by_user_id,source_machine_id,request_id,attribution) FROM journal_entries WHERE task_id=$3
UNION ALL SELECT 'acceptance',id,json_array(id,task_id,evidence,note,attribution) FROM task_acceptances WHERE task_id=$3
ORDER BY kind,id`, projectA.ID, projectB.ID, work.ID)
		rows = mustValue(t, rows, err)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var kind, data string
			var id int64
			if err := rows.Scan(&kind, &id, &data); err != nil {
				t.Fatal(err)
			}
			out = append(out, kind+" "+data)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := workSnapshot()

	const editRoute = "/api/project-groups/membership"
	edit := func(member auth.SignInResult, input db.GroupEditInput, status int, out any) {
		t.Helper()
		body, err := json.Marshal(input)
		notifyRequest(t, e, member, "POST", editRoute, string(mustValue(t, body, err)), status, out)
	}
	selected := db.GroupEditInput{
		ConnectionID: connection, WorkspaceID: selector,
	}
	var stale, aboutToGroup db.GroupEdit
	edit(b, selected, 200, &stale)
	toA := selected
	toA.TargetProjectID = &projectA.ID
	edit(a, toA, 200, &aboutToGroup)
	toA.PreviewToken = aboutToGroup.PreviewToken
	var grouped db.GroupEdit
	edit(a, toA, 200, &grouped)
	if !grouped.Applied || grouped.Source.ID != projectB.ID || grouped.Target == nil || grouped.Target.ID != projectA.ID {
		t.Fatalf("unexpected group result: %+v", grouped)
	}

	groupAt := func(member auth.SignInResult, project int64) db.ProjectGroup {
		t.Helper()
		var g db.ProjectGroup
		notifyRequest(t, e, member, "GET", fmt.Sprintf("/api/project-groups/%d", project), "", 200, &g)
		return g
	}
	if g := groupAt(b, projectA.ID); len(g.Members) != 2 {
		t.Fatalf("second member cannot see shared membership: %+v", g)
	}
	if g := groupAt(b, alias.ID); g.ID != projectB.ID || g.State != "empty" {
		t.Fatalf("history link did not retain its project: %+v", g)
	}
	selected.PreviewToken = stale.PreviewToken
	var changed struct {
		Code    string
		Preview db.GroupEdit
	}
	edit(b, selected, 409, &changed)
	if changed.Code != "preview_changed" || changed.Preview.Source.ID != projectA.ID || changed.Preview.PreviewToken == stale.PreviewToken {
		t.Fatalf("unexpected stale edit response: %+v", changed)
	}
	selected.PreviewToken = changed.Preview.PreviewToken
	var separated db.GroupEdit
	edit(b, selected, 200, &separated)
	if !separated.Applied || separated.Target == nil || separated.Target.ID == projectB.ID {
		t.Fatalf("separation failed: %+v", separated)
	}

	fresh := execution.WorkspaceInfo{ID: "fresh-native", Title: "/private/fresh", WorkspaceRoot: "/private/fresh"}
	refresh(first, second, fresh)
	var groupActor store.Attribution
	var creator int64
	var source *int64
	err = testDB.Pool.QueryRowContext(ctx, `
SELECT e.group_attribution,p.created_by_user_id,p.source_machine_id
FROM external_projects e JOIN projects p ON p.id=e.group_project_id WHERE e.external_id=$1`, fresh.ID).
		Scan(&groupActor, &creator, &source)
	if err != nil || groupActor.Actor.Kind != "system" || groupActor.Actor.UserID != 0 || groupActor.Actor.InitiatorUserID != 0 ||
		groupActor.Cause == nil || groupActor.Cause.Kind != "workspace_discovery" || creator != db.DefaultUser || source != nil {
		t.Fatalf("refresh attribution %+v, creator %d, source %v: %v", groupActor, creator, source, err)
	}
	for _, member := range []auth.SignInResult{a, b} {
		g := groupAt(member, separated.Target.ID)
		if len(g.Members) != 1 || g.Members[0].WorkspaceID != selected.WorkspaceID {
			t.Fatalf("refresh lost separation for %s: %+v", member.Account.DisplayName, g)
		}
		var legacy db.Project
		notifyRequest(t, e, member, "GET", fmt.Sprintf("/api/projects/%d", alias.ID), "", 200, &legacy)
		if legacy.ID != projectB.ID || legacy.CreatedBy != former.Account.ID || len(legacy.Bindings) != 1 || legacy.Bindings[0].ID != bindingID {
			t.Fatalf("historical project changed: %+v", legacy)
		}
	}
	if after := workSnapshot(); !slices.Equal(before, after) {
		t.Fatalf("group edits or refresh rewrote work\nbefore %v\nafter  %v", before, after)
	}

	var filtered struct{ Tasks []db.Task }
	notifyRequest(t, e, b, "GET", fmt.Sprintf("/api/tasks?projectId=%d&closed=all", alias.ID), "", 200, &filtered)
	if len(filtered.Tasks) != 1 || filtered.Tasks[0].ID != work.ID {
		t.Fatalf("legacy project filter changed: %+v", filtered)
	}
	agent, err := accounts.RotateAgent(ctx, b.Token.Reveal(), store.SessionBrowser)
	agent = mustValue(t, agent, err)
	agentRequest := func(method, route string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, notificationOrigin+route, nil)
		req.RemoteAddr = "127.0.0.1:32101"
		req.Header.Set("Authorization", "Bearer "+principal.AgentTokenPrefix+agent.Token.Reveal())
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s", method, route, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	agentRequest("POST", editRoute, 403)
	for _, route := range []string{"/api/project-groups", fmt.Sprintf("/api/project-groups/%d", separated.Target.ID), "/api/projects?view=destinations"} {
		body := agentRequest("GET", route, 200)
		for _, private := range []string{"members", "/private/", "native-first", "fresh-native", "relatedProjectIds"} {
			if strings.Contains(body, private) {
				t.Fatalf("%s exposes %q: %s", route, private, body)
			}
		}
	}
	notifyRequest(t, e, former, "POST", editRoute, "{}", 401, nil)
	notifyRequest(t, e, auth.SignInResult{}, "GET", "/api/project-groups", "", 401, nil)
}
