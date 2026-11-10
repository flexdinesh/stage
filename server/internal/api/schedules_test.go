package api_test

import (
	"fmt"
	"testing"
	"time"
)

type schedule struct {
	ID            int64        `json:"id"`
	ProjectID     int64        `json:"projectId"`
	ProjectTitle  string       `json:"projectTitle"`
	Title         string       `json:"title"`
	Cron          string       `json:"cron"`
	Tz            string       `json:"tz"`
	Paused        bool         `json:"paused"`
	NextRunAt     *time.Time   `json:"nextRunAt"`
	LastRunAt     *time.Time   `json:"lastRunAt"`
	LastTaskID    *int64       `json:"lastTaskId"`
	LastTaskState *string      `json:"lastTaskState"`
	Attachments   []attachment `json:"attachments"`
	Runs          []struct {
		Outcome string `json:"outcome"`
		TaskID  *int64 `json:"taskId"`
		Detail  string `json:"detail"`
		Task    *task  `json:"task"`
	} `json:"runs"`
}

type startTask struct {
	task
	StartAt       *time.Time `json:"startAt"`
	StartTz       *string    `json:"startTz"`
	ScheduleID    *int64     `json:"scheduleId"`
	ScheduleTitle *string    `json:"scheduleTitle"`
}

func TestTaskStartTime(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/start")
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	wall := time.Now().In(loc).Add(2 * time.Hour).Truncate(time.Minute)
	local := wall.Format("2006-01-02T15:04")
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"projectId": p.ID, "title": "later", "prompt": "x"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	var tk startTask
	e.must("POST", "/api/tasks", body(map[string]any{"startLocal": local, "startTz": "Asia/Kolkata"}), 201, &tk)
	if tk.State != "backlog" || tk.StartAt == nil || !tk.StartAt.Equal(wall) || tk.StartTz == nil || *tk.StartTz != "Asia/Kolkata" {
		t.Fatalf("created = %+v start %v", tk.task, tk.StartAt)
	}

	other := e.task(p.ID, "other", nil)
	past := time.Now().In(loc).Add(-time.Hour).Format("2006-01-02T15:04")
	for name, extra := range map[string]map[string]any{
		"with afterTaskId": {"startLocal": local, "startTz": "Asia/Kolkata", "afterTaskId": other.ID},
		"with start":       {"startLocal": local, "startTz": "Asia/Kolkata", "start": true},
		"past":             {"startLocal": past, "startTz": "Asia/Kolkata"},
		"bad tz":           {"startLocal": local, "startTz": "Nope/Zone"},
		"no tz":            {"startLocal": local},
		"bad format":       {"startLocal": "tomorrow", "startTz": "UTC"},
	} {
		if code := e.do("POST", "/api/tasks", body(extra), nil); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}

	path := fmt.Sprintf("/api/tasks/%d", tk.ID)
	e.must("PATCH", path, map[string]any{"afterTaskId": other.ID}, 400, nil)
	e.must("PATCH", path, map[string]any{"startLocal": local}, 400, nil)
	var got startTask
	e.must("PATCH", path, map[string]any{"startLocal": nil, "startTz": nil, "afterTaskId": other.ID}, 200, &got)
	if got.StartAt != nil || got.AfterTaskID == nil {
		t.Fatalf("switch to after = %+v", got)
	}
	e.must("PATCH", path, map[string]any{"startLocal": local, "startTz": "UTC", "afterTaskId": nil}, 200, &got)
	if got.StartAt == nil || got.AfterTaskID != nil || *got.StartTz != "UTC" {
		t.Fatalf("switch to start = %+v", got)
	}

	// Start now clears the start time.
	e.must("POST", path+"/start", nil, 200, &got)
	if got.State != "running" || got.StartAt != nil || got.StartTz != nil {
		t.Fatalf("started = %+v", got)
	}
	e.must("PATCH", path, map[string]any{"startLocal": local, "startTz": "UTC"}, 409, nil)
	var dup startTask
	e.must("POST", path+"/duplicate", nil, 201, &dup)
	if dup.StartAt != nil || dup.ScheduleID != nil {
		t.Fatalf("duplicate = %+v", dup)
	}
}

func TestSchedulesAPI(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/sched")
	img := e.mustUpload("s.png")
	base := map[string]any{"projectId": p.ID, "title": "nightly", "prompt": "bump deps", "tz": "Asia/Kolkata"}
	with := func(extra map[string]any) map[string]any {
		b := map[string]any{}
		for k, v := range base {
			b[k] = v
		}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	for name, extra := range map[string]map[string]any{
		"bad cron":    {"cron": "61 * * * *"},
		"every":       {"cron": "@every 5m"},
		"cron tz":     {"cron": "CRON_TZ=UTC 0 2 * * *"},
		"bad tz":      {"cron": "0 2 * * *", "tz": "Nope/Zone"},
		"no cron":     {},
		"no title":    {"cron": "0 2 * * *", "title": ""},
		"startLocal":  {"cron": "0 2 * * *", "startLocal": "2030-01-01T00:00", "startTz": "UTC"},
		"afterTaskId": {"cron": "0 2 * * *", "afterTaskId": 1},
	} {
		if code := e.do("POST", "/api/schedules", with(extra), nil); code != 400 {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	e.must("POST", "/api/schedules", with(map[string]any{"cron": "0 2 * * *", "projectId": 999}), 404, nil)

	var sc schedule
	e.must("POST", "/api/schedules", with(map[string]any{"cron": "0 2 * * *", "attachmentIds": []int64{img.ID}}), 201, &sc)
	if sc.Cron != "0 2 * * *" || sc.Tz != "Asia/Kolkata" || sc.ProjectTitle == "" || sc.NextRunAt == nil ||
		!sc.NextRunAt.After(time.Now()) || len(sc.Attachments) != 1 || sc.LastRunAt != nil || sc.LastTaskID != nil {
		t.Fatalf("created = %+v", sc)
	}
	// 02:00 IST is 20:30 UTC.
	if h, m := sc.NextRunAt.UTC().Hour(), sc.NextRunAt.UTC().Minute(); h != 20 || m != 30 {
		t.Fatalf("nextRunAt = %v", sc.NextRunAt)
	}
	// Schedule images are not staged uploads: a task cannot take them.
	e.must("POST", "/api/tasks", map[string]any{"projectId": p.ID, "title": "steal", "prompt": "x", "attachmentIds": []int64{img.ID}}, 400, nil)

	path := fmt.Sprintf("/api/schedules/%d", sc.ID)
	var tk startTask
	e.must("POST", path+"/run", nil, 201, &tk)
	if tk.State != "running" || tk.ScheduleID == nil || *tk.ScheduleID != sc.ID || tk.ScheduleTitle == nil || *tk.ScheduleTitle != "nightly" ||
		len(tk.Attachments) != 1 || tk.Attachments[0].ID == img.ID || tk.StartAt != nil {
		t.Fatalf("run now = %+v", tk)
	}
	if last := e.fake.Launched[len(e.fake.Launched)-1]; len(last.Attachments) != 1 {
		t.Fatalf("launched without image: %+v", last)
	}

	var detail schedule
	e.must("GET", path, nil, 200, &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].Outcome != "manual" || detail.Runs[0].Task == nil || detail.Runs[0].Task.ID != tk.ID ||
		detail.LastTaskID == nil || *detail.LastTaskID != tk.ID || detail.LastTaskState == nil || *detail.LastTaskState != "running" {
		t.Fatalf("detail = %+v", detail)
	}
	if !detail.NextRunAt.Equal(*sc.NextRunAt) {
		t.Fatalf("run now moved nextRunAt: %v -> %v", sc.NextRunAt, detail.NextRunAt)
	}

	var got schedule
	e.must("PATCH", path, map[string]any{"paused": true}, 200, &got)
	if !got.Paused || got.NextRunAt != nil {
		t.Fatalf("paused = %+v", got)
	}
	var list struct {
		Schedules []schedule `json:"schedules"`
	}
	e.must("GET", "/api/schedules?dueWithin=48h", nil, 200, &list)
	if len(list.Schedules) != 0 {
		t.Fatalf("paused listed as due: %+v", list.Schedules)
	}
	e.must("PATCH", path, map[string]any{"paused": false, "cron": "30 9 * * 1-5", "title": "weekday"}, 200, &got)
	if got.Paused || got.NextRunAt == nil || got.Cron != "30 9 * * 1-5" || got.Title != "weekday" {
		t.Fatalf("resumed = %+v", got)
	}
	e.must("PATCH", path, map[string]any{"cron": "@every 1m"}, 400, nil)
	e.must("PATCH", path, map[string]any{"tz": nil}, 400, nil)
	e.must("PATCH", path, map[string]any{"attachmentIds": []int64{}}, 200, &got)
	if len(got.Attachments) != 0 {
		t.Fatalf("images after clear = %+v", got.Attachments)
	}
	e.must("GET", "/api/schedules?dueWithin=200h", nil, 200, &list)
	if len(list.Schedules) != 1 {
		t.Fatalf("dueWithin list = %+v", list.Schedules)
	}
	e.must("GET", fmt.Sprintf("/api/schedules?project=%d", p.ID+1), nil, 200, &list)
	if len(list.Schedules) != 0 {
		t.Fatalf("other project list = %+v", list.Schedules)
	}
	e.must("GET", "/api/schedules?dueWithin=soon", nil, 400, nil)

	var preview struct {
		Next []time.Time `json:"next"`
	}
	e.must("POST", "/api/schedules/preview", map[string]any{"cron": "0 2 * * 1-5", "tz": "Asia/Kolkata"}, 200, &preview)
	if len(preview.Next) != 3 || !preview.Next[0].After(time.Now()) || !preview.Next[1].After(preview.Next[0]) {
		t.Fatalf("preview = %+v", preview)
	}
	e.must("POST", "/api/schedules/preview", map[string]any{"cron": "nope", "tz": "UTC"}, 400, nil)

	e.must("DELETE", path, nil, 204, nil)
	e.must("GET", path, nil, 404, nil)
	e.must("DELETE", path, nil, 404, nil)
	var after startTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", tk.ID), nil, 200, &after)
	if after.ScheduleID != nil || after.ScheduleTitle != nil {
		t.Fatalf("task after schedule delete = %+v", after)
	}
}
